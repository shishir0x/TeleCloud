package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"telecloud/config"
	"telecloud/database"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Initialize a dummy in-memory database to prevent database.RODB nil-pointer panics
	_ = database.InitDB("sqlite", ":memory:", "")

	r := gin.New()
	r.Use(securityHeadersMiddleware())

	// Set up dummy routes to test the middleware headers
	r.GET("/s/:token/cbz/page", func(c *gin.Context) {
		c.String(http.StatusOK, "cbz page")
	})
	r.GET("/s/:token/epub/resource/content.xhtml", func(c *gin.Context) {
		c.String(http.StatusOK, "epub content")
	})
	r.GET("/s/:token/stream", func(c *gin.Context) {
		c.String(http.StatusOK, "other stream")
	})

	tests := []struct {
		name                string
		method              string
		path                string
		expectXFrameOptions bool
	}{
		{
			name:                "CBZ Page - X-Frame-Options should be omitted",
			method:              "GET",
			path:                "/s/dummy-token/cbz/page",
			expectXFrameOptions: false,
		},
		{
			name:                "EPUB Resource - X-Frame-Options should be omitted",
			method:              "GET",
			path:                "/s/dummy-token/epub/resource/content.xhtml",
			expectXFrameOptions: false,
		},
		{
			name:                "Standard Shared Stream - X-Frame-Options should be SAMEORIGIN",
			method:              "GET",
			path:                "/s/dummy-token/stream",
			expectXFrameOptions: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(tt.method, tt.path, nil)
			r.ServeHTTP(w, req)

			xFrame := w.Header().Get("X-Frame-Options")
			if tt.expectXFrameOptions {
				if xFrame != "SAMEORIGIN" {
					t.Errorf("expected X-Frame-Options to be SAMEORIGIN, got %q", xFrame)
				}
			} else {
				if xFrame != "" {
					t.Errorf("expected X-Frame-Options to be omitted, got %q", xFrame)
				}
			}

			// Verify Content-Security-Policy header
			csp := w.Header().Get("Content-Security-Policy")
			if csp == "" {
				t.Fatalf("expected Content-Security-Policy header to be present")
			}
			requiredDirectives := []string{
				"default-src 'self'",
				"script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:",
				"style-src 'self' 'unsafe-inline'",
				"img-src 'self' data: blob:",
				"font-src 'self' data:",
				"connect-src 'self' ws: wss: blob: data: https://api.github.com",
				"media-src 'self' blob: data:",
				"worker-src 'self' blob:",
				"child-src 'self' blob:",
				"frame-src 'self' blob: data:",
				"object-src 'none'",
				"base-uri 'self'",
				"frame-ancestors 'self'",
			}
			for _, directive := range requiredDirectives {
				if !strings.Contains(csp, directive) {
					t.Errorf("expected CSP to contain %q, but got %q", directive, csp)
				}
			}
		})
	}
}

