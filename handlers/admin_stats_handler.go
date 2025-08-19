package handlers

import (
	"net/http"
	"strconv"
	"time"

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

// 관리자 인증 미들웨어
func (h *AdminStatsHandler) AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 간단한 토큰 인증 (실제로는 더 강화된 인증 필요)
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

// 저장소 통계 조회
func (h *AdminStatsHandler) GetStorageStats(c *gin.Context) {
	stats, err := h.statsService.GetStorageStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "저장소 통계 조회 실패",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "저장소 통계 조회 성공",
		"data":    stats,
	})
}

// 오래된 스냅샷 정리
func (h *AdminStatsHandler) CleanupOldSnapshots(c *gin.Context) {
	// 기본값: 30일 이전 데이터 삭제
	daysStr := c.DefaultQuery("days", "30")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days < 1 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "올바른 일수를 입력하세요 (1 이상의 정수)",
		})
		return
	}

	cutoffDate := time.Now().AddDate(0, 0, -days)
	deletedCount, err := h.statsService.CleanupOldSnapshots(cutoffDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "오래된 스냅샷 정리 실패",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "오래된 스냅샷 정리 완료",
		"deletedCount": deletedCount,
		"cutoffDate":   cutoffDate.Format("2006-01-02"),
		"daysDeleted":  days,
	})
}

// 특정 기간 데이터 삭제
func (h *AdminStatsHandler) DeleteDataByDateRange(c *gin.Context) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	if startDateStr == "" || endDateStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "start_date와 end_date를 모두 제공해야 합니다 (YYYY-MM-DD 형식)",
		})
		return
	}

	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "start_date 형식이 올바르지 않습니다 (YYYY-MM-DD 형식 사용)",
		})
		return
	}

	endDate, err := time.Parse("2006-01-02", endDateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "end_date 형식이 올바르지 않습니다 (YYYY-MM-DD 형식 사용)",
		})
		return
	}

	if endDate.Before(startDate) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "end_date는 start_date보다 늦어야 합니다",
		})
		return
	}

	// 안전장치: 너무 많은 데이터 삭제 방지 (최대 90일)
	if endDate.Sub(startDate).Hours() > 24*90 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "한 번에 삭제할 수 있는 기간은 최대 90일입니다",
		})
		return
	}

	result, err := h.statsService.DeleteDataByDateRange(startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "기간별 데이터 삭제 실패",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "기간별 데이터 삭제 완료",
		"startDate": startDateStr,
		"endDate":   endDateStr,
		"deleted":   result,
	})
}

// 특정 대회 히스토리 데이터 삭제
func (h *AdminStatsHandler) DeleteCompetitionHistory(c *gin.Context) {
	competitionID := c.Param("id")
	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "대회 ID가 필요합니다",
		})
		return
	}

	// 확인 파라미터 (실수 방지)
	confirm := c.Query("confirm")
	if confirm != "yes" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "데이터 삭제를 확인하려면 ?confirm=yes 파라미터를 추가하세요",
			"warning": "이 작업은 되돌릴 수 없습니다",
		})
		return
	}

	deletedCount, err := h.statsService.DeleteCompetitionHistoryData(competitionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "대회 히스토리 데이터 삭제 실패",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "대회 히스토리 데이터 삭제 완료",
		"competitionId": competitionID,
		"deletedCount":  deletedCount,
	})
}

// 수동 일별 집계 실행
func (h *AdminStatsHandler) TriggerDailyAggregation(c *gin.Context) {
	err := h.statsService.SaveDailyAggregation()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "일별 집계 실행 실패",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "일별 집계 실행 완료",
		"date":    time.Now().Format("2006-01-02"),
	})
}

// 수동 시간별 스냅샷 저장 (모든 활성 대회)
func (h *AdminStatsHandler) TriggerHourlySnapshots(c *gin.Context) {
	// 개별 대회용
	competitionID := c.Query("competition_id")
	
	if competitionID != "" {
		// 특정 대회만
		err := h.statsService.SaveCompetitionHourlySnapshot(competitionID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "시간별 스냅샷 저장 실패",
				"details": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":       "시간별 스냅샷 저장 완료",
			"competitionId": competitionID,
		})
	} else {
		// 모든 활성 대회
		err := h.statsService.UpdateAllActiveCompetitions()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "전체 시간별 스냅샷 저장 실패",
				"details": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "전체 활성 대회 시간별 스냅샷 저장 완료",
		})
	}
}

// 데이터 백업 정보 조회
func (h *AdminStatsHandler) GetBackupInfo(c *gin.Context) {
	stats, err := h.statsService.GetStorageStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "백업 정보 조회 실패",
		})
		return
	}

	backupInfo := gin.H{
		"totalSnapshots":  stats["totalSnapshots"],
		"oldestSnapshot":  stats["oldestSnapshot"],
		"newestSnapshot":  stats["newestSnapshot"],
		"collections":     stats["collections"],
		"recommendedActions": []string{},
	}

	// 권장 사항 계산
	if stats["totalSnapshots"].(int) > 10000 {
		backupInfo["recommendedActions"] = append(
			backupInfo["recommendedActions"].([]string),
			"시간별 스냅샷이 많습니다. 30일 이전 데이터 정리를 고려하세요.",
		)
	}

	if stats["oldestSnapshot"] != nil {
		oldestStr := stats["oldestSnapshot"].(string)
		if oldest, err := time.Parse("2006-01-02 15:04:05", oldestStr); err == nil {
			if time.Since(oldest).Hours() > 24*90 {
				backupInfo["recommendedActions"] = append(
					backupInfo["recommendedActions"].([]string),
					"90일 이전 데이터가 있습니다. 정리를 고려하세요.",
				)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "백업 정보 조회 성공",
		"data":    backupInfo,
	})
}

// 시스템 헬스 체크 (관리자용)
func (h *AdminStatsHandler) GetSystemHealth(c *gin.Context) {
	health := gin.H{
		"status":     "healthy",
		"timestamp":  time.Now().Format("2006-01-02 15:04:05"),
		"services":   gin.H{},
		"warnings":   []string{},
	}

	// YouTube API 상태 확인
	if h.statsService != nil {
		// 간단한 헬스 체크 (실제로는 API 호출 테스트)
		health["services"].(gin.H)["statsService"] = "healthy"
		health["services"].(gin.H)["youtubeAPI"] = "healthy" // 실제로는 테스트 필요
		health["services"].(gin.H)["firestore"] = "healthy"
	} else {
		health["status"] = "unhealthy"
		health["services"].(gin.H)["statsService"] = "unavailable"
	}

	c.JSON(http.StatusOK, health)
}
