package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"telecloud/config"
	"telecloud/tgclient"
)

func TestTempCleaner_StaleSweep(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "telecloud_temp_sweep_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create 3 files:
	// 1. Stale file (2 hours old, 1024 bytes)
	staleFile := filepath.Join(tempDir, "stale_upload.tmp")
	staleData := make([]byte, 1024)
	if err := os.WriteFile(staleFile, staleData, 0644); err != nil {
		t.Fatalf("failed to write stale file: %v", err)
	}
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(staleFile, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("failed to set modtime on stale file: %v", err)
	}

	// 2. Fresh file (10 minutes old, 2048 bytes)
	freshFile := filepath.Join(tempDir, "fresh_upload.tmp")
	freshData := make([]byte, 2048)
	if err := os.WriteFile(freshFile, freshData, 0644); err != nil {
		t.Fatalf("failed to write fresh file: %v", err)
	}
	tenMinsAgo := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(freshFile, tenMinsAgo, tenMinsAgo); err != nil {
		t.Fatalf("failed to set modtime on fresh file: %v", err)
	}

	// 3. Stale folder (e.g. completed/abandoned torrent folder)
	staleFolder := filepath.Join(tempDir, "torrent_abandoned123")
	if err := os.MkdirAll(staleFolder, 0755); err != nil {
		t.Fatalf("failed to make stale folder: %v", err)
	}
	subFile := filepath.Join(staleFolder, "inner.bin")
	if err := os.WriteFile(subFile, make([]byte, 512), 0644); err != nil {
		t.Fatalf("failed to write subFile: %v", err)
	}
	if err := os.Chtimes(staleFolder, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("failed to set modtime on stale folder: %v", err)
	}

	// Run sweep with 1-hour stale threshold
	result, err := CleanTempFiles(tempDir, 1*time.Hour, false)
	if err != nil {
		t.Fatalf("CleanTempFiles failed: %v", err)
	}

	// Verify counts: 2 entries cleaned (staleFile + staleFolder)
	if result.FilesCleaned != 2 {
		t.Errorf("expected 2 cleaned entries, got %d", result.FilesCleaned)
	}
	expectedBytes := int64(1024 + 512)
	if result.BytesCleaned != expectedBytes {
		t.Errorf("expected %d bytes cleaned, got %d", expectedBytes, result.BytesCleaned)
	}

	// Stale file should be gone
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Errorf("expected staleFile to be deleted, but it still exists")
	}
	// Stale folder should be gone
	if _, err := os.Stat(staleFolder); !os.IsNotExist(err) {
		t.Errorf("expected staleFolder to be deleted, but it still exists")
	}
	// Fresh file should STILL exist
	if _, err := os.Stat(freshFile); os.IsNotExist(err) {
		t.Errorf("expected freshFile to be preserved, but it was deleted!")
	}
}

func TestTempCleaner_ActiveUploadProtection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "telecloud_temp_active_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	activeTaskID := "activeTask999"
	activeFile := filepath.Join(tempDir, activeTaskID+"_chunk1.tmp")
	if err := os.WriteFile(activeFile, []byte("in_flight_chunk_data"), 0644); err != nil {
		t.Fatalf("failed to write active file: %v", err)
	}
	// Make it older than 1h to prove active check overrides age
	oldTime := time.Now().Add(-5 * time.Hour)
	_ = os.Chtimes(activeFile, oldTime, oldTime)

	// Register chunk tracker for activeTaskID
	chunkTrackerSync.Store(activeTaskID, &sync.Mutex{})
	defer chunkTrackerSync.Delete(activeTaskID)

	// Another task: tgclient active upload task
	tgActiveTaskID := "tgActive888"
	tgActiveFile := filepath.Join(tempDir, tgActiveTaskID+"_file.mkv")
	if err := os.WriteFile(tgActiveFile, []byte("telegram_uploading_data"), 0644); err != nil {
		t.Fatalf("failed to write tg active file: %v", err)
	}
	_ = os.Chtimes(tgActiveFile, oldTime, oldTime)

	tgclient.SetTaskStatusForTest(tgActiveTaskID, "uploading")
	defer tgclient.RemoveTaskForTest(tgActiveTaskID)

	// Abandoned file with no active tracker
	abandonedFile := filepath.Join(tempDir, "abandoned777_dead.tmp")
	if err := os.WriteFile(abandonedFile, []byte("dead_file"), 0644); err != nil {
		t.Fatalf("failed to write abandoned file: %v", err)
	}
	_ = os.Chtimes(abandonedFile, oldTime, oldTime)

	// Run cleanup with forceStale = true (simulating aggressive admin clean)
	result, err := CleanTempFiles(tempDir, 0, true)
	if err != nil {
		t.Fatalf("CleanTempFiles failed: %v", err)
	}

	// Only abandonedFile should have been cleaned
	if result.FilesCleaned != 1 {
		t.Errorf("expected 1 file cleaned, got %d", result.FilesCleaned)
	}

	// Active chunk file MUST still exist
	if _, err := os.Stat(activeFile); os.IsNotExist(err) {
		t.Errorf("CRITICAL: active chunk tracker file was deleted while upload was in progress!")
	}

	// Active TG file MUST still exist
	if _, err := os.Stat(tgActiveFile); os.IsNotExist(err) {
		t.Errorf("CRITICAL: active tgclient file was deleted while upload was in progress!")
	}

	// Abandoned file must be gone
	if _, err := os.Stat(abandonedFile); !os.IsNotExist(err) {
		t.Errorf("expected abandoned file to be deleted")
	}

	// Now unregister chunk tracker and finish TG task, then clean again
	chunkTrackerSync.Delete(activeTaskID)
	tgclient.RemoveTaskForTest(tgActiveTaskID)

	result2, err := CleanTempFiles(tempDir, 0, true)
	if err != nil {
		t.Fatalf("CleanTempFiles second run failed: %v", err)
	}

	if result2.FilesCleaned != 2 {
		t.Errorf("expected remaining 2 files to be cleaned after tasks finished, got %d", result2.FilesCleaned)
	}

	if _, err := os.Stat(activeFile); !os.IsNotExist(err) {
		t.Errorf("activeFile should now be deleted after task ended")
	}
	if _, err := os.Stat(tgActiveFile); !os.IsNotExist(err) {
		t.Errorf("tgActiveFile should now be deleted after task ended")
	}
}

