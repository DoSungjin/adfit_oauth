package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

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

	// ⭐ 성과형 대회 정보 (competitionType == "performance"면 view당 가격 기반)
	competitionType := getStringFromData(competitionData, "competitionType")
	pricePerView := getInt64FromData(competitionData, "pricePerView")
	minViews := getInt64FromData(competitionData, "minViews")
	isPerformance := competitionType == "performance"

	// ⭐ 수상자 상금 계산 헬퍼
	// - 성과형: totalViews × pricePerView (minViews 이상일 때만 지급)
	// - 랭킹형: rank별 고정 상금 (prizeFirst/Second/Third, 4등+ 균등 분배)
	calcWinnerPrize := func(totalViews int64, rank, winnerCountForSplit int) float64 {
		if isPerformance {
			if totalViews >= minViews {
				return float64(totalViews * pricePerView)
			}
			return 0
		}
		switch rank {
		case 1:
			return prizeFirst
		case 2:
			return prizeSecond
		case 3:
			return prizeThird
		default:
			remainder := totalPrize - prizeFirst - prizeSecond - prizeThird
			remaining := winnerCountForSplit - 3
			if remaining > 0 {
				return remainder / float64(remaining)
			}
			return 0
		}
	}

	log.Printf("✅ 대회 정보: 총 상금 %.0f원, 수상자 수 %d명, 유형 %s", totalPrize, winnerCount, competitionType)
	if isPerformance {
		log.Printf("  - 성과형: %d원/view, 최소 %d views", pricePerView, minViews)
	} else {
		log.Printf("  - 1등 상금: %.0f원", prizeFirst)
		log.Printf("  - 2등 상금: %.0f원", prizeSecond)
		log.Printf("  - 3등 상금: %.0f원", prizeThird)
	}

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

	// ⭐ 실제 수상자 수 결정 (성과형/랭킹형 분기)
	var actualWinnerCount int
	if isPerformance {
		// 성과형: minViews 이상 달성한 전원이 수상자 (winnerCount 상한 무시)
		for _, c := range validCreators {
			if c.TotalViews >= minViews {
				actualWinnerCount++
			}
		}
		log.Printf("🎯 성과형 수상자 수: %d명 (minViews %d 이상 달성, 유효 참가자 %d명 중)", actualWinnerCount, minViews, len(validCreators))
	} else {
		// 랭킹형: top N (winnerCount)
		actualWinnerCount = winnerCount
		if len(validCreators) < winnerCount {
			actualWinnerCount = len(validCreators)
			log.Printf("⚠️ 수상자 수 조정: %d명 → %d명 (유효 참가자 부족)", winnerCount, actualWinnerCount)
		}
	}

	winners := make(map[string]interface{})

	// 5️⃣ 동적 수상자 선정 (성과형/랭킹형 분기는 calcWinnerPrize에서 처리)
	if actualWinnerCount >= 1 {
		prize := calcWinnerPrize(validCreators[0].TotalViews, 1, actualWinnerCount)
		winners["first"] = WinnerInfo{
			CreatorID:   validCreators[0].CreatorID,
			CreatorName: validCreators[0].CreatorName,
			ViewCount:   validCreators[0].TotalViews,
			Prize:       prize,
		}
		log.Printf("🥇 1등: %s - 조회수 %d, 상금 %.0f원", validCreators[0].CreatorName, validCreators[0].TotalViews, prize)
	}

	if actualWinnerCount >= 2 {
		prize := calcWinnerPrize(validCreators[1].TotalViews, 2, actualWinnerCount)
		winners["second"] = WinnerInfo{
			CreatorID:   validCreators[1].CreatorID,
			CreatorName: validCreators[1].CreatorName,
			ViewCount:   validCreators[1].TotalViews,
			Prize:       prize,
		}
		log.Printf("🥈 2등: %s - 조회수 %d, 상금 %.0f원", validCreators[1].CreatorName, validCreators[1].TotalViews, prize)
	}

	if actualWinnerCount >= 3 {
		prize := calcWinnerPrize(validCreators[2].TotalViews, 3, actualWinnerCount)
		winners["third"] = WinnerInfo{
			CreatorID:   validCreators[2].CreatorID,
			CreatorName: validCreators[2].CreatorName,
			ViewCount:   validCreators[2].TotalViews,
			Prize:       prize,
		}
		log.Printf("🥉 3등: %s - 조회수 %d, 상금 %.0f원", validCreators[2].CreatorName, validCreators[2].TotalViews, prize)
	}

	// 4등 이상 처리
	if actualWinnerCount > 3 {
		for i := 3; i < actualWinnerCount; i++ {
			prize := calcWinnerPrize(validCreators[i].TotalViews, i+1, actualWinnerCount)
			rankKey := fmt.Sprintf("rank%d", i+1)
			winners[rankKey] = WinnerInfo{
				CreatorID:   validCreators[i].CreatorID,
				CreatorName: validCreators[i].CreatorName,
				ViewCount:   validCreators[i].TotalViews,
				Prize:       prize,
			}
			log.Printf("🏅 %d등: %s - 조회수 %d, 상금 %.0f원", i+1, validCreators[i].CreatorName, validCreators[i].TotalViews, prize)
		}
	}

	// 6️⃣ 최종 통계 계산
	totalViews := int64(0)
	totalComments := int64(0)

	for _, sub := range submissions {
		totalViews += sub.CurrentViewCount
		totalComments += sub.CommentCount
	}

	// ⭐ engagement (likeCount, savedCount, shareCount)는 Realtime DB에서 직접 합산
	// Firestore에는 currentViewCount만 있고, 정확한 최신값은 Realtime DB에 있음
	totalLikes, totalSaved, totalShares := s.getEngagementFromRealtimeDB(ctx, competitionID)

	averageViews := float64(0)
	if len(submissions) > 0 {
		averageViews = float64(totalViews) / float64(len(submissions))
	}

	// 전체 참여율 = (좋아요 + 댓글 + 저장 + 공유) / 조회수
	engagementRate := float64(0)
	if totalViews > 0 {
		totalEngagements := totalLikes + totalComments + totalSaved + totalShares
		engagementRate = float64(totalEngagements) / float64(totalViews) * 100
	}

	// Analytics 문서 생성 (YouTube 방식과 동일 구조)
	analyticsData := map[string]interface{}{
		"totalViews":       totalViews,
		"totalSubmissions": len(submissions),
		"totalLikes":       totalLikes,
		"totalComments":    totalComments,
		"totalShares":      totalShares,
		"totalSaved":       totalSaved, // Instagram 전용
		"averageViews":     averageViews,
		"engagementRate":   engagementRate,
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

	// 8️⃣ Instagram 수상자 체널 인사이트 수집 (Instagram 대회인 경우만)
	// 토큰이 살아있는 지금 호출해야 함 (나중에 7일 후 삭제됨)
	winnerInsights := s.collectInstagramWinnerInsights(ctx, competitionID, winners, submissions, db)
	if len(winnerInsights) > 0 {
		_, _ = db.Collection("competitions").Doc(competitionID).Update(ctx, []firestore.Update{
			{Path: "winnerChannelInsights", Value: winnerInsights},
		})
		log.Printf("✅ Instagram 체널 인사이트 저장: %d명", len(winnerInsights))
	}

	// ⭐ Realtime DB 정리 (주석처리 - 필요시 활성화)
	// 대회 종료 후 Realtime DB 데이터 삭제
	// if s.realtimeDB != nil {
	// 	if err := s.cleanupRealtimeData(ctx, competitionID); err != nil {
	// 		log.Printf("⚠️ Realtime DB 정리 실패 [%s]: %v", competitionID, err)
	// 		// 실패해도 대회 종료 처리는 계속 진행
	// 	}
	// }

	log.Printf("\n========================================")
	log.Printf("✅ 대회 종료 처리 완료: %s", competitionID)
	log.Printf("========================================\n")

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
			// ⭐ 참여 지표 — analytics/final engagementRate 계산에 사용
			LikeCount:         getInt64FromData(data, "likeCount"),
			ShareCount:        getInt64FromData(data, "shareCount"),
			SavedCount:        getInt64FromData(data, "savedCount"),
			CommentCount:      getInt64FromData(data, "commentCount"),
			EstimatedEarnings: getInt64FromData(data, "estimatedEarnings"),
		}

		submissions = append(submissions, submission)
	}

	log.Printf("✅ 총 %d개 submissions 조회 완료", len(submissions))
	return submissions, nil
}
