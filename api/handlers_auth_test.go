package api

import (
	"context"
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

func setupAuthTestEnv(t *testing.T) *Handler {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbFile := tempDir + "/test_auth.db"
	if err := database.InitDB("sqlite", dbFile, ""); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.CloseDB()
	})

	// Set up admin user
	hash, err := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	database.SetSetting("admin_username", "admin")
	database.SetSetting("admin_password_hash", string(hash))

	cfg := &config.Config{
		ListenAddr: "127.0.0.1",
		Port:       "8091",
	}

	h := &Handler{
		cfg: cfg,
	}
	return h
}

func TestLoginRateLimiter_SequenceAndThreshold(t *testing.T) {
	h := setupAuthTestEnv(t)
	testIP := "192.0.2.1"
	clearLoginAttempts(testIP)

	r := gin.New()
	r.POST("/login", func(c *gin.Context) {
		// Mock client IP
		c.Request.RemoteAddr = testIP + ":12345"
		c.Request.Header.Set("X-Forwarded-For", testIP)
		h.handlePostLogin(c)
	})

	postLogin := func(password string) (int, string) {
		form := url.Values{}
		form.Set("username", "admin")
		form.Set("password", password)

		req, _ := http.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = testIP + ":12345"
		req.Header.Set("X-Forwarded-For", testIP)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	// 1. Failed login #1 -> allowed to attempt, rejected as 401 unauthorized
	code, body := postLogin("wrong1")
	if code != http.StatusUnauthorized {
		t.Fatalf("Failed login #1: expected status 401, got %d (body: %s)", code, body)
	}
	if v, _ := loginAttempts.Load(testIP); v.(loginAttempt).count != 1 {
		t.Fatalf("Failed login #1: expected attempt count 1, got %d", v.(loginAttempt).count)
	}

	// 2. Failed login #2 -> allowed to attempt, rejected as 401 unauthorized
	code, body = postLogin("wrong2")
	if code != http.StatusUnauthorized {
		t.Fatalf("Failed login #2: expected status 401, got %d (body: %s)", code, body)
	}
	if v, _ := loginAttempts.Load(testIP); v.(loginAttempt).count != 2 {
		t.Fatalf("Failed login #2: expected attempt count 2, got %d", v.(loginAttempt).count)
	}

	// 3. Failed login #3 -> allowed to attempt, rejected as 401 unauthorized
	code, body = postLogin("wrong3")
	if code != http.StatusUnauthorized {
		t.Fatalf("Failed login #3: expected status 401, got %d (body: %s)", code, body)
	}
	if v, _ := loginAttempts.Load(testIP); v.(loginAttempt).count != 3 {
		t.Fatalf("Failed login #3: expected attempt count 3, got %d", v.(loginAttempt).count)
	}

	// 4. Failed login #4 -> allowed to attempt, rejected as 401 unauthorized
	code, body = postLogin("wrong4")
	if code != http.StatusUnauthorized {
		t.Fatalf("Failed login #4: expected status 401, got %d (body: %s)", code, body)
	}
	if v, _ := loginAttempts.Load(testIP); v.(loginAttempt).count != 4 {
		t.Fatalf("Failed login #4: expected attempt count 4, got %d", v.(loginAttempt).count)
	}

	// 5. Failed login #5 -> reaches threshold of 5, returns 429 ip_blocked
	code, body = postLogin("wrong5")
	if code != http.StatusTooManyRequests {
		t.Fatalf("Failed login #5: expected status 429 (lockout), got %d (body: %s)", code, body)
	}
	if !strings.Contains(body, "ip_blocked") {
		t.Fatalf("Failed login #5: expected body containing 'ip_blocked', got: %s", body)
	}
	if v, _ := loginAttempts.Load(testIP); v.(loginAttempt).count != 5 {
		t.Fatalf("Failed login #5: expected attempt count 5, got %d", v.(loginAttempt).count)
	}

	// 6. Failed login #6 (subsequent request while locked out) -> returns 429 too_many_requests immediately
	code, body = postLogin("wrong6")
	if code != http.StatusTooManyRequests {
		t.Fatalf("Subsequent attempt while locked out: expected status 429, got %d", code)
	}
	if !strings.Contains(body, "too_many_requests") {
		t.Fatalf("Subsequent attempt: expected 'too_many_requests', got: %s", body)
	}

	// 7. Successful login resets attempt state
	clearLoginAttempts(testIP) // reset for testing clean login
	// Let's create 3 failed attempts
	postLogin("wrong")
	postLogin("wrong")
	postLogin("wrong")
	if v, _ := loginAttempts.Load(testIP); v.(loginAttempt).count != 3 {
		t.Fatalf("Expected attempt count 3 before success, got %v", v)
	}
	// Now successful login
	code, body = postLogin("correctpassword")
	if code != http.StatusOK {
		t.Fatalf("Successful login: expected status 200, got %d (body: %s)", code, body)
	}
	if v, ok := loginAttempts.Load(testIP); ok && v != nil {
		t.Fatalf("Expected login attempts to be cleared after success, got %v", v)
	}
	// Next failed login after reset starts at 1
	code, _ = postLogin("wrong_again")
	if code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 after reset, got %d", code)
	}
	if v, _ := loginAttempts.Load(testIP); v.(loginAttempt).count != 1 {
		t.Fatalf("Expected attempt count 1 after reset, got %d", v.(loginAttempt).count)
	}
}

