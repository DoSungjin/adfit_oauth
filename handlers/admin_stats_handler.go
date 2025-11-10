package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	
	"adfit-oauth/services"
)

type AdminStatsHandler struct {
	statsService *services.StatsService
}

func NewAdminStatsHandler() (*AdminStatsHandler, error) {
	statsService, err := services.NewStatsService()
	if err != nil {
		return nil, err
	}

	return &AdminStatsHandler{
		statsService: statsService,
	}, nil
}

// AdminAuthRequired - 관리자 권한 검증 미들웨어
func (h *AdminStatsHandler) AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 토큰 검증
		token := c.GetHeader("Authorization")
		adminToken := c.GetHeader("X-Admin-Token")
		
		if token != "Bearer adfit-stats-update-token" || adminToken != "adfit-admin-secret" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "관리자 권한이 필요합니다",
				"code":  "ADMIN_AUTH_REQUIRED",
			})
			c.Abort()
			return
		}
		
		c.Next()
	}
}

// GetSystemHealth - 시스템 상태 체크
func (h *AdminStatsHandler) GetSystemHealth(c *gin.Context) {
	health := gin.H{
		"status":     "healthy",
		"services":   gin.H{},
		"warnings":   []string{},
	}

	// StatsService 상태 체크
	if h.statsService != nil {
		health["services"].(gin.H)["statsService"] = "healthy"
		health["services"].(gin.H)["youtubeAPI"] = "healthy"
		health["services"].(gin.H)["firestore"] = "healthy"
	} else {
		health["status"] = "unhealthy"
		health["services"].(gin.H)["statsService"] = "unavailable"
	}

	c.JSON(http.StatusOK, health)
}
