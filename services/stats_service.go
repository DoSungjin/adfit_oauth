package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"

	"adfit-oauth/config"
)

type StatsService struct {
	firestore  *firestore.Client
	testDB     *firestore.Client  // adtown-test
	realtimeDB *db.Client
	youtube    *youtube.Service
	clients    *FirestoreClients  // ⭐ 두 DB 관리자
}

type CompetitionStats struct {
	TotalSubmissions int       `json:"totalSubmissions"`
	TotalViews       int64     `json:"totalViews"`
	UniqueCreators   int       `json:"uniqueCreators"`
	AverageViews     float64   `json:"averageViews"`
	LastUpdated      time.Time `json:"lastUpdated"`
}

type SubmissionData struct {
	ID               string `json:"id"`
	CompetitionID    string `json:"competitionId"`
	CreatorID        string `json:"creatorId"`
	CreatorName      string `json:"creatorName"`
	Platform         string `json:"platform"`
	VideoID          string `json:"videoId"`
	CurrentViewCount int64  `json:"currentViewCount"`
}

type WinnerInfo struct {
	CreatorID   string  `json:"creatorId"`
	CreatorName string  `json:"creatorName"`
	ViewCount   int64   `json:"viewCount"`
	Prize       float64 `json:"prize"`
}

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

// ==================== 대회 상태 자동 관리 ====================

// CheckAndStartApprovedCompetitions - APPROVED → ONGOING 전환 (자정 12시)
func (s *StatsService) CheckAndStartApprovedCompetitions() error {
	ctx := context.Background()
	now := time.Now()

	// ⭐ 두 DB 모두에서 조회
	databases := []*firestore.Client{s.firestore}
	if s.testDB != nil {
		databases = append(databases, s.testDB)
	}

	startedCount := 0
	for _, db := range databases {
		iter := db.Collection("competitions").
			Where("status", "==", "APPROVED").
			Where("deleted", "==", false).
			Documents(ctx)

		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("❌ APPROVED 대회 조회 오류: %v", err)
				continue
			}

			data := doc.Data()

			// startDate 확인
			startDate, ok := data["startDate"].(time.Time)
			if !ok {
				continue
			}

			// startDate가 현재 시간보다 이전이거나 같으면 시작
			if startDate.Before(now) || startDate.Equal(now) {
				// ONGOING으로 상태 변경
				_, err := doc.Ref.Update(ctx, []firestore.Update{
					{Path: "status", Value: "ONGOING"},
					{Path: "startedAt", Value: now},
					{Path: "updatedAt", Value: now},
				})

				if err == nil {
					startedCount++
				}
			}
		}
	}

	return nil
}

// CheckAndFinishOngoingCompetitions - ONGOING → FINISHED 전환 + 최종 데이터 수집 (오전 1시)
func (s *StatsService) CheckAndFinishOngoingCompetitions() error {
	ctx := context.Background()
	now := time.Now()

	// ⭐ 두 DB 모두에서 조회
	databases := []*firestore.Client{s.firestore}
	if s.testDB != nil {
		databases = append(databases, s.testDB)
	}

	finishedCount := 0
	for _, db := range databases {
		iter := db.Collection("competitions").
			Where("status", "==", "ONGOING").
			Where("deleted", "==", false).
			Documents(ctx)

		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("❌ ONGOING 대회 조회 오류: %v", err)
				continue
			}

			data := doc.Data()
			competitionID := doc.Ref.ID

			// deadline 확인
			deadline, ok := data["deadline"].(time.Time)
			if !ok {
				continue
			}

			// deadline이 지났으면 종료 처리
			if deadline.Before(now) {
				// 종료 처리 (YouTube 데이터 수집 + 우승자 선정)
				if err := s.FinalizeCompetitionWithDB(competitionID, db); err == nil {
					finishedCount++
				}
			}
		}
	}

	return nil
}

