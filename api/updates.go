package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"telecloud/database"
)

// ReleaseInfo represents sanitized public release metadata.
type ReleaseInfo struct {
	Tag  string `json:"tag"`
	Name string `json:"name"`
	Body string `json:"body"`
	URL  string `json:"url"`
	Date string `json:"date"`
}

// UpdateCheckResponse represents the response returned by GET /api/updates.
type UpdateCheckResponse struct {
	Enabled          bool          `json:"enabled"`
	UpdateAvailable  bool          `json:"update_available"`
	CurrentVersion   string        `json:"current_version"`
	LatestVersion    string        `json:"latest_version,omitempty"`
	LatestReleaseURL string        `json:"latest_release_url,omitempty"`
	Changelog        []ReleaseInfo `json:"changelog,omitempty"`
	CheckedAt        string        `json:"checked_at,omitempty"`
	Cached           bool          `json:"cached"`
	Error            string        `json:"error,omitempty"`
}

// UpdateChecker manages querying GitHub releases with a 24-hour cache.
type UpdateChecker struct {
	mu          sync.RWMutex
	lastChecked time.Time
	cachedResp  *UpdateCheckResponse
	ttl         time.Duration
	client      *http.Client
	fetchMu     sync.Mutex // Serializes upstream checks to prevent thundering herd
}

// NewUpdateChecker creates a new UpdateChecker instance.
func NewUpdateChecker(ttl time.Duration) *UpdateChecker {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &UpdateChecker{
		ttl: ttl,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// SetHTTPClient allows injecting a custom HTTP client (useful for testing).
func (uc *UpdateChecker) SetHTTPClient(client *http.Client) {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	uc.client = client
}

// Check queries GitHub or returns the cached response.
func (uc *UpdateChecker) Check(repo, currentVersion string, force bool) *UpdateCheckResponse {
	// 1. Check in-memory cache under read lock (unless forced)
	uc.mu.RLock()
	if !force && uc.cachedResp != nil && time.Since(uc.lastChecked) < uc.ttl {
		resp := *uc.cachedResp
		resp.Cached = true
		uc.mu.RUnlock()
		return &resp
	}
	uc.mu.RUnlock()

	// 2. Lock fetchMu so concurrent requests don't all hit GitHub simultaneously
	uc.fetchMu.Lock()
	defer uc.fetchMu.Unlock()

	// Recheck cache in case another goroutine fetched while waiting for lock
	uc.mu.RLock()
	if !force && uc.cachedResp != nil && time.Since(uc.lastChecked) < uc.ttl {
		resp := *uc.cachedResp
		resp.Cached = true
		uc.mu.RUnlock()
		return &resp
	}
	uc.mu.RUnlock()

	if repo == "" {
		repo = "shishir0x/TeleCloud"
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases", repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return uc.fallbackOrError(currentVersion, "failed to create request: "+err.Error())
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "TeleCloud-Update-Checker")

	uc.mu.RLock()
	httpClient := uc.client
	uc.mu.RUnlock()

	httpResp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[UPDATE CHECKER] Network failure or timeout: %v", err)
		return uc.fallbackOrError(currentVersion, "update check unavailable (offline or timeout)")
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		log.Printf("[UPDATE CHECKER] GitHub API returned status: %d", httpResp.StatusCode)
		return uc.fallbackOrError(currentVersion, fmt.Sprintf("GitHub API returned status %d", httpResp.StatusCode))
	}

	var ghReleases []struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}

	if err := json.NewDecoder(httpResp.Body).Decode(&ghReleases); err != nil {
		return uc.fallbackOrError(currentVersion, "failed to decode GitHub releases: "+err.Error())
	}

	if len(ghReleases) == 0 {
		resp := &UpdateCheckResponse{
			Enabled:         true,
			UpdateAvailable: false,
			CurrentVersion:  currentVersion,
			CheckedAt:       time.Now().UTC().Format(time.RFC3339),
			Cached:          false,
		}
		uc.updateCache(resp)
		return resp
	}

	latest := ghReleases[0]
	isNewer := compareVersions(latest.TagName, currentVersion) == 1

	var changelog []ReleaseInfo
	maxReleases := 5
	if len(ghReleases) < maxReleases {
		maxReleases = len(ghReleases)
	}
	for i := 0; i < maxReleases; i++ {
		r := ghReleases[i]
		changelog = append(changelog, ReleaseInfo{
			Tag:  r.TagName,
			Name: r.Name,
			Body: r.Body,
			URL:  r.HTMLURL,
			Date: r.PublishedAt,
		})
	}

	resp := &UpdateCheckResponse{
		Enabled:          true,
		UpdateAvailable:  isNewer,
		CurrentVersion:   currentVersion,
		LatestVersion:    latest.TagName,
		LatestReleaseURL: latest.HTMLURL,
		Changelog:        changelog,
		CheckedAt:        time.Now().UTC().Format(time.RFC3339),
		Cached:           false,
	}

	uc.updateCache(resp)
	return resp
}

