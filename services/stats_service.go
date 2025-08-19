package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	
	"adfit-oauth/config"
)

type StatsService struct {
	firestore *firestore.Client
	youtube   *youtube.Service
}

type CompetitionStats struct {
	TotalSubmissions int     `json:"totalSubmissions"`
	TotalViews       int64   `json:"totalViews"`
	UniqueCreators   int     `json:"uniqueCreators"`
	AverageViews     float64 `json:"averageViews"`
	LastUpdated      time.Time `json:"lastUpdated"`
}

type SubmissionData struct {
	ID               string `json:"id"`
	CompetitionID    string `json:"competitionId"`
	CreatorID        string `json:"creatorId"`
	Platform         string `json:"platform"`
	VideoID          string `json:"videoId"`
	CurrentViewCount int64  `json:"currentViewCount"`
}

func NewStatsService() (*StatsService, error) {
	ctx := context.Background()

	// Firebase 초기화
	var app *firebase.App
	var err error

	// config가 로드되어 있으면 사용, 없으면 기본값
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
		// 기존 방식 (하위 호환성)
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID: "posted-app-c4ff5",
		})
	}
	
	if err != nil {
		return nil, fmt.Errorf("firebase 초기화 실패: %v", err)
	}

	// Firestore 클라이언트
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore 클라이언트 생성 실패: %v", err)
	}

	// YouTube 서비스 초기화
	var youtubeService *youtube.Service
	var apiKey string
	
	if config.Config != nil {
		apiKey = config.GetYouTubeAPIKey()
	} else {
		// 환경변수에서 직접 읽기 (하위 호환성)
		apiKey = "YOUR_YOUTUBE_API_KEY" // 실제 키로 교체 필요
	}
	
	if apiKey != "" && apiKey != "YOUR_YOUTUBE_API_KEY" {
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
		firestore: firestoreClient,
		youtube:   youtubeService,
	}, nil
}

// 모든 활성 대회의 통계 업데이트 + 시간별 스냅샷 저장
func (s *StatsService) UpdateAllActiveCompetitions() error {
	ctx := context.Background()
	
	log.Println("🔄 활성 대회 통계 업데이트 시작")

	// 활성 상태 대회 조회
	iter := s.firestore.Collection("competitions").
		Where("status", "==", "active").
		Documents(ctx)

	count := 0
	successCount := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ 대회 조회 오류: %v", err)
			continue
		}

		count++
		competitionID := doc.Ref.ID
		
		// 각 대회 통계 업데이트
		if err := s.UpdateCompetitionStats(competitionID); err != nil {
			log.Printf("❌ 대회 %s 통계 업데이트 실패: %v", competitionID, err)
		} else {
			successCount++
			
			// 시간별 스냅샷 저장
			if err := s.SaveCompetitionHourlySnapshot(competitionID); err != nil {
				log.Printf("⚠️ 대회 %s 시간별 스냅샷 저장 실패: %v", competitionID, err)
			}
		}
	}

	log.Printf("✅ 활성 대회 통계 업데이트 완료: %d개 중 %d개 성공", count, successCount)
	return nil
}

// 특정 대회의 통계 업데이트
func (s *StatsService) UpdateCompetitionStats(competitionID string) error {
	ctx := context.Background()

	log.Printf("📊 대회 %s 통계 업데이트 시작", competitionID)

	// 1. 해당 대회의 모든 submissions 조회
	submissions, err := s.getCompetitionSubmissions(ctx, competitionID)
	if err != nil {
		return fmt.Errorf("submissions 조회 실패: %v", err)
	}

	if len(submissions) == 0 {
		log.Printf("ℹ️ 대회 %s에 제출된 영상이 없습니다", competitionID)
		return s.updateCompetitionStatsInFirestore(ctx, competitionID, CompetitionStats{
			TotalSubmissions: 0,
			TotalViews:       0,
			UniqueCreators:   0,
			AverageViews:     0,
			LastUpdated:      time.Now(),
		})
	}

	// 2. YouTube 영상들의 조회수 업데이트
	if err := s.updateYouTubeViewCounts(ctx, submissions); err != nil {
		log.Printf("⚠️ YouTube 조회수 업데이트 실패: %v", err)
		// YouTube 업데이트 실패해도 기존 데이터로 통계는 계산
	}

	// 3. 통계 계산
	stats := s.calculateCompetitionStats(submissions)

	// 4. Firebase에 통계 저장
	if err := s.updateCompetitionStatsInFirestore(ctx, competitionID, stats); err != nil {
		return fmt.Errorf("통계 저장 실패: %v", err)
	}

	log.Printf("✅ 대회 %s 통계 업데이트 완료 - 제출: %d, 조회수: %d, 크리에이터: %d",
		competitionID, stats.TotalSubmissions, stats.TotalViews, stats.UniqueCreators)

	return nil
}

