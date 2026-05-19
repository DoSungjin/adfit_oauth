package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"

	"adfit-oauth/config"
)

// ==================== 타입 정의 ====================

type StatsService struct {
	firestore  *firestore.Client
	testDB     *firestore.Client // adtown-test
	realtimeDB *db.Client
	youtube    *youtube.Service
	clients    *FirestoreClients // ⭐ 두 DB 관리자
}

type CompetitionStats struct {
	TotalSubmissions   int       `json:"totalSubmissions"`
	TotalViews         int64     `json:"totalViews"`
	UniqueCreators     int       `json:"uniqueCreators"`
	AverageViews       float64   `json:"averageViews"`
	TotalEstimatedCost int64     `json:"totalEstimatedCost"` // 성과형 총 예상 비용
	LastUpdated        time.Time `json:"lastUpdated"`
}

type SubmissionData struct {
	ID                string `json:"id"`
	CompetitionID     string `json:"competitionId"`
	CreatorID         string `json:"creatorId"`
	CreatorName       string `json:"creatorName"`
	Platform          string `json:"platform"`
	VideoID           string `json:"videoId"`
	CurrentViewCount  int64  `json:"currentViewCount"`
	LikeCount         int64  `json:"likeCount"`
	ShareCount        int64  `json:"shareCount"`
	SavedCount        int64  `json:"savedCount"`   // Instagram 전용
	CommentCount      int64  `json:"commentCount"` // YouTube/TikTok
	EstimatedEarnings int64  `json:"estimatedEarnings"`
}

// 성과형 대회 정보 (캐싱용)
type PerformanceCompetitionInfo struct {
	IsPerformance bool
	PricePerView  int64
	MinViews      int64
}

type WinnerInfo struct {
	CreatorID   string  `json:"creatorId"`
	CreatorName string  `json:"creatorName"`
	ViewCount   int64   `json:"viewCount"`
	Prize       float64 `json:"prize"`
}

// ==================== 생성자 ====================

func NewStatsService() (*StatsService, error) {
	// ⭐ FirestoreClients 사용 (두 DB 동시 지원)
	clients := GetFirestoreClients()
	if clients == nil {
		return nil, fmt.Errorf("FirestoreClients 초기화 실패")
	}

	firestoreClient := clients.GetDefaultDB()
	testClient := clients.GetTestDB()
	realtimeDBClient := clients.GetRealtimeDB()

	// YouTube 서비스 초기화
	ctx := context.Background()
	var youtubeService *youtube.Service
	var apiKey string

	// ⭐ 환경변수 우선, Config는 fallback
	apiKey = os.Getenv("YOUTUBE_API_KEY")
	if apiKey == "" && config.Config != nil {
		apiKey = config.GetYouTubeAPIKey()
	}

	if apiKey != "" && apiKey != "YOUR_YOUTUBE_API_KEY" {
		var err error
		youtubeService, err = youtube.NewService(ctx, option.WithAPIKey(apiKey))
		if err != nil {
			log.Printf("⚠️ YouTube 서비스 초기화 실패: %v", err)
			youtubeService = nil
		} else {
			log.Println("✅ YouTube API 연동 완료")
		}
	} else {
		log.Println("⚠️ YouTube API 키가 설정되지 않음")
		youtubeService = nil
	}

	return &StatsService{
		firestore:  firestoreClient,
		testDB:     testClient,
		realtimeDB: realtimeDBClient,
		youtube:    youtubeService,
		clients:    clients,
	}, nil
}

// ==================== 상금 결제 ====================

// CompletePrizePayment - ⭐ 상금 입금 완료 처리
func (s *StatsService) CompletePrizePayment(competitionID, userID string) error {
	ctx := context.Background()

	// 1. 참가자 문서 경로
	participantRef := s.firestore.Collection("competitions").Doc(competitionID).Collection("participants").Doc(userID)

	// 2. 현재 상태 확인
	participantDoc, err := participantRef.Get(ctx)
	if err != nil {
		return fmt.Errorf("참가자 정보 조회 실패: %v", err)
	}

	if !participantDoc.Exists() {
		return fmt.Errorf("참가자를 찾을 수 없습니다")
	}

	data := participantDoc.Data()
	prizeData, ok := data["prize"].(map[string]interface{})
	if !ok || prizeData["prizeClaimStatus"] != "ready" {
		return fmt.Errorf("상금 신청 상태가 아닙니다")
	}

	// 3. 입금 완료 처리
	_, err = participantRef.Update(ctx, []firestore.Update{
		{Path: "prize.prizeClaimStatus", Value: "completed"},
		{Path: "prize.completedAt", Value: firestore.ServerTimestamp},
	})

	if err != nil {
		return fmt.Errorf("입금 완료 처리 실패: %v", err)
	}

	log.Printf("✅ 입금 완료: Competition=%s, User=%s", competitionID, userID)
	return nil
}

// SaveDailyAggregation - 일별 데이터 집계
func (s *StatsService) SaveDailyAggregation() error {
	// TODO: 일별 데이터 집계 구현
	return nil
}

// ==================== 시스템 통계 ====================

