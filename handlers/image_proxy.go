package handlers

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
)

var imageProxyClient = &http.Client{Timeout: 10 * time.Second}

// imageProxyAllowedHosts - SSRF 방지: 허용된 이미지 CDN 도메인만 프록시
var imageProxyAllowedHosts = map[string]bool{
	"yt3.ggpht.com":              true,
	"yt3.googleusercontent.com": true,
	"i.ytimg.com":                true,
	"lh3.googleusercontent.com": true,
	"pbs.twimg.com":              true,
	"p16-sign.tiktokcdn-us.com": true,
	"p77-sign.tiktokcdn.com":    true,
}

// ImageProxy - 외부 이미지 CORS 우회 프록시 (허용 도메인 한정)
// GET /api/image/proxy?url=ENCODED_URL
func ImageProxy(c *gin.Context) {
	rawURL := c.Query("url")
	if rawURL == "" {
		c.Status(http.StatusBadRequest)
		return
	}

	// URL 유효성 검사
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Scheme != "https" {
		c.Status(http.StatusBadRequest)
		return
	}

	// SSRF 방지: 허용된 도메인만 통과
	if !imageProxyAllowedHosts[parsed.Hostname()] {
		c.Status(http.StatusForbidden)
		return
	}

	resp, err := imageProxyClient.Get(rawURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		c.Status(http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}

	c.Header("Cache-Control", "public, max-age=86400")
	c.Header("Access-Control-Allow-Origin", "https://adtown.ai")
	c.DataFromReader(http.StatusOK, resp.ContentLength, contentType, resp.Body, nil)
}

// UpdateYouTubeImages - YouTube API로 profile_image 업데이트
// POST /api/admin/creators/update-youtube-images
func UpdateYouTubeImages(c *gin.Context) {
	// 이 엔드포인트는 Python 스크립트로 대체하므로 stub
	c.JSON(http.StatusOK, gin.H{"message": "Use Python script for bulk update"})
}