// 대회의 모든 submissions 조회
func (s *StatsService) getCompetitionSubmissions(ctx context.Context, competitionID string) ([]SubmissionData, error) {
	iter := s.firestore.Collection("competitions").
		Doc(competitionID).
		Collection("submissions").
		Documents(ctx)

	var submissions []SubmissionData
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		data := doc.Data()
		submission := SubmissionData{
			ID:            doc.Ref.ID,
			CompetitionID: competitionID,
			CreatorID:     getStringFromData(data, "creatorId"),
			Platform:      getStringFromData(data, "platform"),
			VideoID:       getStringFromData(data, "videoId"),
			CurrentViewCount: getInt64FromData(data, "currentViewCount"),
		}

		// YouTube 영상인 경우 youtubeData에서 videoId 추출
		if submission.Platform == "youtube" {
			if youtubeData, ok := data["youtubeData"].(map[string]interface{}); ok {
				if videoID, ok := youtubeData["videoId"].(string); ok {
					submission.VideoID = videoID
				}
			}
		}

		submissions = append(submissions, submission)
	}

	return submissions, nil
}

// YouTube 영상들의 조회수 업데이트
func (s *StatsService) updateYouTubeViewCounts(ctx context.Context, submissions []SubmissionData) error {
	// YouTube 영상들만 필터링
	var youtubeVideoIDs []string
	youtubeSubmissions := make(map[string]SubmissionData)

	for _, sub := range submissions {
		if sub.Platform == "youtube" && sub.VideoID != "" {
			youtubeVideoIDs = append(youtubeVideoIDs, sub.VideoID)
			youtubeSubmissions[sub.VideoID] = sub
		}
	}

	if len(youtubeVideoIDs) == 0 {
		return nil
	}

	// YouTube API로 조회수 가져오기 (50개씩 배치 처리)
	for i := 0; i < len(youtubeVideoIDs); i += 50 {
		end := i + 50
		if end > len(youtubeVideoIDs) {
			end = len(youtubeVideoIDs)
		}

		batch := youtubeVideoIDs[i:end]
		if err := s.updateYouTubeViewCountsBatch(ctx, batch, youtubeSubmissions); err != nil {
			log.Printf("⚠️ YouTube 배치 %d-%d 업데이트 실패: %v", i, end, err)
		}
	}

	return nil
}

// YouTube API 배치 호출
func (s *StatsService) updateYouTubeViewCountsBatch(ctx context.Context, videoIDs []string, submissions map[string]SubmissionData) error {
	call := s.youtube.Videos.List([]string{"statistics"}).Id(videoIDs...)
	response, err := call.Do()
	if err != nil {
		return err
	}

	// 배치 쓰기 준비
	batch := s.firestore.Batch()

	for _, video := range response.Items {
		if submission, exists := submissions[video.Id]; exists {
			viewCount := int64(video.Statistics.ViewCount)
			
			// submissions 문서 업데이트
			docRef := s.firestore.Collection("competitions").
				Doc(submission.CompetitionID).
				Collection("submissions").
				Doc(submission.ID)

			updateData := map[string]interface{}{
				"currentViewCount": viewCount,
				"lastUpdatedAt":    time.Now(),
			}

			// YouTube 영상인 경우 youtubeData도 업데이트
			if submission.Platform == "youtube" {
				updateData["youtubeData.statistics.viewCount"] = viewCount
			}

			// Firestore batch update - slice 방식
			updates := []firestore.Update{
				{Path: "currentViewCount", Value: viewCount},
				{Path: "lastUpdatedAt", Value: time.Now()},
			}
			
			// YouTube 플랫폼인 경우 추가 필드 업데이트
			if submission.Platform == "youtube" {
				updates = append(updates, firestore.Update{
					Path: "youtubeData.statistics.viewCount", 
					Value: viewCount,
				})
			}
			
			batch.Update(docRef, updates)
		}
	}

	// 배치 실행
	_, err = batch.Commit(ctx)
	return err
}

