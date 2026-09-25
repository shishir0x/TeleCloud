package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"telecloud/config"
	"telecloud/database"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandleGetFiles_DatabaseErrorNotExposed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_files_err.db")

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

	// Drop files table to simulate an internal database failure
	_, err = database.DB.Exec("DROP TABLE files")
	if err != nil {
		t.Fatalf("failed to drop table for test: %v", err)
	}

	// 1. Test handleGetFiles
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/files?path=/", nil)
	c.Set("username", "testuser")
	c.Set("is_admin", false)

	h.handleGetFiles(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for handleGetFiles with DB error, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v, body: %s", err, w.Body.String())
	}

	if resp["error"] != "internal_error" {
		t.Errorf("expected error 'internal_error', got '%s'", resp["error"])
	}

	// Verify no SQL / driver details leaked in response
	rawBody := strings.ToLower(w.Body.String())
	forbiddenKeywords := []string{"select", "no such table", "sqlite", "syntax error", "table files"}
	for _, kw := range forbiddenKeywords {
		if strings.Contains(rawBody, kw) {
			t.Errorf("leaked internal database detail '%s' in response body: %s", kw, w.Body.String())
		}
	}

	// 2. Test handlePostFolders
	wFolder := httptest.NewRecorder()
	cFolder, _ := gin.CreateTestContext(wFolder)
	form := url.Values{}
	form.Set("name", "NewFolder")
	form.Set("path", "/")
	cFolder.Request, _ = http.NewRequest("POST", "/api/folders", strings.NewReader(form.Encode()))
	cFolder.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cFolder.Set("username", "testuser")
	cFolder.Set("is_admin", false)

	h.handlePostFolders(cFolder)

	if wFolder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for handlePostFolders with DB error, got %d", wFolder.Code)
	}

	var respFolder map[string]string
	if err := json.Unmarshal(wFolder.Body.Bytes(), &respFolder); err != nil {
		t.Fatalf("failed to parse json response: %v, body: %s", err, wFolder.Body.String())
	}

	if respFolder["error"] != "internal_error" {
		t.Errorf("expected error 'internal_error', got '%s'", respFolder["error"])
	}

	// 3. Test handleGetTrashFiles
	wTrash := httptest.NewRecorder()
	cTrash, _ := gin.CreateTestContext(wTrash)
	cTrash.Request, _ = http.NewRequest("GET", "/api/files/trash", nil)
	cTrash.Set("username", "testuser")
	cTrash.Set("is_admin", false)

	h.handleGetTrashFiles(cTrash)

	if wTrash.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for handleGetTrashFiles with DB error, got %d", wTrash.Code)
	}

	var respTrash map[string]string
	if err := json.Unmarshal(wTrash.Body.Bytes(), &respTrash); err != nil {
		t.Fatalf("failed to parse json response: %v, body: %s", err, wTrash.Body.String())
	}

	if respTrash["error"] != "internal_error" {
		t.Errorf("expected error 'internal_error', got '%s'", respTrash["error"])
	}
}
