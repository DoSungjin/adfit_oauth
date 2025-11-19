package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
)

// GenerateCompetitionReport - 대회 전체 리포트 생성
func (s *StatsService) GenerateCompetitionReport(competitionID string) error {
	ctx := context.Background()
	now := time.Now()

	log.Printf("\n========================================")
	log.Printf("📊 대회 리포트 생성 시작: %s", competitionID)
	log.Printf("========================================\n")

	// 1️⃣ 모든 submissions 조회
	submissions, err := s.getCompetitionSubmissions(ctx, competitionID)
	if err != nil {
		return fmt.Errorf("submissions 조회 실패: %v", err)
	}

	if len(submissions) == 0 {
		return fmt.Errorf("제출된 영상이 없습니다")
	}

	// 2️⃣ 기본 통계 계산 (모든 영상)
	basicStats := s.calculateBasicStats(ctx, competitionID, submissions)

	// 3️⃣ Analytics 연동 영상 필터링
	verifiedSubmissions := s.getVerifiedSubmissions(ctx, competitionID, submissions)

	// 4️⃣ 상세 통계 계산 (Analytics 연동 영상만)
	var detailedStats map[string]interface{}
	if len(verifiedSubmissions) > 0 {
		detailedStats = s.calculateDetailedStats(verifiedSubmissions)
	} else {
		detailedStats = map[string]interface{}{
			"available": false,
			"reason":    "상세 통계를 수집한 영상이 없습니다",
		}
	}

	// 5️⃣ 메타 정보 계산
	metadata := map[string]interface{}{
		"hasDetailedStats": len(verifiedSubmissions) > 0,
		"verificationRate": float64(len(verifiedSubmissions)) / float64(len(submissions)) * 100,
		"dataQuality":      s.determineDataQuality(len(verifiedSubmissions), len(submissions)),
	}

	// 6️⃣ 리포트 저장 (competitions/{competitionId}/report 필드에 저장)
	reportData := map[string]interface{}{
		"generatedAt":         now,
		"totalSubmissions":    len(submissions),
		"verifiedSubmissions": len(verifiedSubmissions),
		"basicStats":          basicStats,
		"detailedStats":       detailedStats,
		"metadata":            metadata,
	}

	// competitions 문서의 report 필드에 저장
	_, err = s.firestore.Collection("competitions").
		Doc(competitionID).
		Update(ctx, []firestore.Update{
			{Path: "report", Value: reportData},
			{Path: "reportGeneratedAt", Value: now},
		})

	if err != nil {
		return fmt.Errorf("리포트 저장 실패: %v", err)
	}

	log.Printf("✅ 리포트 생성 완료\n")
	log.Printf("  - 전체 영상: %d개\n", len(submissions))
	log.Printf("  - 상세 통계 영상: %d개 (%.1f%%)\n",
		len(verifiedSubmissions), metadata["verificationRate"])

	return nil
}

// calculateBasicStats - 기본 통계 계산 (모든 영상)
func (s *StatsService) calculateBasicStats(ctx context.Context, competitionID string, submissions []SubmissionData) map[string]interface{} {
	totalViews := int64(0)
	totalLikes := int64(0)
	totalComments := int64(0)

	// 각 submission 문서에서 상세 정보 조회
	for _, sub := range submissions {
		doc, err := s.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Doc(sub.ID).
			Get(ctx)

		if err != nil {
			continue
		}

		data := doc.Data()
		totalViews += sub.CurrentViewCount
		totalLikes += getInt64FromData(data, "likeCount")
		totalComments += getInt64FromData(data, "commentCount")
	}

	count := len(submissions)

	return map[string]interface{}{
		"totalViews":      totalViews,
		"totalLikes":      totalLikes,
		"totalComments":   totalComments,
		"averageViews":    float64(totalViews) / float64(count),
		"averageLikes":    float64(totalLikes) / float64(count),
		"averageComments": float64(totalComments) / float64(count),
	}
}