// 통계 계산
func (s *StatsService) calculateCompetitionStats(submissions []SubmissionData) CompetitionStats {
	totalSubmissions := len(submissions)
	var totalViews int64
	creatorSet := make(map[string]bool)

	for _, sub := range submissions {
		totalViews += sub.CurrentViewCount
		creatorSet[sub.CreatorID] = true
	}

	uniqueCreators := len(creatorSet)
	averageViews := float64(0)
	if totalSubmissions > 0 {
		averageViews = float64(totalViews) / float64(totalSubmissions)
	}

	return CompetitionStats{
		TotalSubmissions: totalSubmissions,
		TotalViews:       totalViews,
		UniqueCreators:   uniqueCreators,
		AverageViews:     averageViews,
		LastUpdated:      time.Now(),
	}
}

// Firebase에 통계 저장
func (s *StatsService) updateCompetitionStatsInFirestore(ctx context.Context, competitionID string, stats CompetitionStats) error {
	docRef := s.firestore.Collection("competitions").Doc(competitionID)

	// Firestore update - slice 방식
	updates := []firestore.Update{
		{Path: "stats", Value: map[string]interface{}{
			"totalSubmissions": stats.TotalSubmissions,
			"totalViews":       stats.TotalViews,
			"uniqueCreators":   stats.UniqueCreators,
			"averageViews":     stats.AverageViews,
			"lastUpdated":      stats.LastUpdated,
		}},
		{Path: "participantCount", Value: stats.TotalSubmissions},
		{Path: "totalViews", Value: float64(stats.TotalViews)},
	}
	
	_, err := docRef.Update(ctx, updates)
	return err
}

// 일별 시스템 통계 업데이트
func (s *StatsService) UpdateDailySystemStats() error {
	ctx := context.Background()
	today := time.Now().Format("2006-01-02")
	
	log.Printf("📊 %s 시스템 통계 업데이트 시작", today)
	
	// 전체 대회 수 계산
	totalCompetitions, err := s.countDocuments(ctx, "competitions")
	if err != nil {
		return fmt.Errorf("전체 대회 수 계산 실패: %v", err)
	}
	
	// 활성 대회 수 계산
	activeCompetitions, err := s.countDocumentsWithCondition(ctx, "competitions", "status", "active")
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
		"date":              today,
		"totalCompetitions": totalCompetitions,
		"activeCompetitions": activeCompetitions,
		"totalUsers":        totalUsers,
		"totalBrands":       totalBrands,
		"totalCreators":     totalCreators,
		"totalPrizeAmount":  totalPrizeAmount,
		"totalViews":        totalViews,
		"updatedAt":         time.Now(),
	}
	
	_, err = s.firestore.Collection("systemStats").Doc(today).Set(ctx, systemStats)
	if err != nil {
		return fmt.Errorf("시스템 통계 저장 실패: %v", err)
	}
	
	log.Printf("✅ %s 시스템 통계 업데이트 완료 - 대회: %d, 사용자: %d, 총 상금: %.0f", 
		today, totalCompetitions, totalUsers, totalPrizeAmount)
	
	return nil
}

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

// 유틸리티 함수들
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// === 시간별 스냅샷 저장 기능 (새로 추가) ===

