package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"adfit-oauth/config"
)

// ReportHandler handles report-related operations
type ReportHandler struct {
	firestore *firestore.Client
}

// ReportData represents a report document
type ReportData struct {
	ID            string                 `json:"id" firestore:"-"`
	CompetitionID string                 `json:"competitionId" firestore:"competitionId"`
	SubmissionID  string                 `json:"submissionId" firestore:"submissionId"`
	CreatorID     string                 `json:"creatorId" firestore:"creatorId"`
	CreatorName   string                 `json:"creatorName" firestore:"creatorName"`
	Reason        string                 `json:"reason" firestore:"reason"`
	Detail        string                 `json:"detail" firestore:"detail"`
	ReportedAt    time.Time              `json:"reportedAt" firestore:"reportedAt"`
	ReportedBy    string                 `json:"reportedBy" firestore:"reportedBy"`
	Status        string                 `json:"status" firestore:"status"` // pending, reviewed, resolved, dismissed
	AdminNote     string                 `json:"adminNote" firestore:"adminNote"`
	ProcessedAt   *time.Time             `json:"processedAt,omitempty" firestore:"processedAt,omitempty"`
	ProcessedBy   string                 `json:"processedBy,omitempty" firestore:"processedBy,omitempty"`
	Action        string                 `json:"action,omitempty" firestore:"action,omitempty"` // warning, ban, none
	Competition   map[string]interface{} `json:"competition,omitempty" firestore:"-"`
}

// NewReportHandler creates a new report handler
func NewReportHandler() (*ReportHandler, error) {
	ctx := context.Background()

	var app *firebase.App
	var err error

	if config.Config != nil {
		if config.Config.Firebase.CredentialsPath != "" {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID: config.Config.Firebase.ProjectID,
			}, option.WithCredentialsFile(config.Config.Firebase.CredentialsPath))
		} else {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID: config.Config.Firebase.ProjectID,
			})
		}
	} else {
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID: "posted-app-c4ff5",
		})
	}

	if err != nil {
		return nil, err
	}

	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, err
	}

	return &ReportHandler{
		firestore: firestoreClient,
	}, nil
}

// GetAllReports retrieves all reports with optional filters
func (h *ReportHandler) GetAllReports(c *gin.Context) {
	ctx := context.Background()

	status := c.Query("status")  // pending, reviewed, resolved, dismissed
	limit := c.DefaultQuery("limit", "50")

	query := h.firestore.Collection("reports").OrderBy("reportedAt", firestore.Desc)

	// Status 필터
	if status != "" {
		query = query.Where("status", "==", status)
	}

	// Limit 설정
	if limit != "" {
		var limitInt int
		if _, err := fmt.Sscanf(limit, "%d", &limitInt); err == nil && limitInt > 0 {
			query = query.Limit(limitInt)
		}
	}

	iter := query.Documents(ctx)
	defer iter.Stop()

	reports := []ReportData{}
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error iterating reports: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "신고 목록 조회 실패",
				"error":   err.Error(),
			})
			return
		}

		var report ReportData
		if err := doc.DataTo(&report); err != nil {
			log.Printf("Error parsing report: %v", err)
			continue
		}

		report.ID = doc.Ref.ID

		// Competition 정보 추가
		if report.CompetitionID != "" {
			compDoc, err := h.firestore.Collection("competitions").Doc(report.CompetitionID).Get(ctx)
			if err == nil {
				report.Competition = compDoc.Data()
			}
		}

		reports = append(reports, report)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"reports": reports,
		"total":   len(reports),
	})
}

// GetReportsByCompetition retrieves reports for a specific competition
func (h *ReportHandler) GetReportsByCompetition(c *gin.Context) {
	ctx := context.Background()
	competitionID := c.Param("competitionId")

	if competitionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "대회 ID가 필요합니다",
		})
		return
	}

	iter := h.firestore.Collection("reports").
		Where("competitionId", "==", competitionID).
		OrderBy("reportedAt", firestore.Desc).
		Documents(ctx)
	defer iter.Stop()

	reports := []ReportData{}
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error iterating reports: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "신고 목록 조회 실패",
				"error":   err.Error(),
			})
			return
		}

		var report ReportData
		if err := doc.DataTo(&report); err != nil {
			log.Printf("Error parsing report: %v", err)
			continue
		}

		report.ID = doc.Ref.ID
		reports = append(reports, report)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"reports": reports,
		"total":   len(reports),
	})
}

