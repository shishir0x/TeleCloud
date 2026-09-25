package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"telecloud/tgclient"
	"telecloud/utils"
	"time"

	"github.com/gin-gonic/gin"
)

// ytdlpFormatIDRe matches valid yt-dlp format selectors (e.g. "137", "137+140",
// "bestvideo[height<=720]", "bv*+ba/b") while excluding leading dashes so the
// value can never be parsed as a yt-dlp option flag.
var ytdlpFormatIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-+/\[\]<>=.*,]*$`)

func (h *Handler) handleGetYTDLPStatus(c *gin.Context) {
	ytdlpEnabled := h.cfg.YTDLPPath != "disabled" && h.cfg.YTDLPPath != "disable"
	ffmpegEnabled := h.cfg.FFMPEGPath != "disabled" && h.cfg.FFMPEGPath != "disable"
	enabled := ytdlpEnabled && ffmpegEnabled
	c.JSON(http.StatusOK, gin.H{"enabled": enabled})
}

func (h *Handler) handleGetYTDLPCookiesStatus(c *gin.Context) {
	username := c.GetString("username")
	activeCookie := tgclient.GetActiveCookieFile(h.cfg, username)
	c.JSON(http.StatusOK, gin.H{"has_cookie": activeCookie != ""})
}

type jsonCookieItem struct {
	Domain         string          `json:"domain"`
	ExpirationDate json.RawMessage `json:"expirationDate"`
	HostOnly       *bool           `json:"hostOnly"`
	HttpOnly       *bool           `json:"httpOnly"`
	Name           string          `json:"name"`
	Path           string          `json:"path"`
	Secure         *bool           `json:"secure"`
	Session        *bool           `json:"session"`
	Value          string          `json:"value"`
}

func convertJSONToNetscapeCookies(data []byte) ([]byte, error) {
	var cookies []jsonCookieItem
	if err := json.Unmarshal(data, &cookies); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString("# Netscape HTTP Cookie File\n")
	buf.WriteString("# http://curl.haxx.se/rfc/cookie_spec.html\n")
	buf.WriteString("# Converted by TeleCloud from JSON format\n\n")

	defaultExp := time.Now().Add(365 * 24 * time.Hour).Unix()

	for _, c := range cookies {
		if c.Name == "" || c.Domain == "" {
			continue
		}

		domain := c.Domain
		if c.HttpOnly != nil && *c.HttpOnly {
			if !strings.HasPrefix(domain, "#HttpOnly_") {
				domain = "#HttpOnly_" + domain
			}
		}

		includeSubdomains := "FALSE"
		cleanDomain := strings.TrimPrefix(domain, "#HttpOnly_")
		if strings.HasPrefix(cleanDomain, ".") {
			includeSubdomains = "TRUE"
		} else if c.HostOnly != nil && !*c.HostOnly {
			includeSubdomains = "TRUE"
		}

		path := c.Path
		if path == "" {
			path = "/"
		}

		secure := "FALSE"
		if c.Secure != nil && *c.Secure {
			secure = "TRUE"
		}

		exp := defaultExp
		if len(c.ExpirationDate) > 0 {
			expStr := strings.TrimSpace(string(c.ExpirationDate))
			if f, err := strconv.ParseFloat(expStr, 64); err == nil && f > 0 {
				exp = int64(f)
			}
		}

		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			domain,
			includeSubdomains,
			path,
			secure,
			exp,
			c.Name,
			c.Value,
		)
		buf.WriteString(line)
	}

	return buf.Bytes(), nil
}

func (h *Handler) handlePostYTDLPCookies(c *gin.Context) {
	file, err := c.FormFile("cookie_file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_required"})
		return
	}

	if file.Size > 5*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_too_large"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "open_failed"})
		return
	}
	defer src.Close()

	data, err := io.ReadAll(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read_failed"})
		return
	}

	headLen := len(data)
	if headLen > 200 {
		headLen = 200
	}
	headStr := string(data[:headLen])
	isNetscape := strings.Contains(headStr, "# Netscape HTTP Cookie File") || strings.Contains(headStr, "# HTTP Cookie File")
	isJSON := strings.HasPrefix(strings.TrimSpace(headStr), "[")

	var finalData []byte
	if isJSON {
		converted, err := convertJSONToNetscapeCookies(data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_cookie_format"})
			return
		}
		finalData = converted
	} else if isNetscape {
		finalData = data
	} else {
		// Check if it's tab-separated netscape data without standard comment header
		trimmed := strings.TrimSpace(string(data))
		lines := strings.Split(trimmed, "\n")
		hasTabs := false
		for _, l := range lines {
			if strings.Count(l, "\t") >= 5 {
				hasTabs = true
				break
			}
		}
		if hasTabs {
			var b bytes.Buffer
			b.WriteString("# Netscape HTTP Cookie File\n# http://curl.haxx.se/rfc/cookie_spec.html\n\n")
			b.Write(data)
			finalData = b.Bytes()
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_cookie_format"})
			return
		}
	}

	if err := os.MkdirAll(h.cfg.CookiesDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}

	username := c.GetString("username")
	// 1. Save user-specific cookie file
	if username != "" {
		cookieFile := filepath.Join(h.cfg.CookiesDir, fmt.Sprintf("user_%s.txt", username))
		if err := os.WriteFile(cookieFile, finalData, 0600); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
			return
		}
	}

	// 2. Also save as global cookies.txt so the mobile app and all sessions inherit it immediately
	globalCookieFile := filepath.Join(h.cfg.CookiesDir, "cookies.txt")
	if err := os.WriteFile(globalCookieFile, finalData, 0600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}

	// 3. If username is admin or empty, also update user_admin.txt
	if username == "admin" || username == "" {
		adminCookieFile := filepath.Join(h.cfg.CookiesDir, "user_admin.txt")
		_ = os.WriteFile(adminCookieFile, finalData, 0600)
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *Handler) handleDeleteYTDLPCookies(c *gin.Context) {
	username := c.GetString("username")
	if username != "" {
		cookieFile := filepath.Join(h.cfg.CookiesDir, fmt.Sprintf("user_%s.txt", username))
		os.Remove(cookieFile)
	}

	// When admin deletes cookies, also remove global cookies.txt and user_admin.txt
	if username == "admin" || username == "" {
		os.Remove(filepath.Join(h.cfg.CookiesDir, "cookies.txt"))
		os.Remove(filepath.Join(h.cfg.CookiesDir, "user_admin.txt"))
		os.Remove(filepath.Join(h.cfg.CookiesDir, "youtube.txt"))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *Handler) handleGetProxyImage(c *gin.Context) {
	targetURL := c.Query("url")
	if targetURL == "" {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if !tgclient.IsValidURL(targetURL) {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", targetURL, nil)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	if u, err := url.Parse(targetURL); err == nil {
		req.Header.Set("Referer", u.Scheme+"://"+u.Host+"/")
	}

	resp, err := client.Do(req)
	if err != nil {
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.AbortWithStatus(resp.StatusCode)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "image/") && contentType != "application/octet-stream" {
		if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/json") {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}

	if contentType == "" {
		contentType = "image/jpeg"
	}

	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "public, max-age=86400")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	io.Copy(c.Writer, resp.Body)
}

func (h *Handler) handlePostYTDLPFormats(c *gin.Context) {
	url := strings.TrimSpace(c.PostForm("url"))
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url_required"})
		return
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && strings.Contains(url, ".") {
		url = "https://" + url
	}
	if !tgclient.IsValidURL(url) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_url_format"})
		return
	}
	if utils.IsPrivateIP(url) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden_url"})
		return
	}
	username := c.GetString("username")
	info, err := tgclient.GetYTDLPFormats(url, h.cfg, username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

func (h *Handler) handlePostYTDLPDownload(c *gin.Context) {
	url := strings.TrimSpace(c.PostForm("url"))
	formatID := c.PostForm("format_id")
	downloadType := c.PostForm("download_type")
	path := c.PostForm("path")

	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url_required"})
		return
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && strings.Contains(url, ".") {
		url = "https://" + url
	}
	if !tgclient.IsValidURL(url) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_url_format"})
		return
	}
	if utils.IsPrivateIP(url) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden_url"})
		return
	}
	if path == "" {
		path = "/"
	}

	username := c.GetString("username")
	isAdmin := c.GetBool("is_admin")
	dbPath := mapPath(path, username, isAdmin)

	if isAdmin && isChildAccountPath(dbPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin_forbidden_child_path"})
		return
	}

	taskID := c.PostForm("task_id")
	if taskID == "" {
		taskID = fmt.Sprintf("ytdlp_%d", time.Now().UnixNano())
	}
	// taskID is embedded in temp file names — reject traversal characters
	if strings.Contains(taskID, "..") || strings.ContainsAny(taskID, "/\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_task_id"})
		return
	}
	// formatID is passed to yt-dlp `-f`; restrict to the safe charset yt-dlp
	// format selectors use, so it can never be interpreted as an option flag.
	if formatID != "" && !ytdlpFormatIDRe.MatchString(formatID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_format_id"})
		return
	}
	go tgclient.ProcessYTDLPUpload(context.Background(), url, formatID, dbPath, taskID, downloadType, h.cfg, username)

	c.JSON(http.StatusOK, gin.H{"status": "started", "task_id": taskID})
}