// 특정 대회의 시간별 스냅샷 저장
func (s *StatsService) SaveCompetitionHourlySnapshot(competitionID string) error {
	ctx := context.Background()
	now := time.Now()
	hourKey := now.Format("2006-01-02-15") // 2024-08-18-14
	
	// 현재 대회 정보 조회
	competitionDoc, err := s.firestore.Collection("competitions").Doc(competitionID).Get(ctx)
	if err != nil {
		return fmt.Errorf("대회 정보 조회 실패: %v", err)
	}

	competitionData := competitionDoc.Data()
	currentStats := CompetitionStats{}
	
	if stats, ok := competitionData["stats"].(map[string]interface{}); ok {
		currentStats.TotalSubmissions = getIntFromData(stats, "totalSubmissions")
		currentStats.TotalViews = getInt64FromData(stats, "totalViews")
		currentStats.UniqueCreators = getIntFromData(stats, "uniqueCreators")
	}

	// 제출 데이터 조회
	submissions, err := s.getCompetitionSubmissions(ctx, competitionID)
	if err != nil {
		return fmt.Errorf("submissions 조회 실패: %v", err)
	}

	// 이전 시간 스냅샷 조회 (증가량 계산용)
	previousSnapshot, err := s.getPreviousHourSnapshot(ctx, competitionID, now.Add(-time.Hour))
	if err != nil {
		// 첫 번째 스냅샷이거나 오류시 0으로 초기화
		previousSnapshot = &CompetitionStats{}
	}

	// 순위 계산
	rankings := s.calculateRankings(submissions)
	
	// 시간별 스냅샷 데이터 구성
	snapshot := map[string]interface{}{
		"timestamp":        now,
		"competitionId":    competitionID,
		"totalViews":       currentStats.TotalViews,
		"totalSubmissions": currentStats.TotalSubmissions,
		"uniqueCreators":   currentStats.UniqueCreators,
		"topSubmissions":   rankings[:min(10, len(rankings))], // 상위 10개만
		"hourlyGrowth": map[string]interface{}{
			"viewsGain":      currentStats.TotalViews - previousSnapshot.TotalViews,
			"newSubmissions": currentStats.TotalSubmissions - previousSnapshot.TotalSubmissions,
			"rankingChanges": 0, // 간단화를 위해 0으로 설정
		},
	}

	// Firebase에 저장
	_, err = s.firestore.Collection("hourlyStats").
		Doc(competitionID).
		Collection("snapshots").
		Doc(hourKey).
		Set(ctx, snapshot)

	if err != nil {
		return fmt.Errorf("시간별 스냅샷 저장 실패: %v", err)
	}

	log.Printf("✅ 대회 %s 시간별 스냅샷 저장 완료 (%s)", competitionID, hourKey)
	return nil
}

// 이전 시간 스냅샷 조회
func (s *StatsService) getPreviousHourSnapshot(ctx context.Context, competitionID string, previousHour time.Time) (*CompetitionStats, error) {
	hourKey := previousHour.Format("2006-01-02-15")
	
	doc, err := s.firestore.Collection("hourlyStats").
		Doc(competitionID).
		Collection("snapshots").
		Doc(hourKey).
		Get(ctx)

	if err != nil {
		return nil, err
	}

	data := doc.Data()
	return &CompetitionStats{
		TotalViews:       getInt64FromData(data, "totalViews"),
		TotalSubmissions: getIntFromData(data, "totalSubmissions"),
		UniqueCreators:   getIntFromData(data, "uniqueCreators"),
	}, nil
}

// 순위 계산
func (s *StatsService) calculateRankings(submissions []SubmissionData) []map[string]interface{} {
	// 조회수 기준 정렬
	sort.Slice(submissions, func(i, j int) bool {
		return submissions[i].CurrentViewCount > submissions[j].CurrentViewCount
	})

	rankings := make([]map[string]interface{}, len(submissions))
	for i, sub := range submissions {
		rankings[i] = map[string]interface{}{
			"submissionId": sub.ID,
			"rank":         i + 1,
			"viewCount":    sub.CurrentViewCount,
			"platform":     sub.Platform,
		}
	}

	return rankings
}

// === 일별 집계 메서드 ===

// SaveDailyAggregation - 일별 시스템 통계 집계 및 저장
func (s *StatsService) SaveDailyAggregation() error {
	return s.UpdateDailySystemStats()
}

