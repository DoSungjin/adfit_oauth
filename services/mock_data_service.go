package services

import (
	"context"
	"fmt"
	"log"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// MockDataService - 테스트용 mock 리포트 데이터 주입/삭제
// Firebase Admin SDK로 Firestore Rules 우회하여 직접 쓰기
type MockDataService struct {
	firestore *firestore.Client
}

func NewMockDataService() (*MockDataService, error) {
	clients := GetFirestoreClients()
	if clients == nil {
		return nil, fmt.Errorf("FirestoreClients 초기화 실패")
	}
	return &MockDataService{
		firestore: clients.GetDefaultDB(),
	}, nil
}

// HasRealReportData - 실데이터(non-mock)가 있는지 확인
func (s *MockDataService) HasRealReportData(ctx context.Context, competitionID string) (bool, error) {
	compRef := s.firestore.Collection("competitions").Doc(competitionID)

	finalDoc, err := compRef.Collection("analytics").Doc("final").Get(ctx)
	if err == nil && finalDoc.Exists() {
		if isMock, _ := finalDoc.Data()["isMockData"].(bool); !isMock {
			return true, nil
		}
	}

	reportDoc, err := compRef.Collection("analytics").Doc("report").Get(ctx)
	if err == nil && reportDoc.Exists() {
		if isMock, _ := reportDoc.Data()["isMockData"].(bool); !isMock {
			return true, nil
		}
	}

	comp, err := compRef.Get(ctx)
	if err == nil && comp.Exists() {
		data := comp.Data()
		if _, ok := data["winnerChannelInsights"]; ok {
			if injected, _ := data["mockDataInjected"].(bool); !injected {
				return true, nil
			}
		}
	}

	return false, nil
}

// InjectMockReport - Mock 데이터 4곳 주입 (batch)
func (s *MockDataService) InjectMockReport(ctx context.Context, competitionID string) error {
	compRef := s.firestore.Collection("competitions").Doc(competitionID)

	compDoc, err := compRef.Get(ctx)
	if err != nil {
		return fmt.Errorf("대회 조회 실패: %v", err)
	}
	if !compDoc.Exists() {
		return fmt.Errorf("대회를 찾을 수 없습니다: %s", competitionID)
	}

	batch := s.firestore.Batch()

	// 1. analytics/final
	batch.Set(compRef.Collection("analytics").Doc("final"), map[string]interface{}{
		"totalViews":       8500,
		"totalLikes":       420,
		"totalComments":    85,
		"totalShares":      32,
		"totalSaved":       18,
		"averageViews":     2833,
		"engagementRate":   6.53,
		"totalSubmissions": 3,
		"createdAt":        firestore.ServerTimestamp,
		"isMockData":       true,
	})

	// 2. analytics/report
	batch.Set(compRef.Collection("analytics").Doc("report"), map[string]interface{}{
		"detailedStats": map[string]interface{}{
			"geography": map[string]interface{}{
				"topCountries": []map[string]interface{}{
					{"Country": "KR", "Views": 6800, "Percentage": 80.0},
					{"Country": "US", "Views": 850, "Percentage": 10.0},
					{"Country": "JP", "Views": 510, "Percentage": 6.0},
					{"Country": "TW", "Views": 255, "Percentage": 3.0},
					{"Country": "GB", "Views": 85, "Percentage": 1.0},
				},
				"totalCountries": 5,
			},
		},
		"metadata":   map[string]interface{}{"hasDetailedStats": true},
		"isMockData": true,
	})

	// 3. analytics/videos/list/ 경로는 YouTube Analytics 전용이므로 Instagram mock에서는 생략
	// (Instagram demographics는 winnerChannelInsights에서 처리)

	// 4. competitions/{id}.winnerChannelInsights (merge로 다른 필드 보호)
	batch.Set(compRef, map[string]interface{}{
		"winnerChannelInsights": mockWinnerInsights(),
		"mockDataInjected":      true,
	}, firestore.MergeAll)

	_, err = batch.Commit(ctx)
	if err != nil {
		return fmt.Errorf("batch commit 실패: %v", err)
	}

	log.Printf("✅ Mock 주입 완료: %s", competitionID)
	return nil
}

// CleanupMockReport - isMockData=true 표시된 문서만 삭제
func (s *MockDataService) CleanupMockReport(ctx context.Context, competitionID string) error {
	compRef := s.firestore.Collection("competitions").Doc(competitionID)

	// 1. analytics/final
	if doc, err := compRef.Collection("analytics").Doc("final").Get(ctx); err == nil && doc.Exists() {
		if isMock, _ := doc.Data()["isMockData"].(bool); isMock {
			if _, err := doc.Ref.Delete(ctx); err != nil {
				log.Printf("⚠️ final 삭제 실패: %v", err)
			}
		}
	}

	// 2. analytics/report
	if doc, err := compRef.Collection("analytics").Doc("report").Get(ctx); err == nil && doc.Exists() {
		if isMock, _ := doc.Data()["isMockData"].(bool); isMock {
			if _, err := doc.Ref.Delete(ctx); err != nil {
				log.Printf("⚠️ report 삭제 실패: %v", err)
			}
		}
	}

	// 3. analytics/videos/list/ (isMockData=true인 것만)
	videosIter := compRef.Collection("analytics").Doc("videos").Collection("list").
		Where("isMockData", "==", true).Documents(ctx)
	for {
		doc, err := videosIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("⚠️ videos 순회 오류: %v", err)
			break
		}
		if _, err := doc.Ref.Delete(ctx); err != nil {
			log.Printf("⚠️ video 삭제 실패: %v", err)
		}
	}

	// 4. competitions/{id}.winnerChannelInsights (mockDataInjected=true일 때만)
	if comp, err := compRef.Get(ctx); err == nil && comp.Exists() {
		if injected, _ := comp.Data()["mockDataInjected"].(bool); injected {
			if _, err := compRef.Update(ctx, []firestore.Update{
				{Path: "winnerChannelInsights", Value: firestore.Delete},
				{Path: "mockDataInjected", Value: firestore.Delete},
			}); err != nil {
				log.Printf("⚠️ winnerChannelInsights 삭제 실패: %v", err)
			}
		}
	}

	log.Printf("✅ Mock 정리 완료: %s", competitionID)
	return nil
}

