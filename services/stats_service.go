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

// ==================== 대회 상태 자동 관리 ====================

// CheckAndStartApprovedCompetitions - APPROVED → ONGOING 전환 (자정 12시)
func (s *StatsService) CheckAndStartApprovedCompetitions() error {
	ctx := context.Background()
	now := time.Now()

	log.Println("🌙 [자정 12시] APPROVED → ONGOING 체크 시작")

	// APPROVED 상태 대회 조회
	iter := s.firestore.Collection("competitions").
		Where("status", "==", "APPROVED").
		Where("deleted", "==", false).
		Documents(ctx)

	startedCount := 0
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
		competitionID := doc.Ref.ID
		competitionName := getStringFromData(data, "title")

		// startDate 확인
		startDate, ok := data["startDate"].(time.Time)
		if !ok {
			log.Printf("⚠️ 대회 %s: startDate 필드 없음", competitionName)
			continue
		}

		// startDate가 현재 시간보다 이전이거나 같으면 시작
		if startDate.Before(now) || startDate.Equal(now) {
			log.Printf("🚀 대회 시작: %s (ID: %s)", competitionName, competitionID)
			log.Printf("   - startDate: %s", startDate.Format("2006-01-02 15:04:05"))
			log.Printf("   - 현재시간: %s", now.Format("2006-01-02 15:04:05"))

			// ONGOING으로 상태 변경
			_, err := doc.Ref.Update(ctx, []firestore.Update{
				{Path: "status", Value: "ONGOING"},
				{Path: "startedAt", Value: now},
				{Path: "updatedAt", Value: now},
			})

			if err != nil {
				log.Printf("❌ 대회 시작 처리 실패: %v", err)
			} else {
				startedCount++
				log.Printf("✅ 대회 시작 완료: %s", competitionName)
			}
		}
	}

	log.Printf("✅ APPROVED → ONGOING 체크 완료: %d개 대회 시작됨", startedCount)
	return nil
}

// CheckAndFinishOngoingCompetitions - ONGOING → FINISHED 전환 + 최종 데이터 수집 (오전 1시)
func (s *StatsService) CheckAndFinishOngoingCompetitions() error {
	ctx := context.Background()
	now := time.Now()

	log.Println("🌅 [오전 1시] ONGOING → FINISHED 체크 시작")

	// ONGOING 상태 대회 조회
	iter := s.firestore.Collection("competitions").
		Where("status", "==", "ONGOING").
		Where("deleted", "==", false).
		Documents(ctx)

	finishedCount := 0
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
		competitionName := getStringFromData(data, "title")

		// deadline 확인
		deadline, ok := data["deadline"].(time.Time)
		if !ok {
			log.Printf("⚠️ 대회 %s: deadline 필드 없음", competitionName)
			continue
		}

		// deadline이 지났으면 종료 처리
		if deadline.Before(now) {
			log.Printf("🏁 대회 종료 처리 시작: %s (ID: %s)", competitionName, competitionID)
			log.Printf("   - deadline: %s", deadline.Format("2006-01-02 15:04:05"))
			log.Printf("   - 현재시간: %s", now.Format("2006-01-02 15:04:05"))

			// 종료 처리 (YouTube 데이터 수집 + 우승자 선정)
			if err := s.finalizeCompetition(ctx, competitionID, competitionName); err != nil {
				log.Printf("❌ 대회 종료 처리 실패: %s - %v", competitionName, err)
			} else {
				finishedCount++
				log.Printf("✅ 대회 종료 완료: %s", competitionName)
			}
		}
	}

	log.Printf("✅ ONGOING → FINISHED 체크 완료: %d개 대회 종료됨", finishedCount)
	return nil
}