func (uc *UpdateChecker) updateCache(resp *UpdateCheckResponse) {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	uc.lastChecked = time.Now()
	cached := *resp
	uc.cachedResp = &cached
}

func (uc *UpdateChecker) fallbackOrError(currentVersion, errMsg string) *UpdateCheckResponse {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	if uc.cachedResp != nil {
		resp := *uc.cachedResp
		resp.Cached = true
		resp.Error = errMsg
		return &resp
	}
	return &UpdateCheckResponse{
		Enabled:         true,
		UpdateAvailable: false,
		CurrentVersion:  currentVersion,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		Cached:          false,
		Error:           errMsg,
	}
}

// compareVersions compares v1 and v2 semver strings.
// Returns 1 if v1 > v2, -1 if v1 < v2, and 0 if equal.
func compareVersions(v1, v2 string) int {
	clean := func(v string) []int {
		v = strings.TrimPrefix(v, "v")
		parts := strings.Split(v, "-")
		segments := strings.Split(parts[0], ".")
		var nums []int
		for _, s := range segments {
			n, _ := strconv.Atoi(s)
			nums = append(nums, n)
		}
		return nums
	}

	p1 := clean(v1)
	p2 := clean(v2)
	maxLen := len(p1)
	if len(p2) > maxLen {
		maxLen = len(p2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(p1) {
			n1 = p1[i]
		}
		if i < len(p2) {
			n2 = p2[i]
		}
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}
	return 0
}

// handleGetUpdates handles GET /api/updates.
func (h *Handler) handleGetUpdates(c *gin.Context) {
	enabled := database.GetSetting("check_updates_enabled") == "true"
	currentVersion := ""
	if h.cfg != nil {
		currentVersion = h.cfg.Version
	}
	if currentVersion == "" {
		currentVersion = "v1.0.0"
	}

	if !enabled {
		c.JSON(http.StatusOK, gin.H{
			"enabled":          false,
			"update_available": false,
			"current_version":  currentVersion,
		})
		return
	}

	force := c.Query("force") == "true" && c.GetBool("is_admin")
	repo := database.GetSetting("github_repo")
	if repo == "" {
		repo = "shishir0x/TeleCloud"
	}

	if h.updateChecker == nil {
		h.updateChecker = NewUpdateChecker(24 * time.Hour)
	}

	resp := h.updateChecker.Check(repo, currentVersion, force)
	c.JSON(http.StatusOK, resp)
}

// handlePostUpdateSettings handles POST /api/settings/updates (admin only).
func (h *Handler) handlePostUpdateSettings(c *gin.Context) {
	if !c.GetBool("is_admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	enabled := c.PostForm("enabled")
	if enabled == "true" {
		database.SetSetting("check_updates_enabled", "true")
	} else {
		database.SetSetting("check_updates_enabled", "false")
	}

	database.LogAuditFromCtx(c, c.GetString("username"), database.AuditActionSetupConfig, "check_updates_enabled="+enabled, database.AuditStatusOK)
	c.JSON(http.StatusOK, gin.H{"status": "success", "enabled": enabled == "true"})
}
