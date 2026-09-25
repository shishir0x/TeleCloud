package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"telecloud/config"
	"telecloud/database"
)

func setupPerfTestDB(t *testing.T, numFiles int) (*Handler, string) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_perf.db")
	thumbsDir := filepath.Join(tempDir, "thumbs")
	_ = os.MkdirAll(thumbsDir, 0755)

	err := database.InitDB("sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			TempDir:   tempDir,
			ThumbsDir: thumbsDir,
		},
	}

	// Insert child account for testuser
	_, _ = database.DB.Exec("INSERT INTO child_accounts (username, password_hash) VALUES ('testuser', 'hash')")

	// Insert files into /testuser
	for i := 1; i <= numFiles; i++ {
		filename := fmt.Sprintf("file_%04d.txt", i)
		var thumbPath *string
		hasThumb := false
		if i%2 == 0 {
			tp := filepath.Join(thumbsDir, fmt.Sprintf("thumb_%d.jpg", i))
			_ = os.WriteFile(tp, []byte("fake_thumb_data"), 0644)
			thumbPath = &tp
			hasThumb = true
		}
		msgID := i
		_, err := database.DB.Exec(
			"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, thumb_path, owner, has_thumb) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)",
			msgID, filename, "/testuser", 1024, "text/plain", thumbPath, "testuser", hasThumb,
		)
		if err != nil {
			t.Fatalf("failed to insert file: %v", err)
		}
	}

	return h, tempDir
}