func (s *StatsService) CheckAndCancelPendingCompetitions() error {
	ctx := context.Background()
	now := time.Now()
	oneWeekLater := now.AddDate(0, 0, 7) // 1주일 후

	log.Println("🚫 미승인 대회 취소 체크 시작")

	// REGISTERED, NOTICED, UNDERREVIEW 상태 대회 조회
	pendingStatuses := []string{"REGISTERED", "NOTICED", "UNDERREVIEW"}
	canceledCount := 0

	for _, status := range pendingStatuses {
		iter := s.firestore.Collection("competitions").
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
			competitionID := doc.Ref.ID
			competitionName := getStringFromData(data, "title")

			// startDate 확인
			startDate, ok := data["startDate"].(time.Time)
			if !ok {
				log.Printf("⚠️ 대회 %s: startDate 필드 없음", competitionName)
				continue
			}

			// 개최일이 1주일 이내면 취소
			if startDate.Before(oneWeekLater) {
				log.Printf("🚫 대회 자동 취소: %s (ID: %s, 상태: %s)", competitionName, competitionID, status)
				log.Printf("   - startDate: %s", startDate.Format("2006-01-02"))
				log.Printf("   - 현재+7일: %s", oneWeekLater.Format("2006-01-02"))

				// CANCELLED로 상태 변경
				_, err := doc.Ref.Update(ctx, []firestore.Update{
					{Path: "status", Value: "CANCELLED"},
					{Path: "cancelledAt", Value: now},
					{Path: "cancelReason", Value: "자동 취소: 승인 미완료"},
					{Path: "updatedAt", Value: now},
				})

				if err != nil {
					log.Printf("❌ 대회 취소 처리 실패: %v", err)
				} else {
					canceledCount++
					log.Printf("✅ 대회 취소 완료: %s", competitionName)
				}
			}
		}
	}

	log.Printf("✅ 미승인 대회 취소 체크 완료: %d개 대회 취소됨", canceledCount)
	return nil
}

// finalizeCompetition - 대회 종료 처리 (내부 메서드)
func (s *StatsService) finalizeCompetition(ctx context.Context, competitionID string, competitionName string) error {
	now := time.Now()

	// 1️⃣ Submissions 조회
	log.Printf("📥 1/4: Submissions 조회 중...")
	submissions, err := s.getCompetitionSubmissions(ctx, competitionID)
	if err != nil {
		return fmt.Errorf("submissions 조회 실패: %v", err)
	}

	if len(submissions) == 0 {
		log.Printf("ℹ️ 제출된 영상이 없습니다. 우승자 없이 종료 처리합니다.")

		// 제출 없이 종료
		_, err := s.firestore.Collection("competitions").Doc(competitionID).Update(ctx, []firestore.Update{
			{Path: "status", Value: "FINISHED"},
			{Path: "finishedAt", Value: now},
			{Path: "updatedAt", Value: now},
			{Path: "finalDataCollected", Value: true},
		})
		return err
	}

	log.Printf("📊 총 %d개 제출 영상", len(submissions))

	// 2️⃣ YouTube 최종 조회수 수집
	log.Printf("🎬 2/4: YouTube 최종 조회수 수집 중...")
	if s.youtube != nil {
		if err := s.updateYouTubeViewCounts(ctx, submissions); err != nil {
			log.Printf("⚠️ YouTube 조회수 업데이트 실패 (기존 데이터 사용): %v", err)
			// 실패해도 계속 진행
		} else {
			log.Printf("✅ YouTube 조회수 업데이트 완료")
		}
	} else {
		log.Printf("⚠️ YouTube API 미설정 (기존 조회수 데이터 사용)")
	}

	// 3️⃣ 우승자 선정 (조회수 기준 정렬)
	log.Printf("🏆 3/4: 우승자 선정 중...")
	sort.Slice(submissions, func(i, j int) bool {
		return submissions[i].CurrentViewCount > submissions[j].CurrentViewCount
	})

	winners := make(map[string]interface{})

	// 1등 (50%)
	if len(submissions) > 0 {
		winners["first"] = WinnerInfo{
			CreatorID:   submissions[0].CreatorID,
			CreatorName: submissions[0].CreatorName,
			ViewCount:   submissions[0].CurrentViewCount,
			Prize:       0.5,
		}
		log.Printf("🥇 1등: %s (%d views)", submissions[0].CreatorName, submissions[0].CurrentViewCount)
	}

	// 2등 (30%)
	if len(submissions) > 1 {
		winners["second"] = WinnerInfo{
			CreatorID:   submissions[1].CreatorID,
			CreatorName: submissions[1].CreatorName,
			ViewCount:   submissions[1].CurrentViewCount,
			Prize:       0.3,
		}
		log.Printf("🥈 2등: %s (%d views)", submissions[1].CreatorName, submissions[1].CurrentViewCount)
	}

	// 3등 (20%)
	if len(submissions) > 2 {
		winners["third"] = WinnerInfo{
			CreatorID:   submissions[2].CreatorID,
			CreatorName: submissions[2].CreatorName,
			ViewCount:   submissions[2].CurrentViewCount,
			Prize:       0.2,
		}
		log.Printf("🥉 3등: %s (%d views)", submissions[2].CreatorName, submissions[2].CurrentViewCount)
	}

	// 4️⃣ 최종 통계 계산 및 저장
	log.Printf("📊 4/4: 최종 통계 계산 중...")
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

	_, err = s.firestore.Collection("competitions").
		Doc(competitionID).
		Collection("analytics").
		Doc("final").
		Set(ctx, analyticsData)

	if err != nil {
		log.Printf("⚠️ Analytics 저장 실패: %v", err)
		// 실패해도 계속 진행
	}

	// 5️⃣ 대회 문서 업데이트 (상태 + 우승자)
	updateData := []firestore.Update{
		{Path: "status", Value: "FINISHED"},
		{Path: "finishedAt", Value: now},
		{Path: "updatedAt", Value: now},
		{Path: "winners", Value: winners},
		{Path: "finalDataCollected", Value: true},
		{Path: "stats.totalViews", Value: totalViews},
		{Path: "stats.totalSubmissions", Value: len(submissions)},
		{Path: "stats.averageViews", Value: averageViews},
	}

	_, err = s.firestore.Collection("competitions").Doc(competitionID).Update(ctx, updateData)
	if err != nil {
		return fmt.Errorf("대회 문서 업데이트 실패: %v", err)
	}

	log.Printf("🎉 대회 종료 처리 완료!")
	log.Printf("   - 총 조회수: %d", totalViews)
	log.Printf("   - 평균 조회수: %.0f", averageViews)

	return nil
}