func TestTempCleaner_ExtractTaskID(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"abc123_part1.tmp", "abc123"},
		{"ytdlp_vid456_chunk.mp4", "vid456"},
		{"torrent_hash789", "hash789"},
		{"remote_url999_data", "url999"},
		{"plainfile.txt", "plainfile.txt"},
	}

	for _, tc := range cases {
		actual := extractTaskID(tc.input)
		if actual != tc.expected {
			t.Errorf("extractTaskID(%q) = %q, expected %q", tc.input, actual, tc.expected)
		}
	}
}

func TestTempCleaner_AdminEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tempDir, err := os.MkdirTemp("", "telecloud_api_clean_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create 2 test files
	_ = os.WriteFile(filepath.Join(tempDir, "file1.tmp"), make([]byte, 100), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "file2.tmp"), make([]byte, 200), 0644)

	cfg := &config.Config{TempDir: tempDir}
	handler := &Handler{cfg: cfg}

	// 1. Test non-admin access to POST /api/settings/temp/clean -> 403 Forbidden
	router := gin.New()
	router.POST("/api/settings/temp/clean", func(c *gin.Context) {
		c.Set("is_admin", false)
		c.Next()
	}, handler.handleCleanTempFiles)
	router.GET("/api/settings/temp/status", func(c *gin.Context) {
		c.Set("is_admin", false)
		c.Next()
	}, handler.handleGetTempStatus)

	reqCleanNonAdmin, _ := http.NewRequest("POST", "/api/settings/temp/clean", nil)
	wCleanNonAdmin := httptest.NewRecorder()
	router.ServeHTTP(wCleanNonAdmin, reqCleanNonAdmin)
	if wCleanNonAdmin.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for non-admin clean, got %d", wCleanNonAdmin.Code)
	}

	reqStatusNonAdmin, _ := http.NewRequest("GET", "/api/settings/temp/status", nil)
	wStatusNonAdmin := httptest.NewRecorder()
	router.ServeHTTP(wStatusNonAdmin, reqStatusNonAdmin)
	if wStatusNonAdmin.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for non-admin status, got %d", wStatusNonAdmin.Code)
	}

	// 2. Test admin access to GET /api/settings/temp/status -> 200 OK
	adminRouter := gin.New()
	adminRouter.POST("/api/settings/temp/clean", func(c *gin.Context) {
		c.Set("is_admin", true)
		c.Set("username", "admin")
		c.Next()
	}, handler.handleCleanTempFiles)
	adminRouter.GET("/api/settings/temp/status", func(c *gin.Context) {
		c.Set("is_admin", true)
		c.Set("username", "admin")
		c.Next()
	}, handler.handleGetTempStatus)

	reqStatusAdmin, _ := http.NewRequest("GET", "/api/settings/temp/status", nil)
	wStatusAdmin := httptest.NewRecorder()
	adminRouter.ServeHTTP(wStatusAdmin, reqStatusAdmin)
	if wStatusAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin status, got %d", wStatusAdmin.Code)
	}

	var statusResp TempStatusResult
	if err := json.Unmarshal(wStatusAdmin.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}
	if statusResp.Count != 2 {
		t.Errorf("expected count 2, got %d", statusResp.Count)
	}
	if statusResp.SizeBytes != 300 {
		t.Errorf("expected size_bytes 300, got %d", statusResp.SizeBytes)
	}

	// 3. Test admin access to POST /api/settings/temp/clean -> 200 OK
	reqCleanAdmin, _ := http.NewRequest("POST", "/api/settings/temp/clean", nil)
	wCleanAdmin := httptest.NewRecorder()
	adminRouter.ServeHTTP(wCleanAdmin, reqCleanAdmin)
	if wCleanAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin clean, got %d", wCleanAdmin.Code)
	}

	var cleanResp map[string]interface{}
	if err := json.Unmarshal(wCleanAdmin.Body.Bytes(), &cleanResp); err != nil {
		t.Fatalf("failed to decode clean response: %v", err)
	}
	if cleanResp["status"] != "success" {
		t.Errorf("expected status 'success', got %v", cleanResp["status"])
	}
	if int(cleanResp["files_cleaned"].(float64)) != 2 {
		t.Errorf("expected 2 files_cleaned, got %v", cleanResp["files_cleaned"])
	}
	if int64(cleanResp["bytes_cleaned"].(float64)) != 300 {
		t.Errorf("expected 300 bytes_cleaned, got %v", cleanResp["bytes_cleaned"])
	}
}

func BenchmarkTempCleaner_CleanOperation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		tempDir, err := os.MkdirTemp("", "telecloud_bench_clean_*")
		if err != nil {
			b.Fatalf("failed to create temp dir: %v", err)
		}

		// Create 100 small files
		for j := 0; j < 100; j++ {
			f := filepath.Join(tempDir, filepath.Base(tempDir)+string(rune('a'+j%26))+".tmp")
			_ = os.WriteFile(f, []byte("benchmark data"), 0644)
		}

		b.StartTimer()
		_, _ = CleanTempFiles(tempDir, 0, true)
		b.StopTimer()

		_ = os.RemoveAll(tempDir)
	}
}