func (s *StatsService) CheckAndCancelPendingCompetitions() error {
	ctx := context.Background()
	now := time.Now()

	// ⭐ 두 DB 모두에서 조회
	databases := []*firestore.Client{s.firestore}
	if s.testDB != nil {
		databases = append(databases, s.testDB)
	}

	pendingStatuses := []string{"REGISTERED", "NOTICED", "UNDERREVIEW"}
	canceledCount := 0

	for _, db := range databases {
		for _, status := range pendingStatuses {
			iter := db.Collection("competitions").
				Where("status", "==", status).
				Where("deleted", "==", false).
				Documents(ctx)

			for {
				doc, err := iter.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					log.Printf("❌ %s 대회 조회 오류: %v", status, err)
					continue
				}

				data := doc.Data()

				// startDate 확인
				startDate, ok := data["startDate"].(time.Time)
				if !ok {
					continue
				}

				// 개최일이 되었는데도 승인 안 되면 취소
				if startDate.Before(now) || startDate.Equal(now) {
					// CANCELLED로 상태 변경
					_, err := doc.Ref.Update(ctx, []firestore.Update{
						{Path: "status", Value: "CANCELLED"},
						{Path: "cancelledAt", Value: now},
						{Path: "cancelReason", Value: "자동 취소: 승인 미완료"},
						{Path: "updatedAt", Value: now},
					})

					if err == nil {
						canceledCount++
					}
				}
			}
		}
	}

	return nil
}

// FinalizeCompetition - 대회 종료 처리 (공개 메서드 - default DB 사용)
func (s *StatsService) FinalizeCompetition(competitionID string) error {
	return s.FinalizeCompetitionWithDB(competitionID, s.firestore)
}

