package handlers

import (
	"net/http"

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

// 모든 활성 대회 통계 업데이트 (수동 트리거)
func (h *StatsHandler) UpdateAllActiveCompetitions(c *gin.Context) {
	// 인증 확인 (간단한 토큰 방식)
	token := c.GetHeader("Authorization")
	if token != "Bearer adfit-stats-update-token" {
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

// 특정 대회 통계 업데이트
func (h *StatsHandler) UpdateCompetitionStats(c *gin.Context) {
	competitionID := c.Param("id")
	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "대회 ID가 필요합니다",
		})
		return
	}

	// 인증 확인
	token := c.GetHeader("Authorization")
	if token != "Bearer adfit-stats-update-token" {
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

// 통계 업데이트 상태 확인
func (h *StatsHandler) GetStatsStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "AdFit 통계 서비스 정상 작동 중",
		"status":  "healthy",
		"time":    gin.H{
			"server": "running",
		},
	})
}
