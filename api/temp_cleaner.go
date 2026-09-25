package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"telecloud/database"
	"telecloud/tgclient"
)

var tempCleanupMu sync.Mutex

// TempCleanupResult contains metrics on cleaned temporary files.
type TempCleanupResult struct {
	FilesCleaned   int    `json:"files_cleaned"`
	BytesCleaned   int64  `json:"bytes_cleaned"`
	FormattedBytes string `json:"formatted_bytes"`
}

// TempStatusResult contains metrics on current temporary files usage.
type TempStatusResult struct {
	Count         int    `json:"count"`
	SizeBytes     int64  `json:"size_bytes"`
	FormattedSize string `json:"formatted_size"`
}

// CleanTempFiles cleans stale or abandoned temporary upload files.
// If forceStale is false, only files older than staleThreshold are cleaned.
// If forceStale is true, all non-active temporary files are cleaned.
// In ALL cases, active uploads (in ChunkTracker or UploadTasks) are NEVER deleted.
func CleanTempFiles(tempDir string, staleThreshold time.Duration, forceStale bool) (*TempCleanupResult, error) {
	tempCleanupMu.Lock()
	defer tempCleanupMu.Unlock()

	result := &TempCleanupResult{}
	if tempDir == "" {
		return result, nil
	}

	cleanDir := filepath.Clean(tempDir)
	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read temp dir: %w", err)
	}

	now := time.Now()

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(cleanDir, name)

		// Strict path validation to prevent path traversal
		rel, err := filepath.Rel(cleanDir, fullPath)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Extract task ID to check whether an upload is actively writing
		taskID := extractTaskID(name)
		if taskID != "" {
			if HasChunkTracker(taskID) || tgclient.IsTaskActive(taskID) {
				// Actively being written or uploaded -- NEVER delete!
				continue
			}
		}

		// Check staleness threshold
		if !forceStale && staleThreshold > 0 {
			if now.Sub(info.ModTime()) < staleThreshold {
				// File is newer than stale threshold, preserve it
				continue
			}
		}

		// Safe to remove
		fileSize := info.Size()
		if entry.IsDir() {
			// Walk directory to get total size before removal
			var dirSize int64
			_ = filepath.Walk(fullPath, func(_ string, fi os.FileInfo, e error) error {
				if e == nil && fi != nil && !fi.IsDir() {
					dirSize += fi.Size()
				}
				return nil
			})
			if err := os.RemoveAll(fullPath); err == nil {
				result.FilesCleaned++
				result.BytesCleaned += dirSize
			}
		} else {
			if err := os.Remove(fullPath); err == nil {
				result.FilesCleaned++
				result.BytesCleaned += fileSize
			}
		}

		// Clean up DB tracking tables if associated with a task
		if taskID != "" {
			DeleteChunkTracker(taskID)
			if database.DB != nil {
				_, _ = database.DB.Exec("DELETE FROM upload_chunks WHERE task_id = ?", taskID)
				_, _ = database.DB.Exec("DELETE FROM upload_tasks WHERE id = ?", taskID)
			}
		}
	}

	result.FormattedBytes = formatBytes(result.BytesCleaned)
	return result, nil
}

// GetTempStatus calculates the count and size of temporary files.
func GetTempStatus(tempDir string) (*TempStatusResult, error) {
	result := &TempStatusResult{}
	if tempDir == "" {
		return result, nil
	}

	cleanDir := filepath.Clean(tempDir)
	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result.Count++
		if entry.IsDir() {
			fullPath := filepath.Join(cleanDir, entry.Name())
			_ = filepath.Walk(fullPath, func(_ string, fi os.FileInfo, e error) error {
				if e == nil && fi != nil && !fi.IsDir() {
					result.SizeBytes += fi.Size()
				}
				return nil
			})
		} else {
			result.SizeBytes += info.Size()
		}
	}

	result.FormattedSize = formatBytes(result.SizeBytes)
	return result, nil
}

// extractTaskID extracts the taskID prefix from temporary file or folder names.
func extractTaskID(filename string) string {
	clean := filename
	for _, prefix := range []string{"ytdlp_", "torrent_", "remote_"} {
		if strings.HasPrefix(clean, prefix) {
			clean = strings.TrimPrefix(clean, prefix)
			break
		}
	}

	if idx := strings.Index(clean, "_"); idx != -1 {
		return clean[:idx]
	}
	return clean
}

// handleCleanTempFiles handles POST /api/settings/temp/clean (admin only).
func (h *Handler) handleCleanTempFiles(c *gin.Context) {
	if !c.GetBool("is_admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	tempDir := ""
	if h.cfg != nil {
		tempDir = h.cfg.TempDir
	}
	if tempDir == "" {
		c.JSON(http.StatusOK, gin.H{
			"status":          "success",
			"files_cleaned":   0,
			"bytes_cleaned":   0,
			"formatted_bytes": "0 B",
		})
		return
	}

	result, err := CleanTempFiles(tempDir, 0, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_clean_temp_files"})
		return
	}

	database.LogAuditFromCtx(c, c.GetString("username"), database.AuditActionSetupConfig, fmt.Sprintf("cleaned_%d_temp_files", result.FilesCleaned), database.AuditStatusOK)

	c.JSON(http.StatusOK, gin.H{
		"status":          "success",
		"files_cleaned":   result.FilesCleaned,
		"bytes_cleaned":   result.BytesCleaned,
		"formatted_bytes": result.FormattedBytes,
	})
}

// handleGetTempStatus handles GET /api/settings/temp/status (admin only).
func (h *Handler) handleGetTempStatus(c *gin.Context) {
	if !c.GetBool("is_admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	tempDir := ""
	if h.cfg != nil {
		tempDir = h.cfg.TempDir
	}

	status, err := GetTempStatus(tempDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_get_temp_status"})
		return
	}

	c.JSON(http.StatusOK, status)
}
