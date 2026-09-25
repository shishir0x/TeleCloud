package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"telecloud/config"
	"telecloud/database"
)

func setupSearchFixtureDB(t *testing.T) *Handler {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "search_fixture.db")

	err := database.InitDB("sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			TempDir: tempDir,
		},
	}

	// Create test accounts
	_, _ = database.DB.Exec("INSERT INTO child_accounts (username, password_hash) VALUES ('user_alice', 'dummyhash1')")
	_, _ = database.DB.Exec("INSERT INTO child_accounts (username, password_hash) VALUES ('user_bob', 'dummyhash2')")

	// Alice's files
	aliceFiles := []struct {
		name     string
		path     string
		mime     string
		isFolder bool
	}{
		{"holiday_photo.jpg", "/user_alice", "image/jpeg", false},
		{"holiday_video.mp4", "/user_alice", "video/mp4", false},
		{"holiday_audio.mp3", "/user_alice", "audio/mpeg", false},
		{"Quarterly_Report.pdf", "/user_alice", "application/pdf", false},
		{"archive_data.tar.gz", "/user_alice", "application/gzip", false},
		{"holiday_album", "/user_alice", "", true},
		{"notes.txt", "/user_alice/documents", "text/plain", false},
		{"INVOICE_2026.PDF", "/user_alice", "application/pdf", false},
	}

	for i, f := range aliceFiles {
		msgID := i + 1
		isFolderInt := 0
		var msgIDPtr *int = &msgID
		if f.isFolder {
			isFolderInt = 1
			msgIDPtr = nil
		}
		_, err := database.DB.Exec(
			"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, owner) VALUES (?, ?, ?, 1024, ?, ?, 'user_alice')",
			msgIDPtr, f.name, f.path, f.mime, isFolderInt,
		)
		if err != nil {
			t.Fatalf("failed inserting alice file %s: %v", f.name, err)
		}
	}

	// Bob's files
	bobFiles := []struct {
		name     string
		path     string
		mime     string
		isFolder bool
	}{
		{"bob_portrait.png", "/user_bob", "image/png", false},
		{"holiday_in_rome.jpg", "/user_bob", "image/jpeg", false},
		{"bob_personal.txt", "/user_bob", "text/plain", false},
	}

	for i, f := range bobFiles {
		msgID := i + 50
		isFolderInt := 0
		var msgIDPtr *int = &msgID
		if f.isFolder {
			isFolderInt = 1
			msgIDPtr = nil
		}
		_, err := database.DB.Exec(
			"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, owner) VALUES (?, ?, ?, 2048, ?, ?, 'user_bob')",
			msgIDPtr, f.name, f.path, f.mime, isFolderInt,
		)
		if err != nil {
			t.Fatalf("failed inserting bob file %s: %v", f.name, err)
		}
	}

	return h
}