// FinalizeCompetitionWithDB - 대회 종료 처리 (특정 DB 사용)
func (s *StatsService) FinalizeCompetitionWithDB(competitionID string, db *firestore.Client) error {
	ctx := context.Background()
	now := time.Now()
	
	log.Printf("\n========================================")
	log.Printf("🏆 대회 종료 처리 시작: %s", competitionID)
	log.Printf("========================================\n")

	// 1️⃣ Submissions 조회
	submissions, err := s.getCompetitionSubmissionsWithDB(ctx, competitionID, db)
	if err != nil {
		log.Printf("❌ submissions 조회 실패: %v", err)
		return fmt.Errorf("submissions 조회 실패: %v", err)
	}

	if len(submissions) == 0 {
		log.Printf("⚠️ 제출된 영상이 없습니다. 대회를 FINISHED 상태로 변경합니다.")
		_, err := db.Collection("competitions").Doc(competitionID).Update(ctx, []firestore.Update{
			{Path: "status", Value: "FINISHED"},
			{Path: "finishedAt", Value: now},
			{Path: "updatedAt", Value: now},
			{Path: "finalDataCollected", Value: true},
		})
		return err
	}

	// 2️⃣ YouTube 최종 조회수 수집
	if s.youtube != nil {
		s.updateYouTubeViewCounts(ctx, submissions)
	}

	// 3️⃣ 대회 정보 읽기 (상금 설정)
	log.Printf("📊 대회 정보 조회 중...")
	competitionDoc, err := db.Collection("competitions").Doc(competitionID).Get(ctx)
	if err != nil {
		log.Printf("❌ 대회 정보 조회 실패: %v", err)
		return fmt.Errorf("대회 정보 조회 실패: %v", err)
	}

	competitionData := competitionDoc.Data()
	totalPrize := getFloat64FromData(competitionData, "prize")
	winnerCount := getIntFromData(competitionData, "winnerCount")
	if winnerCount == 0 {
		winnerCount = 3 // 기본값
	}

	// 개별 상금 금액 (Flutter에서 설정한 값)
	prizeFirst := getFloat64FromData(competitionData, "prizeFirst")
	prizeSecond := getFloat64FromData(competitionData, "prizeSecond")
	prizeThird := getFloat64FromData(competitionData, "prizeThird")

	// 기본값 설정 (개별 상금이 없으면 비율로 계산)
	if prizeFirst == 0 && totalPrize > 0 {
		prizeFirst = totalPrize * 0.5
		prizeSecond = totalPrize * 0.3
		prizeThird = totalPrize * 0.2
	}
	
	log.Printf("✅ 대회 정보: 총 상금 %.0f원, 수상자 수 %d명", totalPrize, winnerCount)
	log.Printf("  - 1등 상금: %.0f원", prizeFirst)
	log.Printf("  - 2등 상금: %.0f원", prizeSecond)
	log.Printf("  - 3등 상금: %.0f원", prizeThird)

	// 4️⃣ submissions를 creatorId별로 그룹화하여 조회수 합산
	creatorStats := make(map[string]*struct {
		CreatorID   string
		CreatorName string
		TotalViews  int64
		VideoCount  int
	})

	for _, sub := range submissions {
		if sub.CurrentViewCount > 0 { // 조회수가 0보다 큰 영상만 집계
			if _, exists := creatorStats[sub.CreatorID]; !exists {
				creatorStats[sub.CreatorID] = &struct {
					CreatorID   string
					CreatorName string
					TotalViews  int64
					VideoCount  int
				}{
					CreatorID:   sub.CreatorID,
					CreatorName: sub.CreatorName,
					TotalViews:  0,
					VideoCount:  0,
				}
			}
			creatorStats[sub.CreatorID].TotalViews += sub.CurrentViewCount
			creatorStats[sub.CreatorID].VideoCount++
		}
	}

	log.Printf("📊 크리에이터별 통계:")
	for creatorID, stats := range creatorStats {
		log.Printf("  - %s (%s): %d개 영상, 총 조회수 %d", stats.CreatorName, creatorID, stats.VideoCount, stats.TotalViews)
	}

	// 유효한 참가자 필터링 (영상 1개 이상, 총 조회수 > 0)
	type ValidCreator struct {
		CreatorID   string
		CreatorName string
		TotalViews  int64
	}

	validCreators := []ValidCreator{}
	for _, stats := range creatorStats {
		if stats.VideoCount > 0 && stats.TotalViews > 0 {
			validCreators = append(validCreators, ValidCreator{
				CreatorID:   stats.CreatorID,
				CreatorName: stats.CreatorName,
				TotalViews:  stats.TotalViews,
			})
		}
	}

	if len(validCreators) == 0 {
		log.Printf("⚠️ 유효한 참가자가 없습니다 (제출 영상: %d개)", len(submissions))
	}

	// 조회수 기준 정렬
	sort.Slice(validCreators, func(i, j int) bool {
		return validCreators[i].TotalViews > validCreators[j].TotalViews
	})

	log.Printf("📊 정렬된 순위:")
	for i, creator := range validCreators {
		log.Printf("  %d위: %s - 조회수 %d", i+1, creator.CreatorName, creator.TotalViews)
	}

	// 실제 수상자 수 결정
	actualWinnerCount := winnerCount
	if len(validCreators) < winnerCount {
		actualWinnerCount = len(validCreators)
		log.Printf("⚠️ 수상자 수 조정: %d명 → %d명 (유효 참가자 부족)", winnerCount, actualWinnerCount)
	}

	winners := make(map[string]interface{})

	// 5️⃣ 동적 수상자 선정
	if actualWinnerCount >= 1 {
		winners["first"] = WinnerInfo{
			CreatorID:   validCreators[0].CreatorID,
			CreatorName: validCreators[0].CreatorName,
			ViewCount:   validCreators[0].TotalViews,
			Prize:       prizeFirst,
		}
		log.Printf("🥇 1등: %s - 조회수 %d, 상금 %.0f원", validCreators[0].CreatorName, validCreators[0].TotalViews, prizeFirst)
	}

	if actualWinnerCount >= 2 {
		winners["second"] = WinnerInfo{
			CreatorID:   validCreators[1].CreatorID,
			CreatorName: validCreators[1].CreatorName,
			ViewCount:   validCreators[1].TotalViews,
			Prize:       prizeSecond,
		}
		log.Printf("🥈 2등: %s - 조회수 %d, 상금 %.0f원", validCreators[1].CreatorName, validCreators[1].TotalViews, prizeSecond)
	}

	if actualWinnerCount >= 3 {
		winners["third"] = WinnerInfo{
			CreatorID:   validCreators[2].CreatorID,
			CreatorName: validCreators[2].CreatorName,
			ViewCount:   validCreators[2].TotalViews,
			Prize:       prizeThird,
		}
		log.Printf("🥉 3등: %s - 조회수 %d, 상금 %.0f원", validCreators[2].CreatorName, validCreators[2].TotalViews, prizeThird)
	}

	// 4등 이상 처리 (나머지 금액 균등 분배)
	if actualWinnerCount > 3 {
		remainderPrize := totalPrize - prizeFirst - prizeSecond - prizeThird
		remainingWinners := actualWinnerCount - 3
		prizePerPerson := 0.0
		if remainingWinners > 0 {
			prizePerPerson = remainderPrize / float64(remainingWinners)
		}

		for i := 3; i < actualWinnerCount; i++ {
			rankKey := fmt.Sprintf("rank%d", i+1)
			winners[rankKey] = WinnerInfo{
				CreatorID:   validCreators[i].CreatorID,
				CreatorName: validCreators[i].CreatorName,
				ViewCount:   validCreators[i].TotalViews,
				Prize:       prizePerPerson,
			}
			log.Printf("🏅 %d등: %s - 조회수 %d, 상금 %.0f원", i+1, validCreators[i].CreatorName, validCreators[i].TotalViews, prizePerPerson)
		}
	}

	// 6️⃣ 최종 통계 계산 및 저장
	totalViews := int64(0)
	totalLikes := int64(0)
	totalComments := int64(0)

	for _, sub := range submissions {
		totalViews += sub.CurrentViewCount
		// YouTube 상세 통계는 youtubeData에서 가져오기
		// (이미 updateYouTubeViewCounts에서 업데이트됨)
	}

	averageViews := float64(0)
	if len(submissions) > 0 {
		averageViews = float64(totalViews) / float64(len(submissions))
	}

	// Analytics 문서 생성
	analyticsData := map[string]interface{}{
		"totalViews":       totalViews,
		"totalSubmissions": len(submissions),
		"totalLikes":       totalLikes,
		"totalComments":    totalComments,
		"averageViews":     averageViews,
		"engagementRate":   0, // 계산 필요시 추가
		"createdAt":        now,
	}

	db.Collection("competitions").
		Doc(competitionID).
		Collection("analytics").
		Doc("final").
		Set(ctx, analyticsData)

	// 7️⃣ 대회 문서 업데이트 (상태 + 우승자 + ⭐ 최종 통계)
	log.Printf("\n💾 대회 정보 저장 중...")
	log.Printf("  - status: FINISHED")
	log.Printf("  - winners: %d명", len(winners))
	log.Printf("  - finalDataCollected: true")
	log.Printf("  - winnersUpdatedAt: %s", now.Format("2006-01-02 15:04:05"))
	
	// ⭐ 최종 참가자 수 조회
	participantsCount, err := s.getParticipantsCountWithDB(ctx, competitionID, db)
	if err != nil {
		log.Printf("⚠️ 참가자 수 조회 실패: %v (기본값 0 사용)", err)
		participantsCount = 0
	}
	
	updateData := []firestore.Update{
		{Path: "status", Value: "FINISHED"},
		{Path: "finishedAt", Value: now},
		{Path: "updatedAt", Value: now},
		{Path: "winners", Value: winners},
		{Path: "winnersUpdatedAt", Value: now},
		{Path: "finalDataCollected", Value: true},
		// ⭐ 최종 통계 (FINISHED 대회는 여기서 한 번만 저장)
		{Path: "stats.participants", Value: participantsCount},
		{Path: "stats.submissions", Value: len(submissions)},
		{Path: "stats.totalViews", Value: totalViews},
		{Path: "stats.averageViews", Value: averageViews},
		{Path: "stats.lastUpdated", Value: now},
	}

	_, err = db.Collection("competitions").Doc(competitionID).Update(ctx, updateData)
	if err != nil {
		log.Printf("❌ Firestore 업데이트 실패: %v", err)
		return err
	}
	
	log.Printf("\n========================================")
	log.Printf("✅ 대회 종료 처리 완료: %s", competitionID)
	log.Printf("========================================\n")
	
	return nil
}

