package api

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"telecloud/config"
	"telecloud/database"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupUploadTestEnv(t *testing.T) (*Handler, *config.Config, func()) {
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_chunk.db")

	err := database.InitDB("sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	cfg := &config.Config{
		TempDir: tempDir,
	}

	h := &Handler{
		cfg: cfg,
	}

	cleanup := func() {
		database.CloseDB()
	}

	return h, cfg, cleanup
}

func sendUploadChunk(h *Handler, taskID string, chunkIndex, totalChunks int, data []byte) (*httptest.ResponseRecorder, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "testfile.bin")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		return nil, err
	}

	_ = writer.WriteField("task_id", taskID)
	_ = writer.WriteField("filename", "testfile.bin")
	_ = writer.WriteField("path", "/")
	_ = writer.WriteField("chunk_index", strconv.Itoa(chunkIndex))
	_ = writer.WriteField("total_chunks", strconv.Itoa(totalChunks))
	_ = writer.WriteField("chunk_size", strconv.Itoa(len(data)))
	_ = writer.WriteField("total_size", strconv.Itoa(len(data)*totalChunks))
	_ = writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/upload", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Set("username", "testuser")
	c.Set("is_admin", false)

	h.handlePostUpload(c)
	return w, nil
}

func TestChunkTracker_Lifecycle(t *testing.T) {
	h, _, cleanup := setupUploadTestEnv(t)
	defer cleanup()

	taskID := "task-lifecycle-1"

	// 1. Initially no tracker entry
	if HasChunkTracker(taskID) {
		t.Fatalf("expected no tracker initially for %s", taskID)
	}

	// 2. Upload chunk 0 of 2 -> Tracker entry is created and remains active
	w, err := sendUploadChunk(h, taskID, 0, 2, []byte("chunk0data"))
	if err != nil {
		t.Fatalf("failed to send chunk 0: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for chunk 0, got %d: %s", w.Code, w.Body.String())
	}

	if !HasChunkTracker(taskID) {
		t.Fatalf("expected tracker to exist for active upload %s", taskID)
	}

	// 3. Upload chunk 1 of 2 -> Upload completes, tracker is removed
	w, err = sendUploadChunk(h, taskID, 1, 2, []byte("chunk1data"))
	if err != nil {
		t.Fatalf("failed to send chunk 1: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for chunk 1, got %d: %s", w.Code, w.Body.String())
	}

	if HasChunkTracker(taskID) {
		t.Fatalf("expected tracker to be removed after successful upload completion for %s", taskID)
	}
}

func TestChunkTracker_CancelRemoves(t *testing.T) {
	h, _, cleanup := setupUploadTestEnv(t)
	defer cleanup()

	taskID := "task-cancel-1"

	// Upload chunk 0 of 3 -> Active
	w, err := sendUploadChunk(h, taskID, 0, 3, []byte("chunk0data"))
	if err != nil {
		t.Fatalf("failed to send chunk 0: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	if !HasChunkTracker(taskID) {
		t.Fatalf("expected tracker to exist for active upload %s", taskID)
	}

	// Delete/cancel via DeleteChunkTracker (simulating cancel or cleanup)
	DeleteChunkTracker(taskID)

	if HasChunkTracker(taskID) {
		t.Fatalf("expected tracker to be deleted after cancellation for %s", taskID)
	}
}

func TestChunkTracker_EarlyFailureDoesNotLeaveStaleEntry(t *testing.T) {
	_, cfg, cleanup := setupUploadTestEnv(t)
	defer cleanup()

	taskID := "task-fail-early-1"

	// Make TempDir a file instead of a directory so os.OpenFile fails inside handlePostUpload
	badTempDir := filepath.Join(cfg.TempDir, "not_a_dir")
	if err := os.WriteFile(badTempDir, []byte("blocking-file"), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}
	badHandler := &Handler{
		cfg: &config.Config{
			TempDir: badTempDir,
		},
	}

	w, err := sendUploadChunk(badHandler, taskID, 0, 2, []byte("chunk0data"))
	if err != nil {
		t.Fatalf("failed to send chunk: %v", err)
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for failed temp file open, got %d", w.Code)
	}

	// Stale entry must NOT be left behind in chunkTrackerSync
	if HasChunkTracker(taskID) {
		t.Fatalf("expected no tracker left behind for early upload failure on %s", taskID)
	}
}

func TestChunkTracker_ConcurrentUploadsIndependent(t *testing.T) {
	h, _, cleanup := setupUploadTestEnv(t)
	defer cleanup()

	const numTasks = 10
	var wg sync.WaitGroup

	// Start numTasks concurrently with chunk 0 of 2
	for i := 0; i < numTasks; i++ {
		taskID := fmt.Sprintf("task-concurrent-%d", i)
		wg.Add(1)
		go func(tID string) {
			defer wg.Done()
			w, err := sendUploadChunk(h, tID, 0, 2, []byte("chunk0"))
			if err != nil || w.Code != http.StatusOK {
				t.Errorf("chunk 0 failed for %s", tID)
			}
		}(taskID)
	}
	wg.Wait()

	// All numTasks should have active trackers
	for i := 0; i < numTasks; i++ {
		taskID := fmt.Sprintf("task-concurrent-%d", i)
		if !HasChunkTracker(taskID) {
			t.Fatalf("expected tracker to exist for active task %s", taskID)
		}
	}

	// Complete first half of the tasks
	for i := 0; i < numTasks/2; i++ {
		taskID := fmt.Sprintf("task-concurrent-%d", i)
		w, err := sendUploadChunk(h, taskID, 1, 2, []byte("chunk1"))
		if err != nil || w.Code != http.StatusOK {
			t.Fatalf("chunk 1 failed for %s", taskID)
		}
		// First half must be deleted
		if HasChunkTracker(taskID) {
			t.Fatalf("expected tracker for completed task %s to be deleted", taskID)
		}
	}

	// Second half must still be active and not accidentally deleted!
	for i := numTasks / 2; i < numTasks; i++ {
		taskID := fmt.Sprintf("task-concurrent-%d", i)
		if !HasChunkTracker(taskID) {
			t.Fatalf("tracker for active task %s was prematurely deleted by other tasks", taskID)
		}
	}

	// Complete second half
	for i := numTasks / 2; i < numTasks; i++ {
		taskID := fmt.Sprintf("task-concurrent-%d", i)
		w, err := sendUploadChunk(h, taskID, 1, 2, []byte("chunk1"))
		if err != nil || w.Code != http.StatusOK {
			t.Fatalf("chunk 1 failed for %s", taskID)
		}
		if HasChunkTracker(taskID) {
			t.Fatalf("expected tracker for completed task %s to be deleted", taskID)
		}
	}
}