// DeleteDataByDateRange - 특정 기간의 데이터 삭제
func (s *StatsService) DeleteDataByDateRange(startDate, endDate time.Time) (map[string]int, error) {
	ctx := context.Background()
	result := map[string]int{
		"snapshots": 0,
		"dailyStats": 0,
	}

	// hourlyStats 삭제
	iter := s.firestore.Collection("hourlyStats").Documents(ctx)
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			continue
		}

		competitionID := doc.Ref.ID
		snapshotIter := s.firestore.Collection("hourlyStats").
			Doc(competitionID).
			Collection("snapshots").
			Documents(ctx)

		for {
			snapshotDoc, err := snapshotIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				continue
			}

			data := snapshotDoc.Data()
			if timestamp, ok := data["timestamp"]; ok {
				if ts, ok := timestamp.(time.Time); ok {
					if ts.After(startDate) && ts.Before(endDate) {
						_, err := snapshotDoc.Ref.Delete(ctx)
						if err == nil {
							result["snapshots"]++
						}
					}
				}
			}
		}
	}

	return result, nil
}

// DeleteCompetitionHistoryData - 특정 대회의 모든 히스토리 데이터 삭제
func (s *StatsService) DeleteCompetitionHistoryData(competitionID string) (int, error) {
	ctx := context.Background()
	deletedCount := 0

	// hourlyStats 삭제
	iter := s.firestore.Collection("hourlyStats").
		Doc(competitionID).
		Collection("snapshots").
		Documents(ctx)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			continue
		}

		_, err = doc.Ref.Delete(ctx)
		if err == nil {
			deletedCount++
		}
	}

	return deletedCount, nil
}

// === 관리자용 데이터 정리 메서드들 (새로 추가) ===

// 오래된 시간별 스냅샷 삭제
func (s *StatsService) CleanupOldSnapshots(cutoffDate time.Time) (int, error) {
	ctx := context.Background()
	deletedCount := 0
	
	log.Printf("🧹 %s 이전 시간별 스냅샷 정리 시작", cutoffDate.Format("2006-01-02"))

	// 모든 대회의 hourlyStats 조회
	iter := s.firestore.Collection("hourlyStats").Documents(ctx)
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			continue
		}

		competitionID := doc.Ref.ID
		
		// 해당 대회의 스냅샷들 조회
		snapshotIter := s.firestore.Collection("hourlyStats").
			Doc(competitionID).
			Collection("snapshots").
			Documents(ctx)

		for {
			snapshotDoc, err := snapshotIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				continue
			}

			data := snapshotDoc.Data()
			if timestamp, ok := data["timestamp"]; ok {
				if ts, ok := timestamp.(time.Time); ok {
					if ts.Before(cutoffDate) {
						// 삭제
						_, err := snapshotDoc.Ref.Delete(ctx)
						if err != nil {
							log.Printf("❌ 스냅샷 삭제 실패: %v", err)
						} else {
							deletedCount++
						}
					}
				}
			}
		}
	}

	log.Printf("✅ 오래된 스냅샷 정리 완료: %d개 삭제", deletedCount)
	return deletedCount, nil
}

// 저장소 통계 조회 (관리자용)
func (s *StatsService) GetStorageStats() (map[string]interface{}, error) {
	ctx := context.Background()
	stats := map[string]interface{}{
		"collections": map[string]int{
			"competitions":  0,
			"users":         0,
			"hourlyStats":   0,
			"dailyStats":    0,
		},
		"totalSnapshots":  0,
		"calculatedAt":    time.Now(),
	}

	// 기본 컬렉션 카운트
	stats["collections"].(map[string]int)["competitions"], _ = s.countDocuments(ctx, "competitions")
	stats["collections"].(map[string]int)["users"], _ = s.countDocuments(ctx, "users")

	// hourlyStats 통계
	hourlyCount := 0
	totalSnapshots := 0

	iter := s.firestore.Collection("hourlyStats").Documents(ctx)
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			continue
		}

		hourlyCount++

		// 각 대회의 스냅샷 개수 계산
		snapshotIter := doc.Ref.Collection("snapshots").Documents(ctx)
		for {
			_, err := snapshotIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				continue
			}
			totalSnapshots++
		}
	}

	stats["collections"].(map[string]int)["hourlyStats"] = hourlyCount
	stats["totalSnapshots"] = totalSnapshots

	return stats, nil
}