// RetryFailedFinalizations - 최종 데이터 미수집 대회 재처리 (오전 2시)
func (s *StatsService) RetryFailedFinalizations() error {
	ctx := context.Background()

	// finalDataCollected가 false인 FINISHED 대회 조회
	iter := s.firestore.Collection("competitions").
		Where("status", "==", "FINISHED").
		Where("finalDataCollected", "==", false).
		Where("deleted", "==", false).
		Documents(ctx)

	retryCount := 0
	successCount := 0

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ 재처리 대회 조회 오류: %v", err)
			continue
		}

		retryCount++
		data := doc.Data()
		competitionID := doc.Ref.ID
		competitionName := getStringFromData(data, "title")

		// 최종 데이터 수집만 다시 시도
		if err := s.retryFinalDataCollection(ctx, competitionID, competitionName); err == nil {
			successCount++
		}
	}

	return nil
}

// retryFinalDataCollection - 최종 데이터 수집 재시도
func (s *StatsService) retryFinalDataCollection(ctx context.Context, competitionID string, competitionName string) error {
	// Submissions 조회
	submissions, err := s.getCompetitionSubmissions(ctx, competitionID)
	if err != nil {
		return fmt.Errorf("submissions 조회 실패: %v", err)
	}

	// YouTube 데이터 수집
	if s.youtube != nil {
		if err := s.updateYouTubeViewCounts(ctx, submissions); err != nil {
			return fmt.Errorf("YouTube 데이터 수집 실패: %v", err)
		}
	}

	// finalDataCollected 플래그 업데이트
	_, err = s.firestore.Collection("competitions").Doc(competitionID).Update(ctx, []firestore.Update{
		{Path: "finalDataCollected", Value: true},
		{Path: "updatedAt", Value: time.Now()},
	})

	return err
}