func TestSharePasswordRateLimiting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbFile := tempDir + "/test_share_rate_limit.db"
	if err := database.InitDB("sqlite", dbFile, ""); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.CloseDB()
	})

	hashA, _ := bcrypt.GenerateFromPassword([]byte("secretA"), bcrypt.DefaultCost)
	hashB, _ := bcrypt.GenerateFromPassword([]byte("secretB"), bcrypt.DefaultCost)
	tokenA := "share-token-a"
	tokenB := "share-token-b"

	_, err := database.DB.Exec(`
		INSERT INTO files (filename, size, path, share_token, share_password)
		VALUES ('testA.txt', 100, '/testA.txt', ?, ?),
		       ('testB.txt', 200, '/testB.txt', ?, ?)
	`, tokenA, string(hashA), tokenB, string(hashB))
	if err != nil {
		t.Fatalf("failed to insert test files: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			ListenAddr: "127.0.0.1",
			Port:       "8091",
		},
	}

	r := gin.New()
	r.POST("/s/:token/verify", h.handleVerifySharePassword)

	verifyRequest := func(clientIP, token, password string) (int, string) {
		form := url.Values{}
		form.Set("password", password)
		req, _ := http.NewRequest("POST", "/s/"+token+"/verify", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = clientIP + ":1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	ip1 := "198.51.100.1"
	ip2 := "198.51.100.2"

	// 1. One incorrect password is rejected normally (401)
	code, body := verifyRequest(ip1, tokenA, "wrongpassword")
	if code != http.StatusUnauthorized {
		t.Fatalf("Attempt 1: expected status 401, got %d (body: %s)", code, body)
	}
	if !strings.Contains(body, "incorrect_password") {
		t.Fatalf("Attempt 1: expected 'incorrect_password', got: %s", body)
	}

	// 2. Attempts 2-4 rejected normally with 401
	for attempt := 2; attempt <= 4; attempt++ {
		code, body = verifyRequest(ip1, tokenA, fmt.Sprintf("wrong-%d", attempt))
		if code != http.StatusUnauthorized {
			t.Fatalf("Attempt %d: expected status 401, got %d (body: %s)", attempt, code, body)
		}
	}

	// 3. Repeated incorrect attempts (attempt 5) trigger throttling (429)
	code, body = verifyRequest(ip1, tokenA, "wrong-5")
	if code != http.StatusTooManyRequests {
		t.Fatalf("Attempt 5: expected status 429, got %d (body: %s)", code, body)
	}
	if !strings.Contains(body, "too_many_requests") {
		t.Fatalf("Attempt 5: expected 'too_many_requests', got: %s", body)
	}

	// 4. Subsequent attempts while locked out return 429 immediately
	code, body = verifyRequest(ip1, tokenA, "wrong-6")
	if code != http.StatusTooManyRequests {
		t.Fatalf("Attempt 6 (locked out): expected status 429, got %d", code)
	}

	// 5. Share A's failed attempts do not incorrectly lock Share B for the same IP
	code, body = verifyRequest(ip1, tokenB, "secretB")
	if code != http.StatusOK {
		t.Fatalf("Share B from ip1: expected 200 OK, got %d (body: %s)", code, body)
	}

	// 6. Different clients/IPs behave according to intended policy:
	// ip2 is NOT locked out on Share A despite ip1 being locked out
	code, body = verifyRequest(ip2, tokenA, "secretA")
	if code != http.StatusOK {
		t.Fatalf("Share A from ip2: expected 200 OK, got %d (body: %s)", code, body)
	}

	// 7. Successful verification resets failed-attempt state for ip2
	// Test by entering 3 wrong guesses from ip2, then correct password, then check counter reset
	verifyRequest(ip2, tokenA, "wrong-ip2-1")
	verifyRequest(ip2, tokenA, "wrong-ip2-2")
	code, _ = verifyRequest(ip2, tokenA, "secretA")
	if code != http.StatusOK {
		t.Fatalf("Expected 200 on correct password, got %d", code)
	}
	// Since state was cleared, 4 more wrong guesses from ip2 should still be 401, not 429
	for i := 1; i <= 4; i++ {
		code, _ = verifyRequest(ip2, tokenA, fmt.Sprintf("wrong-after-reset-%d", i))
		if code != http.StatusUnauthorized {
			t.Fatalf("After reset attempt %d: expected 401, got %d", i, code)
		}
	}

	// 8. Test that a valid share token alone cannot be used to perform unlimited password guesses
	// Even across rotating IPs, once token total reaches 10 failed attempts, it throttles
	tokenC := "share-token-c"
	hashC, _ := bcrypt.GenerateFromPassword([]byte("secretC"), bcrypt.DefaultCost)
	database.DB.Exec("INSERT INTO files (filename, size, path, share_token, share_password) VALUES ('testC.txt', 100, '/testC.txt', ?, ?)", tokenC, string(hashC))

	for i := 1; i <= 10; i++ {
		rotatingIP := fmt.Sprintf("10.0.%d.%d", i, i)
		verifyRequest(rotatingIP, tokenC, "wrong")
	}
	// 11th attempt from brand new IP must be rejected with 429
	code, body = verifyRequest("10.99.99.99", tokenC, "wrong")
	if code != http.StatusTooManyRequests {
		t.Fatalf("Token-level lockout: expected 429 after 10 attempts across IPs, got %d (body: %s)", code, body)
	}

	// 9. Concurrent requests cannot bypass counter through a race condition
	tokenD := "share-token-d"
	hashD, _ := bcrypt.GenerateFromPassword([]byte("secretD"), bcrypt.DefaultCost)
	database.DB.Exec("INSERT INTO files (filename, size, path, share_token, share_password) VALUES ('testD.txt', 100, '/testD.txt', ?, ?)", tokenD, string(hashD))

	ipConcurrent := "198.51.100.99"
	var wg sync.WaitGroup
	var mu sync.Mutex
	statusCodes := make([]int, 0, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, _ := verifyRequest(ipConcurrent, tokenD, fmt.Sprintf("wrong-%d", idx))
			mu.Lock()
			statusCodes = append(statusCodes, c)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// Out of 10 concurrent requests from same IP, at most 4 can be 401; the rest MUST be 429
	count401 := 0
	count429 := 0
	for _, sc := range statusCodes {
		if sc == http.StatusUnauthorized {
			count401++
		} else if sc == http.StatusTooManyRequests {
			count429++
		}
	}
	if count401 > 4 {
		t.Fatalf("Expected at most 4 unauthorized (401) responses in concurrent burst, got %d (429 count: %d)", count401, count429)
	}
	if count429 < 5 {
		t.Fatalf("Expected at least 5 throttled (429) responses in concurrent burst, got %d", count429)
	}

	// 10. Throttling eventually expires according to configured policy (15 minutes)
	pairKey := "share_pair:" + ip1 + ":" + tokenA
	loginAttemptsMu.Lock()
	if v, ok := loginAttempts.Load(pairKey); ok {
		att := v.(loginAttempt)
		att.last = time.Now().Add(-16 * time.Minute)
		loginAttempts.Store(pairKey, att)
	}
	loginAttemptsMu.Unlock()

	code, body = verifyRequest(ip1, tokenA, "wrong-after-expiry")
	if code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 after throttle expiration, got %d (body: %s)", code, body)
	}
}

func TestShareRevocationInvalidatesSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbFile := tempDir + "/test_share_revocation.db"
	if err := database.InitDB("sqlite", dbFile, ""); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.CloseDB()
	})

	hash1, _ := bcrypt.GenerateFromPassword([]byte("pass1"), bcrypt.DefaultCost)
	hash2, _ := bcrypt.GenerateFromPassword([]byte("pass2"), bcrypt.DefaultCost)
	token1 := "token-share-1"
	token2 := "token-share-2"

	// 1. Create shares for File 1 (Alice) and File 2 (Bob)
	res1, err := database.DB.Exec(`
		INSERT INTO files (filename, size, path, share_token, share_password, owner)
		VALUES ('file1.txt', 100, '/file1.txt', ?, ?, 'alice')
	`, token1, string(hash1))
	if err != nil {
		t.Fatalf("failed to insert file1: %v", err)
	}
	id1, _ := res1.LastInsertId()

	res2, err := database.DB.Exec(`
		INSERT INTO files (filename, size, path, share_token, share_password, owner)
		VALUES ('file2.txt', 200, '/file2.txt', ?, ?, 'bob')
	`, token2, string(hash2))
	if err != nil {
		t.Fatalf("failed to insert file2: %v", err)
	}
	_ = res2

	h := &Handler{
		cfg: &config.Config{
			ListenAddr: "127.0.0.1",
			Port:       "8091",
		},
	}

	r := gin.New()
	r.POST("/s/:token/verify", h.handleVerifySharePassword)
	r.DELETE("/api/shares/:id", func(c *gin.Context) {
		// Mock authenticated user as alice
		c.Set("username", "alice")
		c.Set("is_admin", false)
		h.handleRevokeShare(c)
	})

	// 2. Authenticate using the share password for token 1
	form1 := url.Values{}
	form1.Set("password", "pass1")
	req1, _ := http.NewRequest("POST", "/s/"+token1+"/verify", strings.NewReader(form1.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("Verify token1: expected 200 OK, got %d (body: %s)", w1.Code, w1.Body.String())
	}

	// Extract session cookie for token1
	cookies1 := w1.Result().Cookies()
	var sessionToken1 string
	for _, ck := range cookies1 {
		if ck.Name == "share_auth_"+token1 {
			sessionToken1 = ck.Value
			break
		}
	}
	if sessionToken1 == "" {
		t.Fatalf("Failed to extract session token from cookie, got cookies: %v", cookies1)
	}

	// Authenticate token 2 as well
	form2 := url.Values{}
	form2.Set("password", "pass2")
	req2, _ := http.NewRequest("POST", "/s/"+token2+"/verify", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("Verify token2: expected 200 OK, got %d", w2.Code)
	}

	// 3. Confirm the share session works (row exists in share_sessions and checkShareAuth returns true)
	var count1 int
	err = database.RODB.Get(&count1, "SELECT COUNT(*) FROM share_sessions WHERE token = ? AND share_token = ?", sessionToken1, token1)
	if err != nil || count1 != 1 {
		t.Fatalf("Expected 1 session row for token1, got %d (err: %v)", count1, err)
	}

	dummyFile1 := database.File{
		SharePassword: &[]string{string(hash1)}[0],
	}
	ginCtx1, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx1.Params = gin.Params{{Key: "token", Value: token1}}
	ginCtx1.Request, _ = http.NewRequest("GET", "/s/"+token1, nil)
	ginCtx1.Request.AddCookie(&http.Cookie{Name: "share_auth_" + token1, Value: sessionToken1})
	ckVal, ckErr := ginCtx1.Cookie("share_auth_" + token1)
	if ckErr != nil || ckVal != sessionToken1 {
		t.Fatalf("ginCtx1.Cookie failed: val=%q, err=%v", ckVal, ckErr)
	}
	if !h.checkShareAuth(ginCtx1, dummyFile1) {
		t.Fatalf("Expected checkShareAuth to return true for valid session cookie")
	}

	// 4. Revoke the share for file 1
	reqRevoke, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/shares/%d", id1), nil)
	wRevoke := httptest.NewRecorder()
	r.ServeHTTP(wRevoke, reqRevoke)
	if wRevoke.Code != http.StatusOK {
		t.Fatalf("Revoke share: expected 200 OK, got %d (body: %s)", wRevoke.Code, wRevoke.Body.String())
	}

	// 5. Confirm the corresponding share_sessions row is DELETED
	err = database.RODB.Get(&count1, "SELECT COUNT(*) FROM share_sessions WHERE share_token = ?", token1)
	if err != nil || count1 != 0 {
		t.Fatalf("Expected 0 sessions remaining for token1, got %d", count1)
	}

	// 6. Reuse the previously issued session cookie -> confirm access is denied
	ginCtxRevoked, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtxRevoked.Params = gin.Params{{Key: "token", Value: token1}}
	ginCtxRevoked.Request, _ = http.NewRequest("GET", "/s/"+token1, nil)
	ginCtxRevoked.Request.AddCookie(&http.Cookie{Name: "share_auth_" + token1, Value: sessionToken1})
	if h.checkShareAuth(ginCtxRevoked, dummyFile1) {
		t.Fatalf("Expected checkShareAuth to return FALSE when reusing session cookie after revocation")
	}

	// 7. Verify unrelated sessions (for token2) remain valid
	var count2 int
	err = database.RODB.Get(&count2, "SELECT COUNT(*) FROM share_sessions WHERE share_token = ?", token2)
	if err != nil || count2 != 1 {
		t.Fatalf("Expected session for token2 to remain valid, got count %d", count2)
	}

	// 8. Atomicity verification: if a transaction rolls back, session is preserved
	txRollback, err := database.DB.Beginx()
	if err != nil {
		t.Fatalf("Beginx failed: %v", err)
	}
	if _, err := txRollback.Exec("DELETE FROM share_sessions WHERE share_token = ?", token2); err != nil {
		t.Fatalf("DELETE in tx failed: %v", err)
	}
	_ = txRollback.Rollback()

	err = database.RODB.Get(&count2, "SELECT COUNT(*) FROM share_sessions WHERE share_token = ?", token2)
	if err != nil || count2 != 1 {
		t.Fatalf("Expected rollback to preserve session for token2, got count %d", count2)
	}
}
