package api

import (
	"encoding/json"
	"html/template"
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

func setupResetTokenTestEnv(t *testing.T) *Handler {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbFile := tempDir + "/test_reset_token.db"
	if err := database.InitDB("sqlite", dbFile, ""); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.CloseDB()
	})

	hash, err := bcrypt.GenerateFromPassword([]byte("initialpass123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	_ = database.SetSetting("admin_username", "admin")
	_ = database.SetSetting("admin_password_hash", string(hash))

	cfg := &config.Config{
		ListenAddr: "127.0.0.1",
		Port:       "8091",
		Version:    "1.0.0",
	}

	return &Handler{
		cfg: cfg,
	}
}

func TestResetToken_ConstantTimeComparison(t *testing.T) {
	h := setupResetTokenTestEnv(t)

	r := gin.New()
	r.SetHTMLTemplate(template.Must(template.New("reset-admin.html").Parse("<html>{{.version}}</html>")))
	r.GET("/reset-admin", h.handleGetResetAdmin)
	r.POST("/reset-admin", h.handlePostResetAdmin)

	validToken := "550e8400-e29b-41d4-a716-446655440000"

	tests := []struct {
		name          string
		setDBToken    string
		tokenExpiry   time.Duration
		queryToken    string
		postToken     string
		password      string
		expectedGet   int
		expectedPost  int
		expectSuccess bool
	}{
		{
			name:          "Correct token accepted",
			setDBToken:    validToken,
			tokenExpiry:   1 * time.Hour,
			queryToken:    validToken,
			postToken:     validToken,
			password:      "newsecurepassword8",
			expectedGet:   http.StatusOK,
			expectedPost:  http.StatusOK,
			expectSuccess: true,
		},
		{
			name:          "Incorrect token with same length rejected",
			setDBToken:    validToken,
			tokenExpiry:   1 * time.Hour,
			queryToken:    "550e8400-e29b-41d4-a716-446655440001",
			postToken:     "550e8400-e29b-41d4-a716-446655440001",
			password:      "newsecurepassword8",
			expectedGet:   http.StatusForbidden,
			expectedPost:  http.StatusForbidden,
			expectSuccess: false,
		},
		{
			name:          "Different length token rejected (shorter)",
			setDBToken:    validToken,
			tokenExpiry:   1 * time.Hour,
			queryToken:    "550e8400",
			postToken:     "550e8400",
			password:      "newsecurepassword8",
			expectedGet:   http.StatusForbidden,
			expectedPost:  http.StatusForbidden,
			expectSuccess: false,
		},
		{
			name:          "Different length token rejected (longer)",
			setDBToken:    validToken,
			tokenExpiry:   1 * time.Hour,
			queryToken:    validToken + "_extra_data",
			postToken:     validToken + "_extra_data",
			password:      "newsecurepassword8",
			expectedGet:   http.StatusForbidden,
			expectedPost:  http.StatusForbidden,
			expectSuccess: false,
		},
		{
			name:          "Empty token rejected",
			setDBToken:    validToken,
			tokenExpiry:   1 * time.Hour,
			queryToken:    "",
			postToken:     "",
			password:      "newsecurepassword8",
			expectedGet:   http.StatusForbidden,
			expectedPost:  http.StatusForbidden,
			expectSuccess: false,
		},
		{
			name:          "Empty DB token rejected (no reset active)",
			setDBToken:    "",
			tokenExpiry:   1 * time.Hour,
			queryToken:    "",
			postToken:     "",
			password:      "newsecurepassword8",
			expectedGet:   http.StatusForbidden,
			expectedPost:  http.StatusForbidden,
			expectSuccess: false,
		},
		{
			name:          "Expired token rejected",
			setDBToken:    validToken,
			tokenExpiry:   -1 * time.Hour, // expired
			queryToken:    validToken,
			postToken:     validToken,
			password:      "newsecurepassword8",
			expectedGet:   http.StatusForbidden,
			expectedPost:  http.StatusForbidden,
			expectSuccess: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setDBToken != "" {
				_ = database.SetSetting("admin_reset_token", tc.setDBToken)
				_ = database.SetSetting("admin_reset_expiry", strconv.FormatInt(time.Now().Add(tc.tokenExpiry).Unix(), 10))
			} else {
				_ = database.DeleteSetting("admin_reset_token")
				_ = database.DeleteSetting("admin_reset_expiry")
			}

			// Test GET /reset-admin
			getReq := httptest.NewRequest("GET", "/reset-admin?token="+url.QueryEscape(tc.queryToken), nil)
			getW := httptest.NewRecorder()
			r.ServeHTTP(getW, getReq)

			if getW.Code != tc.expectedGet {
				t.Errorf("GET /reset-admin status: expected %d, got %d, body: %s", tc.expectedGet, getW.Code, getW.Body.String())
			}

			// Test POST /reset-admin
			postForm := url.Values{}
			postForm.Set("token", tc.postToken)
			postForm.Set("password", tc.password)
			postReq := httptest.NewRequest("POST", "/reset-admin", strings.NewReader(postForm.Encode()))
			postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			postW := httptest.NewRecorder()
			r.ServeHTTP(postW, postReq)

			if postW.Code != tc.expectedPost {
				t.Errorf("POST /reset-admin status: expected %d, got %d, body: %s", tc.expectedPost, postW.Code, postW.Body.String())
			}

			// Check response body does not leak sensitive information
			postBody := postW.Body.String()
			if strings.Contains(postBody, validToken) {
				t.Errorf("POST /reset-admin leaked reset token in response body: %s", postBody)
			}

			if tc.expectSuccess {
				var resp map[string]interface{}
				if err := json.Unmarshal(postW.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to unmarshal JSON: %v", err)
				}
				if resp["status"] != "success" {
					t.Errorf("expected status 'success', got '%v'", resp["status"])
				}

				// Verify token is deleted after successful use
				if tok := database.GetSetting("admin_reset_token"); tok != "" {
					t.Errorf("expected reset token to be deleted, got: %s", tok)
				}

				// Verify new password works
				hash := database.GetSetting("admin_password_hash")
				if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(tc.password)); err != nil {
					t.Errorf("new password failed bcrypt verification: %v", err)
				}
			}
		})
	}
}