// getVerifiedSubmissions - Analytics 연동된 영상만 필터링
func (s *StatsService) getVerifiedSubmissions(ctx context.Context, competitionID string, submissions []SubmissionData) []map[string]interface{} {
	verifiedSubmissions := []map[string]interface{}{}

	for _, sub := range submissions {
		// 각 submission 문서의 analytics 필드 확인
		doc, err := s.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Doc(sub.ID).
			Get(ctx)

		if err != nil {
			continue
		}

		data := doc.Data()

		// analytics 필드가 있고 isVerified = true인지 확인
		if analytics, ok := data["analytics"].(map[string]interface{}); ok {
			if isVerified, ok := analytics["isVerified"].(bool); ok && isVerified {
				verifiedSubmissions = append(verifiedSubmissions, map[string]interface{}{
					"submissionId": sub.ID,
					"viewCount":    sub.CurrentViewCount,
					"analytics":    analytics,
				})
			}
		}
	}

	log.Printf("📊 Analytics 연동 영상: %d개 / 전체 %d개\n", len(verifiedSubmissions), len(submissions))

	return verifiedSubmissions
}

// calculateDetailedStats - 상세 통계 계산 (가중평균)
func (s *StatsService) calculateDetailedStats(verifiedSubmissions []map[string]interface{}) map[string]interface{} {
	totalWeightedViews := int64(0)

	// Demographics 집계
	genderWeighted := make(map[string]float64)
	ageGroupWeighted := make(map[string]float64)

	// Devices 집계
	deviceWeighted := make(map[string]float64)

	// Traffic Sources 집계
	trafficWeighted := make(map[string]float64)

	// Geography 집계
	geographyMap := make(map[string]struct {
		Views          int64
		MinutesWatched float64
	})

	// Retention 집계 (단순 평균)
	totalViewDuration := 0.0
	totalViewPercentage := 0.0
	retentionCount := 0

	for _, verified := range verifiedSubmissions {
		viewCount := verified["viewCount"].(int64)
		analytics := verified["analytics"].(map[string]interface{})

		totalWeightedViews += viewCount

		// ⭐ 1. Demographics (가중평균)
		if demographics, ok := analytics["demographics"].(map[string]interface{}); ok {
			// Gender
			if gender, ok := demographics["gender"].(map[string]interface{}); ok {
				for key, val := range gender {
					if percentage, ok := val.(float64); ok {
						genderWeighted[key] += percentage * float64(viewCount)
					}
				}
			}

			// Age Group
			if ageGroup, ok := demographics["ageGroup"].(map[string]interface{}); ok {
				for key, val := range ageGroup {
					if percentage, ok := val.(float64); ok {
						ageGroupWeighted[key] += percentage * float64(viewCount)
					}
				}
			}
		}

		// ⭐ 2. Devices (가중평균)
		if devices, ok := analytics["devices"].([]interface{}); ok {
			for _, device := range devices {
				if deviceMap, ok := device.(map[string]interface{}); ok {
					deviceType := deviceMap["device"].(string)
					deviceViews := deviceMap["views"].(float64)
					deviceWeighted[deviceType] += deviceViews
				}
			}
		}

		// ⭐ 3. Traffic Sources (가중평균)
		if traffic, ok := analytics["trafficSources"].([]interface{}); ok {
			for _, source := range traffic {
				if sourceMap, ok := source.(map[string]interface{}); ok {
					sourceType := sourceMap["source"].(string)
					sourceViews := sourceMap["views"].(float64)
					trafficWeighted[sourceType] += sourceViews
				}
			}
		}

		// ⭐ 4. Geography (합계)
		if geography, ok := analytics["geography"].([]interface{}); ok {
			for _, geo := range geography {
				if geoMap, ok := geo.(map[string]interface{}); ok {
					country := geoMap["country"].(string)
					views := int64(geoMap["views"].(float64))
					minutesWatched := geoMap["minutesWatched"].(float64)

					existing := geographyMap[country]
					geographyMap[country] = struct {
						Views          int64
						MinutesWatched float64
					}{
						Views:          existing.Views + views,
						MinutesWatched: existing.MinutesWatched + minutesWatched,
					}
				}
			}
		}

		// ⭐ 5. Retention (단순 평균)
		if retention, ok := analytics["retention"].(map[string]interface{}); ok {
			if avgDuration, ok := retention["averageViewDuration"].(float64); ok {
				totalViewDuration += avgDuration
				retentionCount++
			}
			if avgPercentage, ok := retention["averageViewPercentage"].(float64); ok {
				totalViewPercentage += avgPercentage
			}
		}
	}

	// ⭐ 최종 가중평균 계산 - Demographics
	// YouTube Analytics API는 이미 percentage (0-100) 값을 반환하므로 * 100 하지 않음
	genderWeightedRaw := make(map[string]float64)
	if totalWeightedViews > 0 {
		for key, weighted := range genderWeighted {
			genderWeightedRaw[key] = weighted / float64(totalWeightedViews)
		}
	}

	// ⭐ 정규화: 합이 100%가 되도록 조정
	genderFinal := make(map[string]float64)
	genderSum := 0.0
	for _, val := range genderWeightedRaw {
		genderSum += val
	}
	if genderSum > 0 {
		for key, val := range genderWeightedRaw {
			genderFinal[key] = (val / genderSum) * 100
		}
	}

	ageGroupWeightedRaw := make(map[string]float64)
	if totalWeightedViews > 0 {
		for key, weighted := range ageGroupWeighted {
			ageGroupWeightedRaw[key] = weighted / float64(totalWeightedViews)
		}
	}

	// ⭐ 정규화: 합이 100%가 되도록 조정
	ageGroupFinal := make(map[string]float64)
	ageGroupSum := 0.0
	for _, val := range ageGroupWeightedRaw {
		ageGroupSum += val
	}
	if ageGroupSum > 0 {
		for key, val := range ageGroupWeightedRaw {
			ageGroupFinal[key] = (val / ageGroupSum) * 100
		}
	}

	// ⭐ 최종 가중평균 계산 - Devices
	deviceFinal := make(map[string]float64)
	totalDeviceViews := 0.0
	for _, views := range deviceWeighted {
		totalDeviceViews += views
	}
	if totalDeviceViews > 0 {
		for key, views := range deviceWeighted {
			deviceFinal[key] = (views / totalDeviceViews) * 100
		}
	}

	// ⭐ 최종 가중평균 계산 - Traffic Sources
	trafficFinal := make(map[string]float64)
	totalTrafficViews := 0.0
	for _, views := range trafficWeighted {
		totalTrafficViews += views
	}
	if totalTrafficViews > 0 {
		for key, views := range trafficWeighted {
			trafficFinal[key] = (views / totalTrafficViews) * 100
		}
	}

	// Geography 정렬 (조회수 기준 상위 10개)
	type CountryData struct {
		Country        string
		Views          int64
		MinutesWatched float64
	}

	var geographyList []CountryData
	totalGeoViews := int64(0)
	for country, data := range geographyMap {
		totalGeoViews += data.Views
		geographyList = append(geographyList, CountryData{
			Country:        country,
			Views:          data.Views,
			MinutesWatched: data.MinutesWatched,
		})
	}

	// 조회수 기준 정렬
	sort.Slice(geographyList, func(i, j int) bool {
		return geographyList[i].Views > geographyList[j].Views
	})

	// 상위 10개만 + 비율 계산
	topCountries := []map[string]interface{}{}
	for i, country := range geographyList {
		if i >= 10 {
			break
		}
		percentage := float64(0)
		if totalGeoViews > 0 {
			percentage = float64(country.Views) / float64(totalGeoViews) * 100
		}
		topCountries = append(topCountries, map[string]interface{}{
			"country":        country.Country,
			"views":          country.Views,
			"minutesWatched": country.MinutesWatched,
			"percentage":     percentage,
		})
	}

	// Retention 평균 계산
	retentionData := map[string]interface{}{}
	if retentionCount > 0 {
		retentionData["averageViewDuration"] = totalViewDuration / float64(retentionCount)
		retentionData["averageViewPercentage"] = totalViewPercentage / float64(retentionCount)
	}

	// 최종 리포트
	return map[string]interface{}{
		"demographics": map[string]interface{}{
			"gender":   genderFinal,
			"ageGroup": ageGroupFinal,
		},
		"geography": map[string]interface{}{
			"topCountries":   topCountries,
			"totalCountries": len(geographyMap),
		},
		"devices":        deviceFinal,
		"trafficSources": trafficFinal,
		"retention":      retentionData,
	}
}

// determineDataQuality - 데이터 품질 판단
func (s *StatsService) determineDataQuality(verifiedCount, totalCount int) string {
	if totalCount == 0 {
		return "none"
	}

	rate := float64(verifiedCount) / float64(totalCount) * 100

	if rate >= 70 {
		return "good" // 70% 이상
	} else if rate >= 40 {
		return "fair" // 40-70%
	} else {
		return "poor" // 40% 미만
	}
}
