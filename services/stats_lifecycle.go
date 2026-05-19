package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

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

			// ⭐ deadline + 5분이 지났으면 종료 처리 (Cron 업데이트 완료 대기)
			if deadline.Add(5 * time.Minute).Before(now) {
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