// ==================== 기존 통계 업데이트 메서드 (유지) ====================

// 모든 활성 대회의 통계 업데이트 (⭐ APPROVED, ONGOING만 - FINISHED는 종료 시 한 번만)
func (s *StatsService) UpdateAllActiveCompetitions() error {
	ctx := context.Background()

	// ⭐ APPROVED(참가자만), ONGOING만 조회 - FINISHED는 FinalizeCompetition()에서 한 번만 처리
	statuses := []string{"APPROVED", "ONGOING"}
	totalCount := 0
	totalSuccess := 0

	for _, status := range statuses {
		log.Printf("[%s] 대회 통계 업데이트 시작...", status)
		
		iter := s.firestore.Collection("competitions").
			Where("status", "==", status).
			Where("deleted", "==", false).
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
			if err := s.UpdateCompetitionStats(competitionID); err == nil {
				successCount++
			} else {
				log.Printf("⚠️ [%s] %s 통계 업데이트 실패: %v", status, competitionID, err)
			}
		}

		log.Printf("✅ [%s] %d/%d 대회 통계 업데이트 완료", status, successCount, count)
		totalCount += count
		totalSuccess += successCount
	}

	log.Printf("🏆 전체 %d/%d 대회 통계 업데이트 완료", totalSuccess, totalCount)
	return nil
}

// 특정 대회의 통계 업데이트
func (s *StatsService) UpdateCompetitionStats(competitionID string) error {
	ctx := context.Background()

	// 1. 해당 대회의 모든 submissions 조회
	submissions, err := s.getCompetitionSubmissions(ctx, competitionID)
	if err != nil {
		return fmt.Errorf("submissions 조회 실패: %v", err)
	}

	if len(submissions) == 0 {
		return s.updateCompetitionStatsInFirestore(ctx, competitionID, CompetitionStats{
			TotalSubmissions: 0,
			TotalViews:       0,
			UniqueCreators:   0,
			AverageViews:     0,
			LastUpdated:      time.Now(),
		})
	}

	// 2. YouTube 영상들의 조회수 업데이트
	s.updateYouTubeViewCounts(ctx, submissions)

	// 3. 통계 계산
	stats := s.calculateCompetitionStats(submissions)

	// 4. Firebase에 통계 저장
	return s.updateCompetitionStatsInFirestore(ctx, competitionID, stats)
}