// 일별 시스템 통계 업데이트
func (s *StatsService) UpdateDailySystemStats() error {
	ctx := context.Background()
	today := time.Now().Format("2006-01-02")

	// 전체 대회 수 계산
	totalCompetitions, err := s.countDocuments(ctx, "competitions")
	if err != nil {
		return fmt.Errorf("전체 대회 수 계산 실패: %v", err)
	}

	// 활성 대회 수 계산 (ONGOING)
	activeCompetitions, err := s.countDocumentsWithCondition(ctx, "competitions", "status", "ONGOING")
	if err != nil {
		return fmt.Errorf("활성 대회 수 계산 실패: %v", err)
	}

	// 전체 사용자 수 계산
	totalUsers, err := s.countDocuments(ctx, "users")
	if err != nil {
		return fmt.Errorf("전체 사용자 수 계산 실패: %v", err)
	}

	// 브랜드 수 계산
	totalBrands, err := s.countDocumentsWithCondition(ctx, "users", "role", "brand")
	if err != nil {
		return fmt.Errorf("브랜드 수 계산 실패: %v", err)
	}

	// 크리에이터 수 계산
	totalCreators, err := s.countDocumentsWithCondition(ctx, "users", "role", "creator")
	if err != nil {
		return fmt.Errorf("크리에이터 수 계산 실패: %v", err)
	}

	// 총 상금 규모 계산
	totalPrizeAmount, err := s.calculateTotalPrizeAmount(ctx)
	if err != nil {
		return fmt.Errorf("총 상금 계산 실패: %v", err)
	}

	// 총 조회수 계산
	totalViews, err := s.calculateTotalViews(ctx)
	if err != nil {
		return fmt.Errorf("총 조회수 계산 실패: %v", err)
	}

	// 시스템 통계 저장
	systemStats := map[string]interface{}{
		"date":               today,
		"totalCompetitions":  totalCompetitions,
		"activeCompetitions": activeCompetitions,
		"totalUsers":         totalUsers,
		"totalBrands":        totalBrands,
		"totalCreators":      totalCreators,
		"totalPrizeAmount":   totalPrizeAmount,
		"totalViews":         totalViews,
		"updatedAt":          time.Now(),
	}

	_, err = s.firestore.Collection("systemStats").Doc(today).Set(ctx, systemStats)
	return err
}

// ==================== 유틸리티 메서드들 ====================

// 컬렉션 문서 수 계산
func (s *StatsService) countDocuments(ctx context.Context, collection string) (int, error) {
	iter := s.firestore.Collection(collection).Documents(ctx)
	count := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

// 조건부 문서 수 계산
func (s *StatsService) countDocumentsWithCondition(ctx context.Context, collection, field, value string) (int, error) {
	iter := s.firestore.Collection(collection).Where(field, "==", value).Documents(ctx)
	count := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

// 총 상금 규모 계산
func (s *StatsService) calculateTotalPrizeAmount(ctx context.Context) (float64, error) {
	iter := s.firestore.Collection("competitions").Documents(ctx)
	total := float64(0)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, err
		}

		data := doc.Data()
		if prize, ok := data["prize"]; ok {
			if prizeFloat, ok := prize.(float64); ok {
				total += prizeFloat
			}
		}
		if prizeAmount, ok := data["prizeAmount"]; ok {
			if prizeFloat, ok := prizeAmount.(float64); ok {
				total += prizeFloat
			}
		}
	}

	return total, nil
}

// 총 조회수 계산
func (s *StatsService) calculateTotalViews(ctx context.Context) (int64, error) {
	iter := s.firestore.Collection("competitions").Documents(ctx)
	total := int64(0)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, err
		}

		data := doc.Data()
		if stats, ok := data["stats"].(map[string]interface{}); ok {
			if totalViews, ok := stats["totalViews"]; ok {
				total += getInt64FromData(map[string]interface{}{"totalViews": totalViews}, "totalViews")
			}
		}
	}

	return total, nil
}

// ⭐ 참가자 수 조회 (participants 컬렉션, default DB)
func (s *StatsService) getParticipantsCount(ctx context.Context, competitionID string) (int, error) {
	iter := s.firestore.Collection("competitions").
		Doc(competitionID).
		Collection("participants").
		Where("status", "==", "accepted"). // status가 accepted인 참가자만
		Documents(ctx)

	count := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, err
		}
		count++
	}

	return count, nil
}

// ==================== 데이터 추출 헬퍼 (free functions) ====================

func getStringFromData(data map[string]interface{}, key string) string {
	if val, ok := data[key].(string); ok {
		return val
	}
	return ""
}

func getInt64FromData(data map[string]interface{}, key string) int64 {
	switch val := data[key].(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	default:
		return 0
	}
}

func getIntFromData(data map[string]interface{}, key string) int {
	if val, ok := data[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		}
	}
	return 0
}

func getFloat64FromData(data map[string]interface{}, key string) float64 {
	if val, ok := data[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		}
	}
	return 0
}

// ==================== 성과형 대회 정보 ====================

// 성과형 대회 정보 조회
func (s *StatsService) getCompetitionPerformanceInfo(ctx context.Context, competitionID string) *PerformanceCompetitionInfo {
	doc, err := s.firestore.Collection("competitions").Doc(competitionID).Get(ctx)
	if err != nil {
		return &PerformanceCompetitionInfo{IsPerformance: false}
	}
	data := doc.Data()
	compType := getStringFromData(data, "competitionType")
	if compType != "performance" {
		return &PerformanceCompetitionInfo{IsPerformance: false}
	}
	return &PerformanceCompetitionInfo{
		IsPerformance: true,
		PricePerView:  getInt64FromData(data, "pricePerView"),
		MinViews:      getInt64FromData(data, "minViews"),
	}
}

// 예상 수익 계산
func calculateEstimatedEarnings(viewCount, pricePerView, minViews int64) int64 {
	if viewCount >= minViews {
		return viewCount * pricePerView
	}
	return 0
}
