package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"adfit-oauth/services"
)

type YouTubeTrendHandler struct {
	trendService *services.YouTubeTrendService
}

func NewYouTubeTrendHandler() (*YouTubeTrendHandler, error) {
	trendService, err := services.NewYouTubeTrendService()
	if err != nil {
		return nil, err
	}

	return &YouTubeTrendHandler{
		trendService: trendService,
	}, nil
}

// Search - 키워드 검색
// GET /api/youtube-trend/search?q=키워드&max=20&order=relevance
func (h *YouTubeTrendHandler) Search(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "검색어(q)를 입력하세요"})
		return
	}

	maxResults := parseIntParam(c.Query("max"), 20)
	order := c.DefaultQuery("order", "relevance")

	videos, err := h.trendService.SearchVideos(query, int64(maxResults), order)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"query":   query,
		"count":   len(videos),
		"videos":  videos,
	})
}

// Trending - 트렌딩 영상
// GET /api/youtube-trend/trending?region=KR&category=20&max=20
func (h *YouTubeTrendHandler) Trending(c *gin.Context) {
	regionCode := c.DefaultQuery("region", "KR")
	categoryID := c.DefaultQuery("category", "0")
	maxResults := parseIntParam(c.Query("max"), 20)

	videos, err := h.trendService.GetTrendingVideos(regionCode, categoryID, int64(maxResults))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"region":   regionCode,
		"category": categoryID,
		"count":    len(videos),
		"videos":   videos,
	})
}

// TopicAnalysis - 주제 분석
// GET /api/youtube-trend/topic?keyword=먹방&max=20&order=viewCount
func (h *YouTubeTrendHandler) TopicAnalysis(c *gin.Context) {
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "키워드(keyword)를 입력하세요"})
		return
	}

	maxResults := parseIntParam(c.Query("max"), 20)
	order := c.DefaultQuery("order", "viewCount") // relevance, viewCount, date

	analysis, err := h.trendService.GetTopicAnalysis(keyword, int64(maxResults), order)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"analysis": analysis,
	})
}

// Viral - 바이럴 영상 찾기
// GET /api/youtube-trend/viral?region=KR&period=week&max=20
func (h *YouTubeTrendHandler) Viral(c *gin.Context) {
	regionCode := c.DefaultQuery("region", "KR")
	period := c.DefaultQuery("period", "week") // week, month, year
	maxResults := parseIntParam(c.Query("max"), 20)

	videos, err := h.trendService.GetViralVideos(regionCode, period, int64(maxResults))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"region":  regionCode,
		"period":  period,
		"count":   len(videos),
		"videos":  videos,
	})
}

// 파라미터 파싱 헬퍼
func parseIntParam(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(s)
	if err != nil || val <= 0 {
		return defaultVal
	}
	return val
}
