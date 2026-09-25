package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"telecloud/config"
	"telecloud/database"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func setupPasswordTestEnv(t *testing.T) *Handler {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbFile := tempDir + "/test_password.db"
	if err := database.InitDB("sqlite", dbFile, ""); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.CloseDB()
	})

	hash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	_ = database.SetSetting("admin_username", "admin")
	_ = database.SetSetting("admin_password_hash", string(hash))

	cfg := &config.Config{
		ListenAddr: "127.0.0.1",
		Port:       "8091",
	}

	return &Handler{
		cfg: cfg,
	}
}

func TestPasswordMinLength_ChangePassword(t *testing.T) {
	h := setupPasswordTestEnv(t)

	r := gin.New()
	r.POST("/api/settings/password", func(c *gin.Context) {
		// Mock authenticated admin session
		c.Set("username", "admin")
		c.Set("is_admin", true)
		c.Set("is_child", false)
		h.handlePostPassword(c)
	})

	tests := []struct {
		name         string
		oldPass      string
		newPass      string
		expectedCode int
		expectedErr  string
	}{
		{
			name:         "Single character rejected",
			oldPass:      "oldpassword123",
			newPass:      "1",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "7 characters rejected",
			oldPass:      "oldpassword123",
			newPass:      "1234567",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "Unicode 7 runes (14 bytes) rejected",
			oldPass:      "oldpassword123",
			newPass:      "世界你好123", // 7 runes
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "8 characters accepted",
			oldPass:      "oldpassword123",
			newPass:      "12345678",
			expectedCode: http.StatusOK,
			expectedErr:  "",
		},
		{
			name:         "Unicode 8 runes accepted",
			oldPass:      "12345678",
			newPass:      "世界你好1234", // 8 runes
			expectedCode: http.StatusOK,
			expectedErr:  "",
		},
		{
			name:         "Long password accepted",
			oldPass:      "世界你好1234",
			newPass:      "correcthorsebatterystaple2026!",
			expectedCode: http.StatusOK,
			expectedErr:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("old_password", tc.oldPass)
			form.Set("new_password", tc.newPass)

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/settings/password", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			r.ServeHTTP(w, req)

			if w.Code != tc.expectedCode {
				t.Fatalf("expected status %d, got %d, body: %s", tc.expectedCode, w.Code, w.Body.String())
			}

			if tc.expectedErr != "" {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to parse JSON response: %v", err)
				}
				if resp["error"] != tc.expectedErr {
					t.Fatalf("expected error '%s', got '%v'", tc.expectedErr, resp["error"])
				}
			}
		})
	}
}

func TestPasswordMinLength_AdminReset(t *testing.T) {
	h := setupPasswordTestEnv(t)

	r := gin.New()
	r.POST("/reset-admin", h.handlePostResetAdmin)

	setupValidToken := func() string {
		token := "valid_test_token_1234567890"
		_ = database.SetSetting("admin_reset_token", token)
		_ = database.SetSetting("admin_reset_expiry", strconv.FormatInt(time.Now().Add(1*time.Hour).Unix(), 10))
		return token
	}

	tests := []struct {
		name         string
		pass         string
		expectedCode int
		expectedErr  string
	}{
		{
			name:         "Single character rejected",
			pass:         "1",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "7 characters rejected",
			pass:         "1234567",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "Unicode 7 runes rejected",
			pass:         "世界你好123",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "8 characters accepted",
			pass:         "12345678",
			expectedCode: http.StatusOK,
			expectedErr:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := setupValidToken()

			form := url.Values{}
			form.Set("token", token)
			form.Set("password", tc.pass)

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/reset-admin", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			r.ServeHTTP(w, req)

			if w.Code != tc.expectedCode {
				t.Fatalf("expected status %d, got %d, body: %s", tc.expectedCode, w.Code, w.Body.String())
			}

			if tc.expectedErr != "" {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to parse JSON response: %v", err)
				}
				if resp["error"] != tc.expectedErr {
					t.Fatalf("expected error '%s', got '%v'", tc.expectedErr, resp["error"])
				}
			} else {
				// Verify token was cleared on success
				dbToken := database.GetSetting("admin_reset_token")
				if dbToken != "" {
					t.Errorf("expected reset token to be cleared, got: %s", dbToken)
				}
				// Verify authentication works with new password
				newHash := database.GetSetting("admin_password_hash")
				if err := bcrypt.CompareHashAndPassword([]byte(newHash), []byte(tc.pass)); err != nil {
					t.Errorf("failed to authenticate with newly reset password: %v", err)
				}
			}
		})
	}
}

func TestPasswordMinLength_ChildUser(t *testing.T) {
	h := setupPasswordTestEnv(t)

	r := gin.New()
	r.POST("/api/settings/users", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("is_admin", true)
		h.handlePostUser(c)
	})

	tests := []struct {
		name         string
		username     string
		pass         string
		expectedCode int
		expectedErr  string
	}{
		{
			name:         "Short password rejected (1 char)",
			username:     "childuser1",
			pass:         "1",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "Short password rejected (7 chars)",
			username:     "childuser2",
			pass:         "1234567",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "Unicode short password rejected",
			username:     "childuser3",
			pass:         "世界你好123",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "Valid password accepted (8 chars)",
			username:     "childuser4",
			pass:         "12345678",
			expectedCode: http.StatusOK,
			expectedErr:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("username", tc.username)
			form.Set("password", tc.pass)

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/settings/users", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			r.ServeHTTP(w, req)

			if w.Code != tc.expectedCode {
				t.Fatalf("expected status %d, got %d, body: %s", tc.expectedCode, w.Code, w.Body.String())
			}

			if tc.expectedErr != "" {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to parse JSON response: %v", err)
				}
				if resp["error"] != tc.expectedErr {
					t.Fatalf("expected error '%s', got '%v'", tc.expectedErr, resp["error"])
				}
			}
		})
	}
}

func TestPasswordMinLength_Setup(t *testing.T) {
	h := setupPasswordTestEnv(t)
	_ = database.DeleteSetting("admin_username")

	r := gin.New()
	r.POST("/setup", h.handlePostSetup)

	tests := []struct {
		name         string
		pass         string
		expectedCode int
		expectedErr  string
	}{
		{
			name:         "Single character rejected",
			pass:         "1",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
		{
			name:         "7 characters rejected",
			pass:         "1234567",
			expectedCode: http.StatusBadRequest,
			expectedErr:  "password_too_short",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("username", "adminsetup")
			form.Set("password", tc.pass)

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/setup", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			r.ServeHTTP(w, req)

			if w.Code != tc.expectedCode {
				t.Fatalf("expected status %d, got %d, body: %s", tc.expectedCode, w.Code, w.Body.String())
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse JSON response: %v", err)
			}
			if resp["error"] != tc.expectedErr {
				t.Fatalf("expected error '%s', got '%v'", tc.expectedErr, resp["error"])
			}
		})
	}
}
