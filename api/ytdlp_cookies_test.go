package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"telecloud/config"
	"telecloud/tgclient"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestConvertJSONToNetscapeCookies(t *testing.T) {
	jsonSample := `[
		{
			"domain": ".youtube.com",
			"expirationDate": 1790000000.5,
			"hostOnly": false,
			"httpOnly": true,
			"name": "LOGIN_INFO",
			"path": "/",
			"secure": true,
			"value": "sample_token_123"
		},
		{
			"domain": "youtube.com",
			"hostOnly": true,
			"httpOnly": false,
			"name": "VISITOR_INFO1_LIVE",
			"path": "/test",
			"secure": false,
			"value": "sample_visitor_456"
		}
	]`

	out, err := convertJSONToNetscapeCookies([]byte(jsonSample))
	if err != nil {
		t.Fatalf("convertJSONToNetscapeCookies failed: %v", err)
	}

	outStr := string(out)
	if !strings.Contains(outStr, "# Netscape HTTP Cookie File") {
		t.Errorf("Expected Netscape header in output, got: %s", outStr)
	}

	if !strings.Contains(outStr, "#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t1790000000\tLOGIN_INFO\tsample_token_123") {
		t.Errorf("Expected LOGIN_INFO netscape line in output, got: %s", outStr)
	}

	if !strings.Contains(outStr, "youtube.com\tFALSE\t/test\tFALSE\t") {
		t.Errorf("Expected VISITOR_INFO1_LIVE netscape line in output, got: %s", outStr)
	}
}

func TestGetActiveCookieFile_HierarchyAndInheritance(t *testing.T) {
	tempCookiesDir := t.TempDir()
	cfg := &config.Config{
		CookiesDir: tempCookiesDir,
	}

	// Case 1: No cookies exist
	if active := tgclient.GetActiveCookieFile(cfg, "mobile_user"); active != "" {
		t.Errorf("Expected empty active cookie file, got: %s", active)
	}

	// Case 2: Global cookies.txt uploaded via web browser exists
	globalCookiePath := filepath.Join(tempCookiesDir, "cookies.txt")
	if err := os.WriteFile(globalCookiePath, []byte("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t1800000000\tSID\tvalue1\n"), 0600); err != nil {
		t.Fatalf("Failed to write global cookies.txt: %v", err)
	}

	// Mobile app user should automatically inherit global cookies.txt!
	active := tgclient.GetActiveCookieFile(cfg, "mobile_user")
	if active != globalCookiePath {
		t.Errorf("Expected mobile user to inherit %s, got: %s", globalCookiePath, active)
	}

	// Case 3: User-specific cookie takes precedence when present
	userCookiePath := filepath.Join(tempCookiesDir, "user_mobile_user.txt")
	if err := os.WriteFile(userCookiePath, []byte("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t1800000000\tSID\tuser_val\n"), 0600); err != nil {
		t.Fatalf("Failed to write user cookie: %v", err)
	}

	activeUser := tgclient.GetActiveCookieFile(cfg, "mobile_user")
	if activeUser != userCookiePath {
		t.Errorf("Expected specific user cookie %s to take precedence, got: %s", userCookiePath, activeUser)
	}
}

func TestCookieUploadAndStatusEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempCookiesDir := t.TempDir()

	h := &Handler{
		cfg: &config.Config{
			CookiesDir: tempCookiesDir,
		},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("username", "admin")
		c.Next()
	})
	r.GET("/api/ytdlp/cookies/status", h.handleGetYTDLPCookiesStatus)
	r.POST("/api/ytdlp/cookies", h.handlePostYTDLPCookies)
	r.DELETE("/api/ytdlp/cookies", h.handleDeleteYTDLPCookies)

	// Step 1: Status initially false
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/ytdlp/cookies/status", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}
	var res map[string]bool
	json.Unmarshal(w.Body.Bytes(), &res)
	if res["has_cookie"] {
		t.Errorf("Expected has_cookie to be false initially")
	}

	// Step 2: Upload JSON cookie file
	jsonPayload := `[{"domain": ".youtube.com", "name": "TEST", "value": "VAL", "path": "/", "secure": true}]`
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("cookie_file", "cookies.json")
	part.Write([]byte(jsonPayload))
	writer.Close()

	wUpload := httptest.NewRecorder()
	reqUpload, _ := http.NewRequest("POST", "/api/ytdlp/cookies", &body)
	reqUpload.Header.Set("Content-Type", writer.FormDataContentType())
	r.ServeHTTP(wUpload, reqUpload)
	if wUpload.Code != http.StatusOK {
		t.Fatalf("Upload failed with status %d: %s", wUpload.Code, wUpload.Body.String())
	}

	// Verify both user_admin.txt and cookies.txt exist and are in Netscape format
	adminPath := filepath.Join(tempCookiesDir, "user_admin.txt")
	globalPath := filepath.Join(tempCookiesDir, "cookies.txt")

	adminData, err := os.ReadFile(adminPath)
	if err != nil || !strings.Contains(string(adminData), "# Netscape HTTP Cookie File") {
		t.Fatalf("user_admin.txt was not converted to Netscape format properly")
	}

	globalData, err := os.ReadFile(globalPath)
	if err != nil || !strings.Contains(string(globalData), "# Netscape HTTP Cookie File") {
		t.Fatalf("cookies.txt was not created in Netscape format properly")
	}

	// Step 3: Status now true
	wStatus2 := httptest.NewRecorder()
	reqStatus2, _ := http.NewRequest("GET", "/api/ytdlp/cookies/status", nil)
	r.ServeHTTP(wStatus2, reqStatus2)
	json.Unmarshal(wStatus2.Body.Bytes(), &res)
	if !res["has_cookie"] {
		t.Errorf("Expected has_cookie to be true after upload")
	}

	// Step 4: Delete cookies
	wDel := httptest.NewRecorder()
	reqDel, _ := http.NewRequest("DELETE", "/api/ytdlp/cookies", nil)
	r.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("Delete failed with status %d", wDel.Code)
	}

	// Verify status is false again
	wStatus3 := httptest.NewRecorder()
	reqStatus3, _ := http.NewRequest("GET", "/api/ytdlp/cookies/status", nil)
	r.ServeHTTP(wStatus3, reqStatus3)
	json.Unmarshal(wStatus3.Body.Bytes(), &res)
	if res["has_cookie"] {
		t.Errorf("Expected has_cookie to be false after deletion")
	}
}