// 대회의 모든 submissions 조회 (삭제되지 않은 영상만)
func (s *StatsService) getCompetitionSubmissions(ctx context.Context, competitionID string) ([]SubmissionData, error) {
	log.Printf("📋 대회 submissions 조회 시작: %s", competitionID)
	
	iter := s.firestore.Collection("competitions").
		Doc(competitionID).
		Collection("submissions").
		Where("isDeleted", "==", "n"). // ⭐ 삭제되지 않은 영상만
		Documents(ctx)

	var submissions []SubmissionData
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ submissions 조회 오류: %v", err)
			return nil, err
		}

		data := doc.Data()
		
		// ⭐ currentViewCount 또는 viewCount 중 큰 값 사용
		viewCount := getInt64FromData(data, "currentViewCount")
		if viewCount == 0 {
			viewCount = getInt64FromData(data, "viewCount")
		}
		
		// ⭐ platform 판단 (youtubeData 있으면 youtube)
		platform := getStringFromData(data, "platform")
		if platform == "" {
			if _, hasYouTubeData := data["youtubeData"]; hasYouTubeData {
				platform = "youtube"
			}
		}
		
		// ⭐ videoId 추출 (여러 위치 체크)
		videoID := getStringFromData(data, "videoId")
		if videoID == "" && platform == "youtube" {
			if youtubeData, ok := data["youtubeData"].(map[string]interface{}); ok {
				// videoId 또는 id 체크
				if vid, ok := youtubeData["videoId"].(string); ok {
					videoID = vid
				} else if vid, ok := youtubeData["id"].(string); ok {
					videoID = vid
				}
			}
		}
		
		submission := SubmissionData{
			ID:               doc.Ref.ID,
			CompetitionID:    competitionID,
			CreatorID:        getStringFromData(data, "creatorId"),
			CreatorName:      getStringFromData(data, "creatorName"),
			Platform:         platform,
			VideoID:          videoID,
			CurrentViewCount: viewCount,
		}
		
		log.Printf("  - 영상 발견: %s (%s) - 조회수 %d", submission.CreatorName, submission.VideoID, submission.CurrentViewCount)

		submissions = append(submissions, submission)
	}
	
	log.Printf("✅ 총 %d개 submissions 조회 완료", len(submissions))

	return submissions, nil
}

