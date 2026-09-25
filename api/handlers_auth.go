package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"telecloud/database"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) handleGetLogin(c *gin.Context) {
	adminUser := database.GetSetting("admin_username")
	if adminUser == "" {
		c.Redirect(http.StatusFound, "/setup")
		return
	}

	token, _ := c.Cookie("session_token")
	sessionUsername := database.LookupSessionUser(token)
	if sessionUsername != "" {
		c.Redirect(http.StatusFound, "/")
		return
	}
	setCSRFCookie(c)
	c.HTML(http.StatusOK, "login.html", gin.H{
		"version": h.cfg.Version,
	})
}

// bumpAttempt records a failed authentication attempt against the given IP
// and returns the updated attempt count.
// Shared by /login, /setup, and Basic Auth so a determined attacker can't trivially burn
// attempts on one endpoint and switch to the other.
func bumpAttempt(ip string) int {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()

	v, _ := loginAttempts.Load(ip)
	var att loginAttempt
	if v != nil {
		att = v.(loginAttempt)
		if time.Since(att.last) >= 15*time.Minute {
			att.count = 0
		}
	}
	att.count++
	att.last = time.Now()
	loginAttempts.Store(ip, att)
	return att.count
}

func isRateLimited(key string, maxAttempts int) bool {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()

	v, _ := loginAttempts.Load(key)
	if v == nil {
		return false
	}
	att := v.(loginAttempt)
	if time.Since(att.last) >= 15*time.Minute {
		loginAttempts.Delete(key)
		return false
	}
	return att.count >= maxAttempts
}

func isIPRateLimited(ip string) bool {
	return isRateLimited(ip, 5)
}

func clearLoginAttempts(ip string) {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	loginAttempts.Delete(ip)
}

// CleanupStaleLoginAttempts removes login attempt entries whose last activity
// was older than maxAge. It is concurrency-safe and returns the number of cleaned entries.
func CleanupStaleLoginAttempts(maxAge time.Duration) int {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()

	cleaned := 0
	now := time.Now()
	loginAttempts.Range(func(key, value any) bool {
		if att, ok := value.(loginAttempt); ok {
			if now.Sub(att.last) >= maxAge {
				loginAttempts.Delete(key)
				cleaned++
			}
		}
		return true
	})
	return cleaned
}

// StartLoginAttemptsCleanup starts a background goroutine that periodically sweeps
// stale login attempts older than 15 minutes. It stops cleanly when ctx is cancelled.
func StartLoginAttemptsCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				CleanupStaleLoginAttempts(15 * time.Minute)
			}
		}
	}()
}

func (h *Handler) handlePostLogin(c *gin.Context) {
	ip := c.ClientIP()
	if isIPRateLimited(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too_many_requests"})
		return
	}

	username := c.PostForm("username")
	password := c.PostForm("password")

	dbUser := database.GetSetting("admin_username")
	dbHash := database.GetSetting("admin_password_hash")

	var authSuccess bool
	var forceChange bool
	if username == dbUser && bcrypt.CompareHashAndPassword([]byte(dbHash), []byte(password)) == nil {
		authSuccess = true
	} else {
		var child struct {
			Hash        string `db:"password_hash"`
			ForceChange int    `db:"force_password_change"`
		}
		err := database.RODB.Get(&child, "SELECT password_hash, force_password_change FROM child_accounts WHERE username = ?", username)
		if err == nil && bcrypt.CompareHashAndPassword([]byte(child.Hash), []byte(password)) == nil {
			authSuccess = true
			forceChange = child.ForceChange == 1
		}
	}

	if authSuccess {
		if forceChange {
			c.JSON(http.StatusOK, gin.H{"status": "force_password_change", "username": username})
			return
		}
		clearLoginAttempts(ip) // Reset on success
		sessionToken, err := database.CreateSession(username)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
			return
		}
		c.SetCookie("session_token", sessionToken, int(database.SessionTTL.Seconds()), "/", "", isSecure(), true)
		database.LogAuditFromCtx(c, username, database.AuditActionLoginSuccess, "", database.AuditStatusOK)
		c.JSON(http.StatusOK, gin.H{"status": "success"})
		return
	}

	// On failure
	attempts := bumpAttempt(ip)

	// Artificial delay to thwart fast scripts (skip in test mode for test speed)
	if gin.Mode() != gin.TestMode {
		time.Sleep(1 * time.Second)
	}

	database.LogAuditFromCtx(c, username, database.AuditActionLoginFail, "", database.AuditStatusDenied)
	if attempts >= 5 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "ip_blocked"})
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
	}
}

func (h *Handler) handleLogout(c *gin.Context) {
	token, _ := c.Cookie("session_token")
	actor := database.LookupSessionUser(token)
	if token != "" {
		database.DB.Exec("DELETE FROM sessions WHERE token = ?", token)
	}
	c.SetCookie("session_token", "", -1, "/", "", isSecure(), true)
	c.SetCookie(csrfCookieName, "", -1, "/", "", isSecure(), false)
	if actor != "" {
		database.LogAuditFromCtx(c, actor, database.AuditActionLogout, "", database.AuditStatusOK)
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *Handler) handleGetResetAdmin(c *gin.Context) {
	token := strings.TrimSpace(c.Query("token"))
	dbToken := strings.TrimSpace(database.GetSetting("admin_reset_token"))
	expiryStr := strings.TrimSpace(database.GetSetting("admin_reset_expiry"))

	if token == "" || dbToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(dbToken)) != 1 {
		c.String(http.StatusForbidden, "Invalid token")
		return
	}

	expiry, _ := strconv.ParseInt(expiryStr, 10, 64)
	if time.Now().Unix() > expiry {
		c.String(http.StatusForbidden, "Token expired")
		return
	}

	setCSRFCookie(c)
	c.HTML(http.StatusOK, "reset-admin.html", gin.H{
		"version": h.cfg.Version,
	})
}

func (h *Handler) handlePostResetAdmin(c *gin.Context) {
	token := strings.TrimSpace(c.PostForm("token"))
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	password := c.PostForm("password")
	if utf8.RuneCountInString(password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "password_too_short",
			"message": "Password must be at least 8 characters long",
		})
		return
	}

	dbToken := strings.TrimSpace(database.GetSetting("admin_reset_token"))
	expiryStr := strings.TrimSpace(database.GetSetting("admin_reset_expiry"))

	if token == "" || dbToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(dbToken)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid_token"})
		return
	}

	expiry, _ := strconv.ParseInt(expiryStr, 10, 64)
	if time.Now().Unix() > expiry {
		c.JSON(http.StatusForbidden, gin.H{"error": "token_expired"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_hash_password"})
		return
	}

	database.SetSetting("admin_password_hash", string(hashedPassword))
	database.DeleteSetting("admin_reset_token")
	database.DeleteSetting("admin_reset_expiry")

	// Clear admin sessions
	adminUser := database.GetSetting("admin_username")
	if adminUser != "" {
		database.DB.Exec("DELETE FROM sessions WHERE username = ?", adminUser)
	}
	database.LogAuditFromCtx(c, adminUser, database.AuditActionAdminReset, "", database.AuditStatusOK)

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