func TestLoginRateLimiter_ConcurrentRequests(t *testing.T) {
	testIP := "198.51.100.42"
	clearLoginAttempts(testIP)

	concurrency := 50
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			bumpAttempt(testIP)
		}()
	}

	wg.Wait()

	v, ok := loginAttempts.Load(testIP)
	if !ok || v == nil {
		t.Fatalf("expected loginAttempts to have entry for %s", testIP)
	}
	att := v.(loginAttempt)
	if att.count != concurrency {
		t.Fatalf("expected count %d after concurrent bumps, got %d", concurrency, att.count)
	}
}

func TestLoginRateLimiter_WindowExpiry(t *testing.T) {
	testIP := "198.51.100.99"
	clearLoginAttempts(testIP)

	// Simulate 4 attempts 16 minutes ago
	loginAttemptsMu.Lock()
	loginAttempts.Store(testIP, loginAttempt{
		count: 4,
		last:  time.Now().Add(-16 * time.Minute),
	})
	loginAttemptsMu.Unlock()

	// isIPRateLimited should report false because last attempt is expired
	if isIPRateLimited(testIP) {
		t.Errorf("expected isIPRateLimited to be false for expired entry")
	}

	// bumpAttempt should reset count to 1 rather than 5
	newCount := bumpAttempt(testIP)
	if newCount != 1 {
		t.Errorf("expected count to reset to 1 after expiry, got %d", newCount)
	}
}

func TestLoginAttempts_CleanupStaleEntries(t *testing.T) {
	staleIP := "198.51.100.101"
	activeIP := "198.51.100.102"
	clearLoginAttempts(staleIP)
	clearLoginAttempts(activeIP)

	loginAttemptsMu.Lock()
	// Stale: last activity 20 minutes ago
	loginAttempts.Store(staleIP, loginAttempt{
		count: 3,
		last:  time.Now().Add(-20 * time.Minute),
	})
	// Active: last activity 2 minutes ago
	loginAttempts.Store(activeIP, loginAttempt{
		count: 2,
		last:  time.Now().Add(-2 * time.Minute),
	})
	loginAttemptsMu.Unlock()

	cleaned := CleanupStaleLoginAttempts(15 * time.Minute)
	if cleaned < 1 {
		t.Errorf("expected at least 1 cleaned entry, got %d", cleaned)
	}

	loginAttemptsMu.Lock()
	_, staleExists := loginAttempts.Load(staleIP)
	vActive, activeExists := loginAttempts.Load(activeIP)
	loginAttemptsMu.Unlock()

	if staleExists {
		t.Errorf("stale IP entry was not removed by cleanup")
	}
	if !activeExists {
		t.Errorf("active IP entry was prematurely removed by cleanup")
	} else if vActive.(loginAttempt).count != 2 {
		t.Errorf("active IP entry count altered, expected 2, got %d", vActive.(loginAttempt).count)
	}
}

func TestLoginAttempts_BackgroundLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testIP := "198.51.100.103"
	clearLoginAttempts(testIP)

	loginAttemptsMu.Lock()
	loginAttempts.Store(testIP, loginAttempt{
		count: 4,
		last:  time.Now().Add(-16 * time.Minute),
	})
	loginAttemptsMu.Unlock()

	// Start background cleanup with short interval for test
	StartLoginAttemptsCleanup(ctx, 20*time.Millisecond)

	// Wait for cleanup to fire
	time.Sleep(60 * time.Millisecond)

	loginAttemptsMu.Lock()
	_, exists := loginAttempts.Load(testIP)
	loginAttemptsMu.Unlock()

	if exists {
		t.Errorf("expected background cleaner to remove stale entry")
	}

	// Cancel context - goroutine must stop cleanly
	cancel()
	time.Sleep(30 * time.Millisecond)
}
