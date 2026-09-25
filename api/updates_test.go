package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"telecloud/config"
	"telecloud/database"
)

func setupUpdatesTestDB(t *testing.T) *Handler {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_updates.db")

	err := database.InitDB("sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			TempDir: tempDir,
			Version: "v3.8.8",
		},
		updateChecker: NewUpdateChecker(24 * time.Hour),
	}
	return h
}

func TestUpdateChecker_DisabledByDefault(t *testing.T) {
	h := setupUpdatesTestDB(t)
	defer database.CloseDB()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/updates", nil)
	c.Set("username", "testuser")
	c.Set("is_admin", false)
	h.handleGetUpdates(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Enabled         bool   `json:"enabled"`
		UpdateAvailable bool   `json:"update_available"`
		CurrentVersion  string `json:"current_version"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Enabled != false || resp.UpdateAvailable != false || resp.CurrentVersion != "v3.8.8" {
		t.Fatalf("expected disabled by default, got: %+v", resp)
	}
}

func TestUpdateChecker_AdminTogglePermissions(t *testing.T) {
	h := setupUpdatesTestDB(t)
	defer database.CloseDB()

	// 1. Non-admin cannot toggle updates setting
	wNonAdmin := httptest.NewRecorder()
	cNonAdmin, _ := gin.CreateTestContext(wNonAdmin)
	cNonAdmin.Request, _ = http.NewRequest("POST", "/api/settings/updates", nil)
	cNonAdmin.Set("username", "regular_user")
	cNonAdmin.Set("is_admin", false)
	h.handlePostUpdateSettings(cNonAdmin)

	if wNonAdmin.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin, got %d", wNonAdmin.Code)
	}

	// 2. Admin toggles updates setting on
	wAdmin := httptest.NewRecorder()
	cAdmin, _ := gin.CreateTestContext(wAdmin)
	cAdmin.Request, _ = http.NewRequest("POST", "/api/settings/updates", nil)
	cAdmin.Request.PostForm = map[string][]string{"enabled": {"true"}}
	cAdmin.Set("username", "admin")
	cAdmin.Set("is_admin", true)
	h.handlePostUpdateSettings(cAdmin)

	if wAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin toggle, got %d", wAdmin.Code)
	}

	if database.GetSetting("check_updates_enabled") != "true" {
		t.Fatalf("expected setting check_updates_enabled to be true in DB")
	}
}

func TestUpdateChecker_CacheBehavior24Hours(t *testing.T) {
	h := setupUpdatesTestDB(t)
	defer database.CloseDB()

	database.SetSetting("check_updates_enabled", "true")

	var serverHits int32
	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&serverHits, 1)
		w.Header().Set("Content-Type", "application/json")
		releases := []map[string]interface{}{
			{
				"tag_name":     "v3.9.0",
				"name":         "Release 3.9.0",
				"body":         "Major performance enhancements",
				"html_url":     "https://github.com/shishir0x/TeleCloud/releases/tag/v3.9.0",
				"published_at": "2026-09-25T00:00:00Z",
			},
		}
		_ = json.NewEncoder(w).Encode(releases)
	}))
	defer mockGitHub.Close()

	// Inject custom HTTP client directing requests to mockGitHub
	h.updateChecker.SetHTTPClient(mockGitHub.Client())
	// Use custom transport to redirect any request to mock server
	mockClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			mockReq, err := http.NewRequest(req.Method, mockGitHub.URL, req.Body)
			if err != nil {
				return nil, err
			}
			return mockGitHub.Client().Do(mockReq)
		}),
	}
	h.updateChecker.SetHTTPClient(mockClient)

	// Call 1: should hit mock GitHub server
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request, _ = http.NewRequest("GET", "/api/updates", nil)
	c1.Set("username", "admin")
	c1.Set("is_admin", true)
	h.handleGetUpdates(c1)

	var resp1 UpdateCheckResponse
	_ = json.Unmarshal(w1.Body.Bytes(), &resp1)

	if !resp1.UpdateAvailable || resp1.LatestVersion != "v3.9.0" || resp1.Cached {
		t.Fatalf("call 1: unexpected resp1: %+v", resp1)
	}
	if atomic.LoadInt32(&serverHits) != 1 {
		t.Fatalf("expected 1 hit to GitHub server, got %d", serverHits)
	}

	// Call 2: repeated call within 24h -> must be cached, 0 new server hits!
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request, _ = http.NewRequest("GET", "/api/updates", nil)
	c2.Set("username", "admin")
	c2.Set("is_admin", true)
	h.handleGetUpdates(c2)

	var resp2 UpdateCheckResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)

	if !resp2.UpdateAvailable || !resp2.Cached {
		t.Fatalf("call 2: expected cached response, got %+v", resp2)
	}
	if atomic.LoadInt32(&serverHits) != 1 {
		t.Fatalf("server hits should still be 1 (served from cache), but was %d", serverHits)
	}

	// Call 3: forced check with ?force=true -> hits server again
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request, _ = http.NewRequest("GET", "/api/updates?force=true", nil)
	c3.Set("username", "admin")
	c3.Set("is_admin", true)
	h.handleGetUpdates(c3)

	var resp3 UpdateCheckResponse
	_ = json.Unmarshal(w3.Body.Bytes(), &resp3)

	if resp3.Cached {
		t.Fatalf("call 3: forced check should not be cached")
	}
	if atomic.LoadInt32(&serverHits) != 2 {
		t.Fatalf("server hits should be 2 after forced refresh, got %d", serverHits)
	}
}

func TestUpdateChecker_SemverComparison(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"v3.8.9", "v3.8.8", 1},
		{"v3.8.8", "v3.8.9", -1},
		{"v3.8.8", "v3.8.8", 0},
		{"v4.0.0", "v3.9.9", 1},
		{"v3.10.0", "v3.9.9", 1},
		{"v3.8.8-beta", "v3.8.8", 0},
		{"3.9.0", "v3.8.0", 1},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tt.v1, tt.v2), func(t *testing.T) {
			res := compareVersions(tt.v1, tt.v2)
			if res != tt.expected {
				t.Errorf("compareVersions(%s, %s) = %d; want %d", tt.v1, tt.v2, res, tt.expected)
			}
		})
	}
}

func TestUpdateChecker_OfflineAndErrorHandling(t *testing.T) {
	h := setupUpdatesTestDB(t)
	defer database.CloseDB()

	database.SetSetting("check_updates_enabled", "true")

	// Mock server that returns 500 error
	mockErrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "GitHub rate limited", http.StatusForbidden)
	}))
	defer mockErrorServer.Close()

	mockClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			mockReq, _ := http.NewRequest(req.Method, mockErrorServer.URL, req.Body)
			return mockErrorServer.Client().Do(mockReq)
		}),
	}
	h.updateChecker.SetHTTPClient(mockClient)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/updates", nil)
	c.Set("username", "testuser")
	c.Set("is_admin", false)
	h.handleGetUpdates(c)

	// Must return 200 OK with update_available=false and error populated, NEVER 500!
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK gracefully handling upstream failure, got %d", w.Code)
	}

	var resp UpdateCheckResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.UpdateAvailable {
		t.Errorf("expected update_available to be false on error")
	}
	if resp.Error == "" {
		t.Errorf("expected error message to be set on upstream failure")
	}
}

func TestUpdateChecker_CacheLatencyMeasurement(t *testing.T) {
	h := setupUpdatesTestDB(t)
	defer database.CloseDB()

	database.SetSetting("check_updates_enabled", "true")

	// Pre-populate cache
	h.updateChecker.cachedResp = &UpdateCheckResponse{
		Enabled:          true,
		UpdateAvailable:  false,
		CurrentVersion:   "v3.8.8",
		LatestVersion:    "v3.8.8",
		LatestReleaseURL: "https://github.com/shishir0x/TeleCloud/releases",
		CheckedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	h.updateChecker.lastChecked = time.Now()

	start := time.Now()
	runs := 100
	for i := 0; i < runs; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/updates", nil)
		c.Set("username", "testuser")
		c.Set("is_admin", false)
		h.handleGetUpdates(c)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	}
	avg := time.Since(start) / time.Duration(runs)
	t.Logf("[PERF-03 MEASUREMENT] Cached update check: %v per request (100 runs)", avg)
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