func TestDirectoryListing_Measurement(t *testing.T) {
	counts := []int{10, 100, 500}
	for _, numFiles := range counts {
		h, _ := setupPerfTestDB(t, numFiles)

		start := time.Now()
		runs := 50
		for r := 0; r < runs; r++ {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/api/files?path=/", nil)
			c.Set("username", "testuser")
			c.Set("is_admin", false)

			h.handleGetFiles(c)

			if r == 0 {
				var resp struct {
					Files []database.File `json:"files"`
					Total int             `json:"total"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &resp)
				if len(resp.Files) != numFiles {
					t.Fatalf("expected %d files, got %d", numFiles, len(resp.Files))
				}
				if resp.Total != numFiles {
					t.Fatalf("expected total %d, got %d", numFiles, resp.Total)
				}
			}
		}
		elapsed := time.Since(start)
		avgPerReq := elapsed / time.Duration(runs)
		t.Logf("[OPTIMIZED] %d files (full list): %v per request (%d runs)", numFiles, avgPerReq, runs)

		// Also test paginated (limit=50) for large folder
		if numFiles == 500 {
			startPaginated := time.Now()
			for r := 0; r < runs; r++ {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request, _ = http.NewRequest("GET", "/api/files?path=/&limit=50&offset=0", nil)
				c.Set("username", "testuser")
				c.Set("is_admin", false)

				h.handleGetFiles(c)

				if r == 0 {
					var resp struct {
						Files   []database.File `json:"files"`
						Total   int             `json:"total"`
						HasMore bool            `json:"has_more"`
						Limit   int             `json:"limit"`
						Offset  int             `json:"offset"`
					}
					_ = json.Unmarshal(w.Body.Bytes(), &resp)
					if len(resp.Files) != 50 {
						t.Fatalf("expected 50 paginated files, got %d", len(resp.Files))
					}
					if resp.Total != 500 {
						t.Fatalf("expected total 500, got %d", resp.Total)
					}
					if !resp.HasMore {
						t.Fatalf("expected has_more=true")
					}
				}
			}
			elapsedPaginated := time.Since(startPaginated)
			t.Logf("[OPTIMIZED] 500 files with limit=50 offset=0: %v per request (%d runs)", elapsedPaginated/time.Duration(runs), runs)
		}

		_ = database.CloseDB()
	}
}

func TestDirectoryListing_Pagination(t *testing.T) {
	h, _ := setupPerfTestDB(t, 125)
	defer database.CloseDB()

	// 1. First page: limit=50, offset=0
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/files?path=/&limit=50&offset=0", nil)
	c.Set("username", "testuser")
	c.Set("is_admin", false)
	h.handleGetFiles(c)

	var p1 struct {
		Files   []database.File `json:"files"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
		Limit   int             `json:"limit"`
		Offset  int             `json:"offset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p1); err != nil {
		t.Fatalf("p1 json error: %v", err)
	}
	if p1.Total != 125 || len(p1.Files) != 50 || !p1.HasMore || p1.Limit != 50 || p1.Offset != 0 {
		t.Fatalf("unexpected p1: %+v", p1)
	}

	// 2. Second page: limit=50, offset=50
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request, _ = http.NewRequest("GET", "/api/files?path=/&limit=50&offset=50", nil)
	c2.Set("username", "testuser")
	c2.Set("is_admin", false)
	h.handleGetFiles(c2)

	var p2 struct {
		Files   []database.File `json:"files"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
		Limit   int             `json:"limit"`
		Offset  int             `json:"offset"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &p2); err != nil {
		t.Fatalf("p2 json error: %v", err)
	}
	if p2.Total != 125 || len(p2.Files) != 50 || !p2.HasMore || p2.Limit != 50 || p2.Offset != 50 {
		t.Fatalf("unexpected p2: %+v", p2)
	}

	// Verify no duplicates between p1 and p2
	p1IDs := make(map[int]bool)
	for _, f := range p1.Files {
		p1IDs[f.ID] = true
	}
	for _, f := range p2.Files {
		if p1IDs[f.ID] {
			t.Errorf("duplicate file ID %d between page 1 and page 2", f.ID)
		}
	}

	// 3. Third page: limit=50, offset=100 (only 25 remaining)
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request, _ = http.NewRequest("GET", "/api/files?path=/&limit=50&offset=100", nil)
	c3.Set("username", "testuser")
	c3.Set("is_admin", false)
	h.handleGetFiles(c3)

	var p3 struct {
		Files   []database.File `json:"files"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
		Limit   int             `json:"limit"`
		Offset  int             `json:"offset"`
	}
	_ = json.Unmarshal(w3.Body.Bytes(), &p3)
	if p3.Total != 125 || len(p3.Files) != 25 || p3.HasMore {
		t.Fatalf("unexpected p3: %+v", p3)
	}

	// 4. Page past end: offset=200
	w4 := httptest.NewRecorder()
	c4, _ := gin.CreateTestContext(w4)
	c4.Request, _ = http.NewRequest("GET", "/api/files?path=/&limit=50&offset=200", nil)
	c4.Set("username", "testuser")
	c4.Set("is_admin", false)
	h.handleGetFiles(c4)

	var p4 struct {
		Files   []database.File `json:"files"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
	}
	_ = json.Unmarshal(w4.Body.Bytes(), &p4)
	if p4.Total != 125 || len(p4.Files) != 0 || p4.HasMore {
		t.Fatalf("unexpected p4 past end: %+v", p4)
	}

	// 5. Max limit clamp: limit=1000 clamped to 500
	w5 := httptest.NewRecorder()
	c5, _ := gin.CreateTestContext(w5)
	c5.Request, _ = http.NewRequest("GET", "/api/files?path=/&limit=1000&offset=0", nil)
	c5.Set("username", "testuser")
	c5.Set("is_admin", false)
	h.handleGetFiles(c5)

	var p5 struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal(w5.Body.Bytes(), &p5)
	if p5.Limit != 500 {
		t.Errorf("expected limit to clamp to 500, got %d", p5.Limit)
	}
}

func TestDirectoryListing_ThumbnailsAndLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_thumb.db")
	thumbsDir := filepath.Join(tempDir, "thumbs")
	_ = os.MkdirAll(thumbsDir, 0755)

	err := database.InitDB("sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.CloseDB()

	h := &Handler{
		cfg: &config.Config{
			TempDir:   tempDir,
			ThumbsDir: thumbsDir,
		},
	}

	// 1. Insert file with real thumbnail
	tp := filepath.Join(thumbsDir, "real_thumb.jpg")
	_ = os.WriteFile(tp, []byte("thumb_bytes"), 0644)
	res, err := database.DB.Exec(
		"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, thumb_path, owner, has_thumb) VALUES (1, 'with_thumb.txt', '/user1', 100, 'text/plain', 0, ?, 'user1', 1)",
		tp,
	)
	if err != nil {
		t.Fatalf("insert err: %v", err)
	}
	id1, _ := res.LastInsertId()

	// 2. Insert file without thumbnail
	res2, _ := database.DB.Exec(
		"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, thumb_path, owner, has_thumb) VALUES (2, 'no_thumb.txt', '/user1', 100, 'text/plain', 0, NULL, 'user1', 0)",
	)
	id2, _ := res2.LastInsertId()

	// 3. Insert media file without existing thumb (supports on-demand generation)
	res3, _ := database.DB.Exec(
		"INSERT INTO files (message_id, filename, path, size, mime_type, is_folder, thumb_path, owner, has_thumb) VALUES (3, 'photo.jpg', '/user1', 100, 'image/jpeg', 0, NULL, 'user1', 0)",
	)
	id3, _ := res3.LastInsertId()

	// Query /api/files
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/files?path=/", nil)
	c.Set("username", "user1")
	c.Set("is_admin", false)
	h.handleGetFiles(c)

	var resp struct {
		Files []database.File `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json parse error: %v", err)
	}

	fileMap := make(map[int]database.File)
	for _, f := range resp.Files {
		fileMap[f.ID] = f
	}

	if !fileMap[int(id1)].HasThumb {
		t.Errorf("file with_thumb should have HasThumb=true")
	}
	if fileMap[int(id2)].HasThumb {
		t.Errorf("file no_thumb should have HasThumb=false")
	}
	if !fileMap[int(id3)].HasThumb {
		t.Errorf("media file photo.jpg should have HasThumb=true for on-demand generation")
	}

	// 4. Test Rename maintains thumbnail flag
	form := url.Values{}
	form.Set("new_name", "renamed_thumb.txt")
	wRename := httptest.NewRecorder()
	cRename, _ := gin.CreateTestContext(wRename)
	cRename.Request, _ = http.NewRequest("PUT", fmt.Sprintf("/api/files/%d/rename", id1), nil)
	cRename.Params = []gin.Param{{Key: "id", Value: fmt.Sprintf("%d", id1)}}
	cRename.Request.PostForm = form
	cRename.Set("username", "user1")
	cRename.Set("is_admin", false)
	h.handleRenameFile(cRename)

	// Verify database still has has_thumb = 1
	var hasThumbAfterRename bool
	database.RODB.Get(&hasThumbAfterRename, "SELECT has_thumb FROM files WHERE id = ?", id1)
	if !hasThumbAfterRename {
		t.Errorf("renamed file should still have has_thumb=true")
	}
}
