package api

import (
	"net/http"
	"net/http/httptest"
	"telecloud/database"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInitWebAuthn_InvalidConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = database.InitDB("sqlite", ":memory:", "")

	// Explicitly simulate when WebAuthn failed initialization and webAuthn == nil
	webAuthn = nil

	// Check GetWebAuthnConfig returns empty
	rpid, origins := GetWebAuthnConfig()
	if rpid != "" || origins != nil {
		t.Fatalf("expected empty config when webAuthn is nil, got rpid=%q, origins=%v", rpid, origins)
	}

	// Verify endpoints return 503 instead of panicking
	r := gin.New()
	r.GET("/passkey/register/begin", RegisterPasskeyBegin)
	r.POST("/passkey/register/finish", RegisterPasskeyFinish)
	r.GET("/passkey/login/begin", LoginPasskeyBegin)
	r.POST("/passkey/login/finish", LoginPasskeyFinish)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/passkey/register/begin"},
		{"POST", "/passkey/register/finish"},
		{"GET", "/passkey/login/begin"},
		{"POST", "/passkey/login/finish"},
	}

	for _, ep := range endpoints {
		req, _ := http.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("[%s %s] expected status %d (ServiceUnavailable) when webAuthn is nil, got %d. Body: %s",
				ep.method, ep.path, http.StatusServiceUnavailable, w.Code, w.Body.String())
		}
	}
}

func TestInitWebAuthn_ValidConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = database.InitDB("sqlite", ":memory:", "")

	// Valid config initializes successfully
	InitWebAuthn("localhost", []string{"http://localhost:8091"})
	if webAuthn == nil {
		t.Fatal("expected webAuthn to be non-nil after valid InitWebAuthn")
	}

	rpid, origins := GetWebAuthnConfig()
	if rpid != "localhost" {
		t.Errorf("expected rpid=localhost, got %s", rpid)
	}
	if len(origins) != 1 || origins[0] != "http://localhost:8091" {
		t.Errorf("expected origins=[http://localhost:8091], got %v", origins)
	}
}