// YouTube 영상들의 조회수 업데이트
func (s *StatsService) updateYouTubeViewCounts(ctx context.Context, submissions []SubmissionData) error {
	if s.youtube == nil {
		return fmt.Errorf("YouTube API 서비스가 초기화되지 않음")
	}

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
		s.updateYouTubeViewCountsBatch(ctx, batch, youtubeSubmissions)
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
	
	// ⭐ Realtime DB 업데이트를 위한 크리에이터 추적
	affectedCreators := make(map[string]map[string]bool) // competitionID -> creatorID set

	for _, video := range response.Items {
		if submission, exists := submissions[video.Id]; exists {
			viewCount := int64(video.Statistics.ViewCount)
			likeCount := int64(video.Statistics.LikeCount)
			commentCount := int64(video.Statistics.CommentCount)

			// submissions 문서 업데이트
			docRef := s.firestore.Collection("competitions").
				Doc(submission.CompetitionID).
				Collection("submissions").
				Doc(submission.ID)

			updates := []firestore.Update{
				{Path: "currentViewCount", Value: viewCount},
				{Path: "lastUpdatedAt", Value: time.Now()},
			}

			// YouTube 플랫폼인 경우 추가 필드 업데이트
			if submission.Platform == "youtube" {
				updates = append(updates, firestore.Update{
					Path:  "youtubeData.statistics.viewCount",
					Value: viewCount,
				})

				// 좋아요, 댓글 수도 업데이트
				if likeCount > 0 {
					updates = append(updates, firestore.Update{
						Path:  "youtubeData.statistics.likeCount",
						Value: likeCount,
					})
				}
				if commentCount > 0 {
					updates = append(updates, firestore.Update{
						Path:  "youtubeData.statistics.commentCount",
						Value: commentCount,
					})
				}
			}

			batch.Update(docRef, updates)
			
			// ⭐ Realtime DB에도 업데이트
			if s.realtimeDB != nil {
				realtimeData := map[string]interface{}{
					"creatorId":    submission.CreatorID,
					"creatorName":  submission.CreatorName,
					"platform":     submission.Platform,
					"videoId":      submission.VideoID,
					"viewCount":    viewCount,
					"likeCount":    likeCount,
					"commentCount": commentCount,
					"lastUpdated":  time.Now().Unix(),
				}
				
				if err := s.updateRealtimeSubmission(ctx, submission.CompetitionID, submission.ID, realtimeData); err != nil {
					log.Printf("⚠️ Realtime DB submission 업데이트 실패 [%s]: %v", submission.ID, err)
				}
				
				// 영향받은 크리에이터 추적
				if affectedCreators[submission.CompetitionID] == nil {
					affectedCreators[submission.CompetitionID] = make(map[string]bool)
				}
				affectedCreators[submission.CompetitionID][submission.CreatorID] = true
			}
		}
	}

	// 배치 실행 (Firestore)
	_, err = batch.Commit(ctx)
	if err != nil {
		return err
	}
	
	// ⭐ 영향받은 크리에이터들의 Realtime DB 리더보드 업데이트
	if s.realtimeDB != nil {
		for competitionID, creators := range affectedCreators {
			for creatorID := range creators {
				if err := s.updateRealtimeLeaderboard(ctx, competitionID, creatorID); err != nil {
					log.Printf("⚠️ Realtime DB leaderboard 업데이트 실패 [%s/%s]: %v", competitionID, creatorID, err)
				}
			}
		}
	}
	
	return nil
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

// Firebase에 통계 저장 (⭐ 수정: 참가자, 영상수, 조회수 제대로 반영)
func (s *StatsService) updateCompetitionStatsInFirestore(ctx context.Context, competitionID string, stats CompetitionStats) error {
	docRef := s.firestore.Collection("competitions").Doc(competitionID)

	// ⭐ participants 컬렉션에서 실제 참가자 수 조회
	participantsCount, err := s.getParticipantsCount(ctx, competitionID)
	if err != nil {
		log.Printf("⚠️ 참가자 수 조회 실패: %v (기본값 0 사용)", err)
		participantsCount = 0
	}

	updates := []firestore.Update{
		{Path: "stats", Value: map[string]interface{}{
			"participants":     participantsCount,    // ⭐ 실제 참가자 수
			"submissions":      stats.TotalSubmissions, // ⭐ 영상 수
			"totalViews":       stats.TotalViews,       // ⭐ 조회수
			"uniqueCreators":   stats.UniqueCreators,
			"averageViews":     stats.AverageViews,
			"lastUpdated":      stats.LastUpdated,
		}},
		{Path: "participantCount", Value: participantsCount}, // 하위 호환성
		{Path: "totalViews", Value: float64(stats.TotalViews)}, // 하위 호환성
	}

	_, err = docRef.Update(ctx, updates)
	return err
}

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

// ⭐ 참가자 수 조회 (participants 컬렉션)
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

// ==================== Realtime Database 함수들 ====================

// updateRealtimeSubmission - Realtime DB submission 업데이트
func (s *StatsService) updateRealtimeSubmission(ctx context.Context, competitionID, submissionID string, data map[string]interface{}) error {
	if s.realtimeDB == nil {
		return nil
	}

	ref := s.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions/%s", competitionID, submissionID))
	
	if err := ref.Set(ctx, data); err != nil {
		log.Printf("⚠️ Realtime DB submission 업데이트 실패 [%s]: %v", submissionID, err)
		return err
	}

	return nil
}

// updateRealtimeLeaderboard - Realtime DB leaderboard 업데이트
func (s *StatsService) updateRealtimeLeaderboard(ctx context.Context, competitionID, creatorID string) error {
	if s.realtimeDB == nil {
		return nil
	}

	// 1. 해당 크리에이터의 모든 submissions 조회
	submissionsRef := s.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions", competitionID))
	
	var allSubmissions map[string]map[string]interface{}
	if err := submissionsRef.Get(ctx, &allSubmissions); err != nil {
		log.Printf("⚠️ Realtime DB submissions 조회 실패: %v", err)
		return err
	}

	// 2. creatorID로 필터링하여 합산
	var totalViews int64 = 0
	var submissionCount int = 0
	var creatorName string
	var topVideo struct {
		SubmissionID string
		ViewCount    int64
	}

	for submissionID, submission := range allSubmissions {
		// creatorId 체크 (interface{} 타입 처리)
		subCreatorID, ok := submission["creatorId"].(string)
		if !ok || subCreatorID != creatorID {
			continue
		}

		// viewCount 추출
		var viewCount int64
		switch v := submission["viewCount"].(type) {
		case int64:
			viewCount = v
		case float64:
			viewCount = int64(v)
		case int:
			viewCount = int64(v)
		default:
			continue
		}

		totalViews += viewCount
		submissionCount++

		// creatorName 저장 (첫 번째 것만)
		if creatorName == "" {
			if name, ok := submission["creatorName"].(string); ok {
				creatorName = name
			}
		}

		// 최고 조회수 영상 찾기
		if viewCount > topVideo.ViewCount {
			topVideo.SubmissionID = submissionID
			topVideo.ViewCount = viewCount
		}
	}

	// 3. Leaderboard 업데이트
	leaderboardRef := s.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/leaderboard/%s", competitionID, creatorID))
	
	leaderboardData := map[string]interface{}{
		"creatorName":     creatorName,
		"totalViews":      totalViews,
		"submissionCount": submissionCount,
		"lastUpdated":     time.Now().Unix(),
	}

	if topVideo.SubmissionID != "" {
		leaderboardData["topVideo"] = map[string]interface{}{
			"submissionId": topVideo.SubmissionID,
			"viewCount":    topVideo.ViewCount,
		}
	}

	if err := leaderboardRef.Set(ctx, leaderboardData); err != nil {
		log.Printf("⚠️ Realtime DB leaderboard 업데이트 실패 [%s]: %v", creatorID, err)
		return err
	}

	log.Printf("✅ Leaderboard 업데이트: %s - 조회수 %d (영상 %d개)", creatorName, totalViews, submissionCount)
	return nil
}

// cleanupRealtimeData - 대회 종료 시 Realtime DB 데이터 삭제
func (s *StatsService) cleanupRealtimeData(ctx context.Context, competitionID string) error {
	if s.realtimeDB == nil {
		return nil
	}

	ref := s.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s", competitionID))
	
	if err := ref.Delete(ctx); err != nil {
		log.Printf("⚠️ Realtime DB 정리 실패 [%s]: %v", competitionID, err)
		return err
	}

	log.Printf("✅ Realtime DB 정리 완료: %s", competitionID)
	return nil
}

// SaveDailyAggregation - 일별 데이터 집계
func (s *StatsService) SaveDailyAggregation() error {
	// TODO: 일별 데이터 집계 구현
	return nil
}

// ⭐ 참가자 수 조회 (특정 DB 사용)
func (s *StatsService) getParticipantsCountWithDB(ctx context.Context, competitionID string, db *firestore.Client) (int, error) {
	iter := db.Collection("competitions").
		Doc(competitionID).
		Collection("participants").
		Where("status", "==", "accepted").
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

// ⭐ submissions 조회 (특정 DB 사용)
func (s *StatsService) getCompetitionSubmissionsWithDB(ctx context.Context, competitionID string, db *firestore.Client) ([]SubmissionData, error) {
	log.Printf("📋 대회 submissions 조회 시작: %s", competitionID)

	iter := db.Collection("competitions").
		Doc(competitionID).
		Collection("submissions").
		Where("isDeleted", "==", "n").
		Documents(ctx)

	var submissions []SubmissionData
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ submissions 조회 오류: %v", err)
			return nil, err
		}

		data := doc.Data()

		viewCount := getInt64FromData(data, "currentViewCount")
		if viewCount == 0 {
			viewCount = getInt64FromData(data, "viewCount")
		}

		platform := getStringFromData(data, "platform")
		if platform == "" {
			if _, hasYouTubeData := data["youtubeData"]; hasYouTubeData {
				platform = "youtube"
			}
		}

		videoID := getStringFromData(data, "videoId")
		if videoID == "" && platform == "youtube" {
			if youtubeData, ok := data["youtubeData"].(map[string]interface{}); ok {
				if vid, ok := youtubeData["videoId"].(string); ok {
					videoID = vid
				} else if vid, ok := youtubeData["id"].(string); ok {
					videoID = vid
				}
			}
		}

		submission := SubmissionData{
			ID:               doc.Ref.ID,
			CompetitionID:    competitionID,
			CreatorID:        getStringFromData(data, "creatorId"),
			CreatorName:      getStringFromData(data, "creatorName"),
			Platform:         platform,
			VideoID:          videoID,
			CurrentViewCount: viewCount,
		}

		submissions = append(submissions, submission)
	}

	log.Printf("✅ 총 %d개 submissions 조회 완료", len(submissions))
	return submissions, nil
}