func TestSearch_ExactAndPartialMatching(t *testing.T) {
	h := setupSearchFixtureDB(t)
	defer database.CloseDB()

	// 1. Exact match
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/search?q=holiday_photo.jpg", nil)
	c.Set("username", "user_alice")
	c.Set("is_admin", false)
	h.handleSearch(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Files []database.File `json:"files"`
		Total int             `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 1 || len(resp.Files) != 1 || resp.Files[0].Filename != "holiday_photo.jpg" {
		t.Fatalf("expected exact match holiday_photo.jpg, got %+v", resp)
	}

	// 2. Partial match: "holiday" (Alice has photo, video, audio, album folder = 4 items)
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request, _ = http.NewRequest("GET", "/api/search?q=holiday", nil)
	c2.Set("username", "user_alice")
	c2.Set("is_admin", false)
	h.handleSearch(c2)

	var resp2 struct {
		Files []database.File `json:"files"`
		Total int             `json:"total"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Total != 4 {
		t.Errorf("expected 4 holiday items for alice, got %d", resp2.Total)
	}
}

func TestSearch_CaseInsensitiveMatching(t *testing.T) {
	h := setupSearchFixtureDB(t)
	defer database.CloseDB()

	// Uppercase search "REPORT" should match "Quarterly_Report.pdf"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/search?q=REPORT", nil)
	c.Set("username", "user_alice")
	c.Set("is_admin", false)
	h.handleSearch(c)

	var resp struct {
		Files []database.File `json:"files"`
		Total int             `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 1 || resp.Files[0].Filename != "Quarterly_Report.pdf" {
		t.Errorf("expected case-insensitive match for Quarterly_Report.pdf, got %+v", resp)
	}

	// Lowercase search "invoice" should match "INVOICE_2026.PDF"
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request, _ = http.NewRequest("GET", "/api/search?q=invoice", nil)
	c2.Set("username", "user_alice")
	c2.Set("is_admin", false)
	h.handleSearch(c2)

	var resp2 struct {
		Files []database.File `json:"files"`
		Total int             `json:"total"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Total != 1 || resp2.Files[0].Filename != "INVOICE_2026.PDF" {
		t.Errorf("expected case-insensitive match for INVOICE_2026.PDF, got %+v", resp2)
	}
}

func TestSearch_TypeFilteringCategories(t *testing.T) {
	h := setupSearchFixtureDB(t)
	defer database.CloseDB()

	types := []struct {
		typeParam    string
		expectedName string
	}{
		{"image", "holiday_photo.jpg"},
		{"video", "holiday_video.mp4"},
		{"audio", "holiday_audio.mp3"},
		{"document", "Quarterly_Report.pdf"},
		{"archive", "archive_data.tar.gz"},
		{"folder", "holiday_album"},
	}

	for _, tt := range types {
		t.Run("Type_"+tt.typeParam, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", fmt.Sprintf("/api/search?type=%s", tt.typeParam), nil)
			c.Set("username", "user_alice")
			c.Set("is_admin", false)
			h.handleSearch(c)

			var resp struct {
				Files []database.File `json:"files"`
				Total int             `json:"total"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			found := false
			for _, f := range resp.Files {
				if f.Filename == tt.expectedName {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected file %s in results for type %s, got: %+v", tt.expectedName, tt.typeParam, resp.Files)
			}
		})
	}
}

func TestSearch_EmptyAndNonexistentQueries(t *testing.T) {
	h := setupSearchFixtureDB(t)
	defer database.CloseDB()

	// Empty query
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/search?q=", nil)
	c.Set("username", "user_alice")
	c.Set("is_admin", false)
	h.handleSearch(c)

	var resp struct {
		Files []database.File `json:"files"`
		Total int             `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 0 || len(resp.Files) != 0 {
		t.Errorf("expected empty results for empty query, got %+v", resp)
	}

	// Nonexistent file
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request, _ = http.NewRequest("GET", "/api/search?q=this_file_does_not_exist_at_all_999", nil)
	c2.Set("username", "user_alice")
	c2.Set("is_admin", false)
	h.handleSearch(c2)

	var resp2 struct {
		Files []database.File `json:"files"`
		Total int             `json:"total"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Total != 0 || len(resp2.Files) != 0 {
		t.Errorf("expected 0 results for nonexistent file, got %d", resp2.Total)
	}
}

func TestSearch_AuthorizationScopeIsolation(t *testing.T) {
	h := setupSearchFixtureDB(t)
	defer database.CloseDB()

	// Alice searches for "holiday" -> must only see her 4 items, NOT Bob's holiday_in_rome.jpg
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/search?q=holiday", nil)
	c.Set("username", "user_alice")
	c.Set("is_admin", false)
	h.handleSearch(c)

	var respAlice struct {
		Files []database.File `json:"files"`
		Total int             `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &respAlice)
	if respAlice.Total != 4 {
		t.Fatalf("Alice should only see her 4 holiday items, got %d", respAlice.Total)
	}
	for _, f := range respAlice.Files {
		if f.Filename == "holiday_in_rome.jpg" {
			t.Fatalf("SECURITY VIOLATION: Alice retrieved Bob's holiday_in_rome.jpg!")
		}
	}

	// Alice tries to search for Bob's file: "bob_personal"
	wBob := httptest.NewRecorder()
	cBob, _ := gin.CreateTestContext(wBob)
	cBob.Request, _ = http.NewRequest("GET", "/api/search?q=bob_personal", nil)
	cBob.Set("username", "user_alice")
	cBob.Set("is_admin", false)
	h.handleSearch(cBob)

	var respBob struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(wBob.Body.Bytes(), &respBob)
	if respBob.Total != 0 {
		t.Fatalf("SECURITY VIOLATION: Alice was able to find Bob's private file!")
	}

	// Admin searches for "holiday" -> sees all 5 holiday items (Alice's 4 + Bob's 1)
	wAdmin := httptest.NewRecorder()
	cAdmin, _ := gin.CreateTestContext(wAdmin)
	cAdmin.Request, _ = http.NewRequest("GET", "/api/search?q=holiday", nil)
	cAdmin.Set("username", "admin")
	cAdmin.Set("is_admin", true)
	h.handleSearch(cAdmin)

	var respAdmin struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(wAdmin.Body.Bytes(), &respAdmin)
	if respAdmin.Total != 5 {
		t.Errorf("Admin should see 5 holiday items, got %d", respAdmin.Total)
	}
}

func TestSearch_PaginationAndLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "search_pagination.db")

	err := database.InitDB("sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.CloseDB()

	h := &Handler{
		cfg: &config.Config{
			TempDir: tempDir,
		},
	}

	_, _ = database.DB.Exec("INSERT INTO child_accounts (username, password_hash) VALUES ('testuser', 'dummyhash')")
	for i := 1; i <= 75; i++ {
		msgID := i
		_, _ = database.DB.Exec(
			"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, owner) VALUES (?, ?, '/testuser', 1024, 'text/plain', 0, 'testuser')",
			&msgID, fmt.Sprintf("dataset_item_%03d.txt", i),
		)
	}

	// Page 1: limit=30, offset=0
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request, _ = http.NewRequest("GET", "/api/search?q=dataset&limit=30&offset=0", nil)
	c1.Set("username", "testuser")
	c1.Set("is_admin", false)
	h.handleSearch(c1)

	var p1 struct {
		Files   []database.File `json:"files"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
		Limit   int             `json:"limit"`
		Offset  int             `json:"offset"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &p1)
	if p1.Total != 75 || len(p1.Files) != 30 || !p1.HasMore || p1.Offset != 0 {
		t.Fatalf("unexpected p1: %+v", p1)
	}

	// Page 2: limit=30, offset=30
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request, _ = http.NewRequest("GET", "/api/search?q=dataset&limit=30&offset=30", nil)
	c2.Set("username", "testuser")
	c2.Set("is_admin", false)
	h.handleSearch(c2)

	var p2 struct {
		Files   []database.File `json:"files"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
		Offset  int             `json:"offset"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &p2)
	if p2.Total != 75 || len(p2.Files) != 30 || !p2.HasMore || p2.Offset != 30 {
		t.Fatalf("unexpected p2: %+v", p2)
	}

	// Page 3: limit=30, offset=60 (last 15 items)
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request, _ = http.NewRequest("GET", "/api/search?q=dataset&limit=30&offset=60", nil)
	c3.Set("username", "testuser")
	c3.Set("is_admin", false)
	h.handleSearch(c3)

	var p3 struct {
		Files   []database.File `json:"files"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
	}
	_ = json.Unmarshal(w3.Body.Bytes(), &p3)
	if p3.Total != 75 || len(p3.Files) != 15 || p3.HasMore {
		t.Fatalf("unexpected p3: %+v", p3)
	}
}

func TestSearch_ConcurrentAccessAndLatency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "search_perf.db")

	err := database.InitDB("sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.CloseDB()

	h := &Handler{
		cfg: &config.Config{
			TempDir: tempDir,
		},
	}

	_, _ = database.DB.Exec("INSERT INTO child_accounts (username, password_hash) VALUES ('perfuser', 'dummyhash')")
	for i := 1; i <= 200; i++ {
		msgID := i
		_, _ = database.DB.Exec(
			"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, owner) VALUES (?, ?, '/perfuser', 1024, 'text/plain', 0, 'perfuser')",
			&msgID, fmt.Sprintf("sample_data_file_%04d.txt", i),
		)
	}

	// Measure 50 search requests
	start := time.Now()
	runs := 50
	for i := 0; i < runs; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", fmt.Sprintf("/api/search?q=sample_data_file_%04d", (i*3)+1), nil)
		c.Set("username", "perfuser")
		c.Set("is_admin", false)
		h.handleSearch(c)
		if w.Code != http.StatusOK {
			t.Fatalf("search request failed with status %d", w.Code)
		}
	}
	elapsed := time.Since(start)
	avg := elapsed / time.Duration(runs)
	t.Logf("[PERF-02 MEASUREMENT] 200 files in DB, 50 queries: %v average latency per search query", avg)

	// Concurrency test: 10 parallel searchers
	var wg sync.WaitGroup
	errCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", fmt.Sprintf("/api/search?q=sample_data&limit=10&offset=%d", workerID*5), nil)
			c.Set("username", "perfuser")
			c.Set("is_admin", false)
			h.handleSearch(c)
			if w.Code != http.StatusOK {
				errCh <- fmt.Errorf("worker %d got status %d", workerID, w.Code)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent search error: %v", err)
	}
}
