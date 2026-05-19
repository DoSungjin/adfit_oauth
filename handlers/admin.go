package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"adfit-oauth/services"
)

type AdminStatsHandler struct {
	statsService *services.StatsService
	mockService  *services.MockDataService
}

func NewAdminStatsHandler() (*AdminStatsHandler, error) {
	statsService, err := services.NewStatsService()
	if err != nil {
		return nil, err
	}

	mockService, mockErr := services.NewMockDataService()
	if mockErr != nil {
		log.Printf("⚠️ MockDataService 초기화 실패: %v", mockErr)
	}

	return &AdminStatsHandler{
		statsService: statsService,
		mockService:  mockService,
	}, nil
}

// AdminAuthRequired - Admin 인증 미들웨어
func (h *AdminStatsHandler) AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Authorization 헤더 확인
		authHeader := c.GetHeader("Authorization")
		adminToken := c.GetHeader("X-Admin-Token")

		// 간단한 토큰 검증 (실제로는 더 복잡한 검증 필요)
		validAuthToken := "Bearer adfit-stats-update-token"
		validAdminToken := "adfit-admin-secret"

		if authHeader != validAuthToken || adminToken != validAdminToken {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: Invalid admin credentials",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetSystemHealth - 시스템 상태 확인
func (h *AdminStatsHandler) GetSystemHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"service": "admin-stats",
		"message": "Admin stats service is running",
	})
}

// CalculateWinners - 특정 대회의 수상자 계산
func (h *AdminStatsHandler) CalculateWinners(c *gin.Context) {
	competitionID := c.Param("competitionId")

	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "대회 ID가 필요합니다",
		})
		return
	}

	log.Printf("🏆 Admin 요청: 대회 수상자 계산 시작 - %s", competitionID)

	// StatsService의 FinalizeCompetition 메서드 호출
	err := h.statsService.FinalizeCompetition(competitionID)

	if err != nil {
		log.Printf("❌ 수상자 계산 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "수상자 계산에 실패했습니다",
			"error":   err.Error(),
		})
		return
	}

	log.Printf("✅ 수상자 계산 완료: %s", competitionID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "수상자가 성공적으로 계산되었습니다",
		"competitionId": competitionID,
	})
}

// GenerateReport - 대회 리포트 생성 API
func (h *AdminStatsHandler) GenerateReport(c *gin.Context) {
	competitionID := c.Param("competitionId")

	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "대회 ID가 필요합니다",
		})
		return
	}

	log.Printf("📊 Admin 요청: 대회 리포트 생성 - %s", competitionID)

	// 리포트 생성
	err := h.statsService.GenerateCompetitionReport(competitionID)

	if err != nil {
		log.Printf("❌ 리포트 생성 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "리포트 생성 실패",
			"error":   err.Error(),
		})
		return
	}

	log.Printf("✅ 리포트 생성 완료: %s", competitionID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "리포트가 성공적으로 생성되었습니다",
		"competitionId": competitionID,
	})
}

// UpdateAllCompetitionStats - ⭐ 모든 활성 대회 통계 즉시 업데이트 (수동 트리거)
func (h *AdminStatsHandler) UpdateAllCompetitionStats(c *gin.Context) {
	log.Println("🔄 Admin 요청: 모든 활성 대회 통계 즉시 업데이트 시작")

	// StatsService의 UpdateAllActiveCompetitions 메서드 호출
	err := h.statsService.UpdateAllActiveCompetitions()

	if err != nil {
		log.Printf("❌ 통계 업데이트 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "통계 업데이트 실패",
			"error":   err.Error(),
		})
		return
	}

	log.Println("✅ 모든 활성 대회 통계 업데이트 완료")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "모든 활성 대회 통계가 성공적으로 업데이트되었습니다",
	})
}

// UpdateSingleCompetitionStats - ⭐ 특정 대회 통계 즉시 업데이트 (수동 트리거)
func (h *AdminStatsHandler) UpdateSingleCompetitionStats(c *gin.Context) {
	competitionID := c.Param("competitionId")

	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "대회 ID가 필요합니다",
		})
		return
	}

	log.Printf("🔄 Admin 요청: 대회 통계 즉시 업데이트 - %s", competitionID)

	// StatsService의 UpdateCompetitionStats 메서드 호출
	err := h.statsService.UpdateCompetitionStats(competitionID)

	if err != nil {
		log.Printf("❌ 통계 업데이트 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "통계 업데이트 실패",
			"error":   err.Error(),
		})
		return
	}

	log.Printf("✅ 대회 통계 업데이트 완료: %s", competitionID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "대회 통계가 성공적으로 업데이트되었습니다",
		"competitionId": competitionID,
	})
}

// InjectMockReport - 테스트용 Mock 리포트 데이터 주입
func (h *AdminStatsHandler) InjectMockReport(c *gin.Context) {
	competitionID := c.Param("competitionId")
	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "대회 ID가 필요합니다"})
		return
	}
	if h.mockService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "MockDataService 미초기화"})
		return
	}

	log.Printf("🧪 Admin 요청: Mock 리포트 주입 - %s", competitionID)
	if err := h.mockService.InjectMockReport(c.Request.Context(), competitionID); err != nil {
		log.Printf("❌ Mock 주입 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Mock 주입 실패", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Mock 리포트 데이터가 주입되었습니다", "competitionId": competitionID})
}

// CleanupMockReport - Mock 리포트 데이터 정리 (isMockData=true 표시된 문서만)
func (h *AdminStatsHandler) CleanupMockReport(c *gin.Context) {
	competitionID := c.Param("competitionId")
	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "대회 ID가 필요합니다"})
		return
	}
	if h.mockService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "MockDataService 미초기화"})
		return
	}

	log.Printf("🧹 Admin 요청: Mock 리포트 정리 - %s", competitionID)
	if err := h.mockService.CleanupMockReport(c.Request.Context(), competitionID); err != nil {
		log.Printf("❌ Mock 정리 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Mock 정리 실패", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Mock 데이터가 정리되었습니다", "competitionId": competitionID})
}

// CheckMockData - 실데이터(non-mock) 존재 여부 확인
func (h *AdminStatsHandler) CheckMockData(c *gin.Context) {
	competitionID := c.Param("competitionId")
	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "대회 ID가 필요합니다"})
		return
	}
	if h.mockService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "MockDataService 미초기화"})
		return
	}

	hasReal, err := h.mockService.HasRealReportData(c.Request.Context(), competitionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "hasRealData": hasReal})
}

// CompletePrizePayment - ⭐ 상금 입금 완료 처리
func (h *AdminStatsHandler) CompletePrizePayment(c *gin.Context) {
	competitionID := c.Param("competitionId")
	userID := c.Param("userId")

	if competitionID == "" || userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "대회 ID와 사용자 ID가 필요합니다",
		})
		return
	}

	log.Printf("💰 Admin 요청: 입금 완료 처리 - Competition: %s, User: %s", competitionID, userID)

	// StatsService의 CompletePrizePayment 메서드 호출
	err := h.statsService.CompletePrizePayment(competitionID, userID)

	if err != nil {
		log.Printf("❌ 입금 완료 처리 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "입금 완료 처리 실패",
			"error":   err.Error(),
		})
		return
	}

	log.Printf("✅ 입금 완료 처리 성공: Competition: %s, User: %s", competitionID, userID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "입금이 완료 처리되었습니다",
		"competitionId": competitionID,
		"userId": userID,
	})
}