// RetryFailedFinalizations - 최종 데이터 미수집 대회 재처리 (오전 2시)
func (s *StatsService) RetryFailedFinalizations() error {
	ctx := context.Background()

	log.Println("🔄 [오전 2시] 최종 데이터 미수집 대회 재처리 시작")

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

		log.Printf("🔄 재처리 시작: %s (ID: %s)", competitionName, competitionID)

		// 최종 데이터 수집만 다시 시도
		if err := s.retryFinalDataCollection(ctx, competitionID, competitionName); err != nil {
			log.Printf("❌ 재처리 실패: %s - %v", competitionName, err)
		} else {
			successCount++
			log.Printf("✅ 재처리 성공: %s", competitionName)
		}
	}

	log.Printf("✅ 재처리 완료: %d개 중 %d개 성공", retryCount, successCount)
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

// 모든 활성 대회의 통계 업데이트
func (s *StatsService) UpdateAllActiveCompetitions() error {
	ctx := context.Background()

	log.Println("🔄 활성 대회 통계 업데이트 시작")

	// ✅ ONGOING 상태 대회만 조회 (새로운 상태 체계)
	iter := s.firestore.Collection("competitions").
		Where("status", "==", "ONGOING").
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
		if err := s.UpdateCompetitionStats(competitionID); err != nil {
			log.Printf("❌ 대회 %s 통계 업데이트 실패: %v", competitionID, err)
		} else {
			successCount++
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
			ID:               doc.Ref.ID,
			CompetitionID:    competitionID,
			CreatorID:        getStringFromData(data, "creatorId"),
			CreatorName:      getStringFromData(data, "creatorName"),
			Platform:         getStringFromData(data, "platform"),
			VideoID:          getStringFromData(data, "videoId"),
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

	log.Printf("🎬 YouTube 영상 %d개 조회수 업데이트 중...", len(youtubeVideoIDs))

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
				if video.Statistics.LikeCount > 0 {
					updates = append(updates, firestore.Update{
						Path:  "youtubeData.statistics.likeCount",
						Value: video.Statistics.LikeCount,
					})
				}
				if video.Statistics.CommentCount > 0 {
					updates = append(updates, firestore.Update{
						Path:  "youtubeData.statistics.commentCount",
						Value: video.Statistics.CommentCount,
					})
				}
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
	if err != nil {
		return fmt.Errorf("시스템 통계 저장 실패: %v", err)
	}

	log.Printf("✅ %s 시스템 통계 업데이트 완료 - 대회: %d, 사용자: %d, 총 상금: %.0f",
		today, totalCompetitions, totalUsers, totalPrizeAmount)

	return nil
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


