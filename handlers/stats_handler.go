package handlers

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	
	"adfit-oauth/services"
)

type StatsHandler struct {
	statsService *services.StatsService
}

func NewStatsHandler() (*StatsHandler, error) {
	statsService, err := services.NewStatsService()
	if err != nil {
		return nil, err
	}

	return &StatsHandler{
		statsService: statsService,
	}, nil
}

// validateToken - 토큰 검증
func validateToken(authHeader string) bool {
	expectedToken := os.Getenv("STATS_UPDATE_TOKEN")
	if expectedToken == "" {
		// 기본 토큰 (운영 환경에서는 반드시 환경변수 설정)
		expectedToken = "7kP9mN3xQ8vL2wR5tY6uA1bC4dE0fG9h"
	}

	// Bearer 토큰 검증
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}

	providedToken := strings.TrimPrefix(authHeader, "Bearer ")
	return providedToken == expectedToken
}

// UpdateAllActiveCompetitions - 모든 활성 대회 통계 업데이트
func (h *StatsHandler) UpdateAllActiveCompetitions(c *gin.Context) {
	// 토큰 검증
	token := c.GetHeader("Authorization")
	if !validateToken(token) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized",
		})
		return
	}

	err := h.statsService.UpdateAllActiveCompetitions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "통계 업데이트 실패",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "모든 활성 대회 통계 업데이트 완료",
		"status":  "success",
	})
}

// UpdateCompetitionStats - 특정 대회 통계 업데이트
func (h *StatsHandler) UpdateCompetitionStats(c *gin.Context) {
	competitionID := c.Param("id")
	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "대회 ID가 필요합니다",
		})
		return
	}

	// 토큰 검증
	token := c.GetHeader("Authorization")
	if !validateToken(token) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized",
		})
		return
	}

	err := h.statsService.UpdateCompetitionStats(competitionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "대회 통계 업데이트 실패",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "대회 통계 업데이트 완료",
		"competitionId": competitionID,
		"status":        "success",
	})
}

// GetStatsStatus - 통계 서비스 상태 체크
func (h *StatsHandler) GetStatsStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "AdFit 통계 서비스 정상 동작 중",
		"status":  "healthy",
		"time":    gin.H{
			"server": "running",
		},
	})
}
