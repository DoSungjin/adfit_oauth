package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// ==================== 진행 중 대회 통계 업데이트 ====================

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

	// 2. 플랫폼별 조회수 업데이트
	s.updateYouTubeViewCounts(ctx, submissions)
	s.updateInstagramViewCounts(ctx, competitionID, submissions)
	s.updateTikTokViewCounts(ctx, competitionID, submissions)

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
			ID:                doc.Ref.ID,
			CompetitionID:     competitionID,
			CreatorID:         getStringFromData(data, "creatorId"),
			CreatorName:       getStringFromData(data, "creatorName"),
			Platform:          platform,
			VideoID:           videoID,
			CurrentViewCount:  viewCount,
			LikeCount:         getInt64FromData(data, "likeCount"),
			ShareCount:        getInt64FromData(data, "shareCount"),
			SavedCount:        getInt64FromData(data, "savedCount"),
			CommentCount:      getInt64FromData(data, "commentCount"),
			EstimatedEarnings: getInt64FromData(data, "estimatedEarnings"),
		}

		log.Printf("  - 영상 발견: %s (%s) - 조회수 %d", submission.CreatorName, submission.VideoID, submission.CurrentViewCount)

		submissions = append(submissions, submission)
	}

	log.Printf("✅ 총 %d개 submissions 조회 완료", len(submissions))

	return submissions, nil
}

// 통계 계산
func (s *StatsService) calculateCompetitionStats(submissions []SubmissionData) CompetitionStats {
	totalSubmissions := len(submissions)
	var totalViews int64
	var totalEstimatedCost int64 // ⭐ 성과형
	creatorSet := make(map[string]bool)

	for _, sub := range submissions {
		totalViews += sub.CurrentViewCount
		totalEstimatedCost += sub.EstimatedEarnings // ⭐ 성과형
		creatorSet[sub.CreatorID] = true
	}

	uniqueCreators := len(creatorSet)
	averageViews := float64(0)
	if totalSubmissions > 0 {
		averageViews = float64(totalViews) / float64(totalSubmissions)
	}

	return CompetitionStats{
		TotalSubmissions:   totalSubmissions,
		TotalViews:         totalViews,
		UniqueCreators:     uniqueCreators,
		AverageViews:       averageViews,
		TotalEstimatedCost: totalEstimatedCost, // ⭐ 성과형
		LastUpdated:        time.Now(),
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
			"participants":       participantsCount,        // ⭐ 실제 참가자 수
			"submissions":        stats.TotalSubmissions,   // ⭐ 영상 수
			"totalViews":         stats.TotalViews,         // ⭐ 조회수
			"totalEstimatedCost": stats.TotalEstimatedCost, // ⭐ 성과형 총 예상 비용
			"uniqueCreators":     stats.UniqueCreators,
			"averageViews":       stats.AverageViews,
			"lastUpdated":        stats.LastUpdated,
		}},
		{Path: "participantCount", Value: participantsCount},   // 하위 호환성
		{Path: "totalViews", Value: float64(stats.TotalViews)}, // 하위 호환성
	}

	_, err = docRef.Update(ctx, updates)
	return err
}