// UpdateReportStatus updates the status of a report
func (h *ReportHandler) UpdateReportStatus(c *gin.Context) {
	ctx := context.Background()
	reportID := c.Param("reportId")

	if reportID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "신고 ID가 필요합니다",
		})
		return
	}

	var updateData struct {
		Status      string `json:"status" binding:"required"` // reviewed, resolved, dismissed
		AdminNote   string `json:"adminNote"`
		ProcessedBy string `json:"processedBy"`
		Action      string `json:"action"` // warning, ban, none
	}

	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "잘못된 요청 데이터",
			"error":   err.Error(),
		})
		return
	}

	// 유효한 상태 확인
	validStatuses := map[string]bool{
		"pending":   true,
		"reviewed":  true,
		"resolved":  true,
		"dismissed": true,
	}

	if !validStatuses[updateData.Status] {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "유효하지 않은 상태값입니다",
		})
		return
	}

	now := time.Now()
	updates := []firestore.Update{
		{Path: "status", Value: updateData.Status},
		{Path: "processedAt", Value: now},
		{Path: "updatedAt", Value: now},
	}

	if updateData.AdminNote != "" {
		updates = append(updates, firestore.Update{Path: "adminNote", Value: updateData.AdminNote})
	}

	if updateData.ProcessedBy != "" {
		updates = append(updates, firestore.Update{Path: "processedBy", Value: updateData.ProcessedBy})
	}

	if updateData.Action != "" {
		updates = append(updates, firestore.Update{Path: "action", Value: updateData.Action})
	}

	_, err := h.firestore.Collection("reports").Doc(reportID).Update(ctx, updates)
	if err != nil {
		log.Printf("Error updating report: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "신고 상태 업데이트 실패",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "신고 처리가 완료되었습니다",
	})
}

// CreateReport creates a new report
func (h *ReportHandler) CreateReport(c *gin.Context) {
	ctx := context.Background()

	var reportData struct {
		CompetitionID string `json:"competitionId" binding:"required"`
		SubmissionID  string `json:"submissionId" binding:"required"`
		CreatorID     string `json:"creatorId" binding:"required"`
		CreatorName   string `json:"creatorName" binding:"required"`
		Reason        string `json:"reason" binding:"required"`
		Detail        string `json:"detail"`
		ReportedBy    string `json:"reportedBy" binding:"required"` // brand, admin
	}

	if err := c.ShouldBindJSON(&reportData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "잘못된 요청 데이터",
			"error":   err.Error(),
		})
		return
	}

	// 신고 데이터 생성
	now := time.Now()
	report := map[string]interface{}{
		"competitionId": reportData.CompetitionID,
		"submissionId":  reportData.SubmissionID,
		"creatorId":     reportData.CreatorID,
		"creatorName":   reportData.CreatorName,
		"reason":        reportData.Reason,
		"detail":        reportData.Detail,
		"reportedAt":    now,
		"reportedBy":    reportData.ReportedBy,
		"status":        "pending",
	}

	// Firestore에 저장
	docRef, _, err := h.firestore.Collection("reports").Add(ctx, report)
	if err != nil {
		log.Printf("Error creating report: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "신고 생성 실패",
			"error":   err.Error(),
		})
		return
	}

	log.Printf("✅ 신고 생성 완료: %s (대회: %s, 신고자: %s)",
		docRef.ID, reportData.CompetitionID, reportData.ReportedBy)

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "신고가 접수되었습니다",
		"reportId": docRef.ID,
	})
}

// GetReportStats retrieves report statistics
func (h *ReportHandler) GetReportStats(c *gin.Context) {
	ctx := context.Background()

	// 모든 신고 조회
	iter := h.firestore.Collection("reports").Documents(ctx)
	defer iter.Stop()

	stats := map[string]int{
		"total":     0,
		"pending":   0,
		"reviewed":  0,
		"resolved":  0,
		"dismissed": 0,
	}

	reasonStats := make(map[string]int)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error iterating reports: %v", err)
			continue
		}

		data := doc.Data()
		stats["total"]++

		// Status 통계
		if status, ok := data["status"].(string); ok {
			stats[status]++
		}

		// Reason 통계
		if reason, ok := data["reason"].(string); ok {
			reasonStats[reason]++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"stats":       stats,
		"reasonStats": reasonStats,
	})
}