// ============ Mock Data Generators ============

func mockRawData(maleBase, femaleBase float64) []map[string]interface{} {
	return []map[string]interface{}{
		{"age": "13-17", "gender": "남성", "viewPercentage": maleBase * 0.3},
		{"age": "13-17", "gender": "여성", "viewPercentage": femaleBase * 0.4},
		{"age": "18-24", "gender": "남성", "viewPercentage": maleBase * 0.9},
		{"age": "18-24", "gender": "여성", "viewPercentage": femaleBase * 1.0},
		{"age": "25-34", "gender": "남성", "viewPercentage": maleBase * 0.7},
		{"age": "25-34", "gender": "여성", "viewPercentage": femaleBase * 0.85},
		{"age": "35-44", "gender": "남성", "viewPercentage": maleBase * 0.4},
		{"age": "35-44", "gender": "여성", "viewPercentage": femaleBase * 0.5},
		{"age": "45-54", "gender": "남성", "viewPercentage": maleBase * 0.1},
		{"age": "45-54", "gender": "여성", "viewPercentage": femaleBase * 0.15},
	}
}

func mockWinnerInsights() map[string]interface{} {
	return map[string]interface{}{
		"first":  winnerData(8500, 12000, 1800, 3200, 25000, 15000, 9000, 1000, 1.0),
		"second": winnerData(4200, 6500, 950, 1600, 12500, 7500, 4500, 500, 0.5),
		"third":  winnerData(1800, 3200, 480, 820, 6500, 3800, 2400, 300, 0.22),
	}
}

func winnerData(followers, reach, engaged, interactions, views, followerViews, nonFollowerViews, unknownViews int, scale float64) map[string]interface{} {
	r := func(v int) int { return int(float64(v) * scale) }
	return map[string]interface{}{
		"followerCount":     followers,
		"reach":             reach,
		"accountsEngaged":   engaged,
		"totalInteractions": interactions,
		"views":             views,
		"viewsByFollowType": map[string]interface{}{
			"FOLLOWER":     followerViews,
			"NON_FOLLOWER": nonFollowerViews,
			"UNKNOWN":      unknownViews,
		},
		"followsUnfollows": map[string]interface{}{
			"FOLLOWER":     r(120),
			"NON_FOLLOWER": r(35),
		},
		"audienceCountry": map[string]interface{}{
			"KR": r(6800), "US": r(850), "JP": r(510), "TW": r(340),
		},
		"audienceCity": map[string]interface{}{
			"Seoul": r(4200), "Busan": r(1500), "Incheon": r(800),
		},
		"audienceGenderAge": map[string]interface{}{
			"M.18-24": r(1200), "F.18-24": r(1800),
			"M.25-34": r(1500), "F.25-34": r(2200),
			"M.35-44": r(600), "F.35-44": r(800),
		},
		"engagedAudienceCountry": map[string]interface{}{
			"KR": r(1500), "US": r(180), "JP": r(80),
		},
		"engagedAudienceCity": map[string]interface{}{
			"Seoul": r(1100), "Busan": r(300),
		},
		"engagedAudienceGenderAge": map[string]interface{}{
			"M.18-24": r(300), "F.18-24": r(450),
			"M.25-34": r(350), "F.25-34": r(500),
		},
	}
}
