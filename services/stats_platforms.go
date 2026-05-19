package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
)

// ==================== YouTube ====================

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

	// ⭐ 성과형 대회 정보 캐시 (대회별 1회만 조회)
	perfInfoCache := make(map[string]*PerformanceCompetitionInfo)

	for _, video := range response.Items {
		if submission, exists := submissions[video.Id]; exists {
			viewCount := int64(video.Statistics.ViewCount)
			likeCount := int64(video.Statistics.LikeCount)
			commentCount := int64(video.Statistics.CommentCount)

			// ⭐ 성과형 대회 정보 조회 (캐시)
			perfInfo, exists := perfInfoCache[submission.CompetitionID]
			if !exists {
				perfInfo = s.getCompetitionPerformanceInfo(ctx, submission.CompetitionID)
				perfInfoCache[submission.CompetitionID] = perfInfo
			}

			// submissions 문서 업데이트
			docRef := s.firestore.Collection("competitions").
				Doc(submission.CompetitionID).
				Collection("submissions").
				Doc(submission.ID)

			updates := []firestore.Update{
				{Path: "currentViewCount", Value: viewCount},
				{Path: "lastUpdatedAt", Value: time.Now()},
			}

			// ⭐ 성과형 대회: estimatedEarnings 계산
			var estimatedEarnings int64 = 0
			if perfInfo.IsPerformance {
				estimatedEarnings = calculateEstimatedEarnings(viewCount, perfInfo.PricePerView, perfInfo.MinViews)
				updates = append(updates, firestore.Update{
					Path:  "estimatedEarnings",
					Value: estimatedEarnings,
				})
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
					"creatorId":         submission.CreatorID,
					"creatorName":       submission.CreatorName,
					"platform":          submission.Platform,
					"videoId":           submission.VideoID,
					"viewCount":         viewCount,
					"likeCount":         likeCount,
					"commentCount":      commentCount,
					"estimatedEarnings": estimatedEarnings, // ⭐ 성과형
					"lastUpdated":       time.Now().Unix(),
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

// ==================== Instagram 조회수 업데이트 ====================

func (s *StatsService) updateInstagramViewCounts(ctx context.Context, competitionID string, submissions []SubmissionData) error {
	perfInfo := s.getCompetitionPerformanceInfo(ctx, competitionID)

	for _, sub := range submissions {
		if sub.Platform != "instagram" {
			continue
		}

		// Firestore에서 instagramAuth 조회
		doc, err := s.firestore.Collection("competitions").Doc(competitionID).
			Collection("submissions").Doc(sub.ID).Get(ctx)
		if err != nil {
			continue
		}

		data := doc.Data()
		instagramAuth, ok := data["instagramAuth"].(map[string]interface{})
		if !ok {
			continue
		}

		accessToken, _ := instagramAuth["accessToken"].(string)
		instagramData, _ := data["instagramData"].(map[string]interface{})
		mediaID, _ := instagramData["mediaId"].(string)

		if accessToken == "" || mediaID == "" {
			continue
		}

		// Instagram Insights API 호출
		viewCount := s.fetchInstagramViews(mediaID, accessToken)
		if viewCount <= 0 {
			continue
		}

		// estimatedEarnings 계산
		var estimatedEarnings int64 = 0
		if perfInfo.IsPerformance && viewCount >= perfInfo.MinViews {
			estimatedEarnings = viewCount * perfInfo.PricePerView
		}

		// Firestore 업데이트
		s.firestore.Collection("competitions").Doc(competitionID).
			Collection("submissions").Doc(sub.ID).Update(ctx, []firestore.Update{
			{Path: "currentViewCount", Value: viewCount},
			{Path: "estimatedEarnings", Value: estimatedEarnings},
			{Path: "lastUpdatedAt", Value: time.Now()},
		})

		log.Printf("✅ Instagram 업데이트: %s - 조회수 %d", mediaID, viewCount)
	}

	return nil
}

func (s *StatsService) fetchInstagramViews(mediaID, accessToken string) int64 {
	url := fmt.Sprintf("https://graph.instagram.com/%s/insights?metric=views&access_token=%s", mediaID, accessToken)

	resp, err := http.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			Values []struct {
				Value int64 `json:"value"`
			} `json:"values"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0
	}

	if len(result.Data) > 0 && len(result.Data[0].Values) > 0 {
		return result.Data[0].Values[0].Value
	}
	return 0
}

// ==================== TikTok 조회수 업데이트 ====================

func (s *StatsService) updateTikTokViewCounts(ctx context.Context, competitionID string, submissions []SubmissionData) error {
	perfInfo := s.getCompetitionPerformanceInfo(ctx, competitionID)

	for _, sub := range submissions {
		if sub.Platform != "tiktok" {
			continue
		}

		// Firestore에서 tiktokAuth 조회
		doc, err := s.firestore.Collection("competitions").Doc(competitionID).
			Collection("submissions").Doc(sub.ID).Get(ctx)
		if err != nil {
			continue
		}

		data := doc.Data()
		tiktokAuth, ok := data["tiktokAuth"].(map[string]interface{})
		if !ok {
			continue
		}

		accessToken, _ := tiktokAuth["accessToken"].(string)
		tiktokData, _ := data["tiktokData"].(map[string]interface{})
		videoID, _ := tiktokData["videoId"].(string)

		if accessToken == "" || videoID == "" {
			continue
		}

		// TikTok Video List API 호출 (조회수 포함)
		viewCount := s.fetchTikTokViews(videoID, accessToken)
		if viewCount <= 0 {
			continue
		}

		// estimatedEarnings 계산
		var estimatedEarnings int64 = 0
		if perfInfo.IsPerformance && viewCount >= perfInfo.MinViews {
			estimatedEarnings = viewCount * perfInfo.PricePerView
		}

		// Firestore 업데이트
		s.firestore.Collection("competitions").Doc(competitionID).
			Collection("submissions").Doc(sub.ID).Update(ctx, []firestore.Update{
			{Path: "currentViewCount", Value: viewCount},
			{Path: "estimatedEarnings", Value: estimatedEarnings},
			{Path: "lastUpdatedAt", Value: time.Now()},
		})

		log.Printf("✅ TikTok 업데이트: %s - 조회수 %d", videoID, viewCount)
	}

	return nil
}

func (s *StatsService) fetchTikTokViews(videoID, accessToken string) int64 {
	url := "https://open.tiktokapis.com/v2/video/list/?fields=id,view_count"

	reqBody := map[string]interface{}{"max_count": 20}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			Videos []struct {
				ID        string `json:"id"`
				ViewCount int64  `json:"view_count"`
			} `json:"videos"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0
	}

	// 해당 videoID 찾기
	for _, video := range result.Data.Videos {
		if video.ID == videoID {
			return video.ViewCount
		}
	}
	return 0
}

// ==================== Instagram 수상자 체널 인사이트 ====================

// collectInstagramWinnerInsights - Instagram 수상자들의 체널 도달 정보 수집
// 대회 종료 시 FinalizeCompetition 내에서 토큰이 살아있을 때만 호출
func (s *StatsService) collectInstagramWinnerInsights(
	ctx context.Context,
	competitionID string,
	winners map[string]interface{},
	submissions []SubmissionData,
	db *firestore.Client,
) map[string]interface{} {

	result := make(map[string]interface{})

	// Instagram submission이 있는 수상자만 처리
	rankKeys := []string{"first", "second", "third"}
	for i := 4; i <= 10; i++ {
		rankKeys = append(rankKeys, fmt.Sprintf("rank%d", i))
	}

	for _, rankKey := range rankKeys {
		winnerData, ok := winners[rankKey]
		if !ok {
			continue
		}

		var creatorID string
		switch w := winnerData.(type) {
		case WinnerInfo:
			creatorID = w.CreatorID
		case map[string]interface{}:
			creatorID, _ = w["creatorId"].(string)
		}
		if creatorID == "" {
			continue
		}

		// Instagram submission 여부 확인 (platform 필드)
		hasInstagram := false
		for _, sub := range submissions {
			if sub.CreatorID == creatorID && sub.Platform == "instagram" {
				hasInstagram = true
				break
			}
		}
		if !hasInstagram {
			continue
		}

		// ⭐ Instagram 수상자는 API 응답 실패/빈값이어도 필드 초기화(0)해서 항상 저장
		// → Firestore에서 수동 수정 가능하도록
		insights := s.fetchInstagramChannelInsights(ctx, competitionID, creatorID, db)
		result[rankKey] = insights
		log.Printf("✅ Instagram 체널 인사이트 (%s): %v", rankKey, insights)
	}

	return result
}

// fetchInstagramChannelInsights - 수상자 한 명의 채널 인사이트 수집
// ⭐ 공식 Meta Graph API v22.0 기준, 실제 API에 존재하는 metric만 사용.
//
// 채널 KPI (period=day, since/until 28일 범위, metric_type=total_value):
//   - reach: 28일 도달 누적
//   - accountsEngaged: 참여 계정 수
//   - totalInteractions: 총 상호작용 수
//   - views: 콘텐츠 뷰 총합
//   - viewsByFollowType: views + breakdown=follow_type {FOLLOWER, NON_FOLLOWER, UNKNOWN}
//   - followsUnfollows: follows_and_unfollows + breakdown=follow_type {FOLLOWER, NON_FOLLOWER}
//
// 프로필 필드:
//   - followerCount: GET /{igUserId}?fields=followers_count
//
// 팔로워 기반 인구통계 (period=lifetime, timeframe=this_month, breakdown 사용):
//   - audienceCountry: follower_demographics + breakdown=country
//   - audienceCity: follower_demographics + breakdown=city
//   - audienceGenderAge: follower_demographics + breakdown=age,gender
//
// 참여자 기반 인구통계 (period=lifetime, timeframe=this_month):
//   - engagedAudienceCountry: engaged_audience_demographics + breakdown=country
//   - engagedAudienceCity: engaged_audience_demographics + breakdown=city
//   - engagedAudienceGenderAge: engaged_audience_demographics + breakdown=age,gender
//
// 제약 (공식 문서 기준):
//   - follower_demographics: 팔로워 100명 미만 계정엔 빈 응답
//   - engaged_audience_demographics: 참여 100건 미만 시 빈 응답
//   - demographics metric: TOP 45명만, 최대 48시간 지연
//
// 실패/빈 응답시에도 모든 필드는 0 또는 빈 맵으로 초기화되어 Firestore에 항상 기록됨.
// (profile_views는 v22에서 deprecated 되어 필드 자체를 제거)
func (s *StatsService) fetchInstagramChannelInsights(
	ctx context.Context,
	competitionID, creatorID string,
	db *firestore.Client,
) map[string]interface{} {

	// ⭐ 기본 필드 초기화 (API 실패해도 항상 이 구조로 저장됨)
	result := map[string]interface{}{
		// 채널 KPI
		"reach":             int64(0),
		"accountsEngaged":   int64(0),
		"totalInteractions": int64(0),
		"views":             int64(0),
		"viewsByFollowType": map[string]int64{},
		"followsUnfollows":  map[string]int64{},
		// 프로필
		"followerCount": int64(0),
		// 팔로워 기반 인구통계
		"audienceCountry":   map[string]int64{},
		"audienceCity":      map[string]int64{},
		"audienceGenderAge": map[string]int64{},
		// 참여자 기반 인구통계
		"engagedAudienceCountry":   map[string]int64{},
		"engagedAudienceCity":      map[string]int64{},
		"engagedAudienceGenderAge": map[string]int64{},
		// 메타
		"fetchedAt": time.Now(),
	}

	// 1. Instagram submission에서 token 조회
	iter := db.Collection("competitions").Doc(competitionID).
		Collection("submissions").
		Where("creatorId", "==", creatorID).
		Where("platform", "==", "instagram").
		Where("isDeleted", "==", "n").
		Limit(1).
		Documents(ctx)

	doc, err := iter.Next()
	if err != nil {
		log.Printf("⚠️ Instagram submission 없음 [%s]: %v", creatorID, err)
		return result
	}

	data := doc.Data()
	instagramAuth, ok := data["instagramAuth"].(map[string]interface{})
	if !ok {
		log.Printf("⚠️ instagramAuth 없음 [%s]", creatorID)
		return result
	}

	accessToken, _ := instagramAuth["accessToken"].(string)
	igUserID, _ := instagramAuth["igUserId"].(string)

	if accessToken == "" || igUserID == "" {
		log.Printf("⚠️ token 또는 igUserId 없음 [%s]", creatorID)
		return result
	}

	const apiVer = "v22.0"
	until := time.Now().Unix()
	since := time.Now().Add(-28 * 24 * time.Hour).Unix()

	// ==================== 채널 KPI ====================

	// 2. reach (28일 누적)
	reachURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=reach&period=day&metric_type=total_value&since=%d&until=%d&access_token=%s",
		apiVer, igUserID, since, until, accessToken,
	)
	if d := s.callInstagramGraphAPI(reachURL, "reach"); d != nil {
		result["reach"] = extractTotalValue(d)
	}

	// 3. accounts_engaged (28일 누적)
	aeURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=accounts_engaged&period=day&metric_type=total_value&since=%d&until=%d&access_token=%s",
		apiVer, igUserID, since, until, accessToken,
	)
	if d := s.callInstagramGraphAPI(aeURL, "accounts_engaged"); d != nil {
		result["accountsEngaged"] = extractTotalValue(d)
	}

	// 4. total_interactions (28일 누적)
	tiURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=total_interactions&period=day&metric_type=total_value&since=%d&until=%d&access_token=%s",
		apiVer, igUserID, since, until, accessToken,
	)
	if d := s.callInstagramGraphAPI(tiURL, "total_interactions"); d != nil {
		result["totalInteractions"] = extractTotalValue(d)
	}

	// 5. views (28일 누적, 전체)
	viewsURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=views&period=day&metric_type=total_value&since=%d&until=%d&access_token=%s",
		apiVer, igUserID, since, until, accessToken,
	)
	if d := s.callInstagramGraphAPI(viewsURL, "views"); d != nil {
		result["views"] = extractTotalValue(d)
	}

	// 6. views + breakdown=follow_type (팔로워 vs 비팔로워 분리)
	viewsFollowURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=views&period=day&metric_type=total_value&breakdown=follow_type&since=%d&until=%d&access_token=%s",
		apiVer, igUserID, since, until, accessToken,
	)
	if d := s.callInstagramGraphAPI(viewsFollowURL, "views_follow_type"); d != nil {
		if parsed := parseBreakdownSingle(d); len(parsed) > 0 {
			result["viewsByFollowType"] = parsed
		}
	}

	// 7. follows_and_unfollows + breakdown=follow_type
	followsURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=follows_and_unfollows&period=day&metric_type=total_value&breakdown=follow_type&since=%d&until=%d&access_token=%s",
		apiVer, igUserID, since, until, accessToken,
	)
	if d := s.callInstagramGraphAPI(followsURL, "follows_and_unfollows"); d != nil {
		if parsed := parseBreakdownSingle(d); len(parsed) > 0 {
			result["followsUnfollows"] = parsed
		}
	}

	// ==================== 프로필 ====================

	// 8. followers_count — 프로필 기본 필드
	followerURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s?fields=followers_count&access_token=%s",
		apiVer, igUserID, accessToken,
	)
	if followerData := s.callInstagramGraphAPI(followerURL, "followers_count"); followerData != nil {
		switch v := followerData["followers_count"].(type) {
		case float64:
			result["followerCount"] = int64(v)
		case int64:
			result["followerCount"] = v
		}
	}

	// ==================== 팔로워 인구통계 (follower_demographics) ====================

	// 9. follower_demographics + breakdown=country
	countryURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=follower_demographics&period=lifetime&timeframe=this_month&breakdown=country&metric_type=total_value&access_token=%s",
		apiVer, igUserID, accessToken,
	)
	if d := s.callInstagramGraphAPI(countryURL, "follower_country"); d != nil {
		if parsed := parseFollowerDemographics(d); len(parsed) > 0 {
			result["audienceCountry"] = parsed
		}
	}

	// 10. follower_demographics + breakdown=city
	cityURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=follower_demographics&period=lifetime&timeframe=this_month&breakdown=city&metric_type=total_value&access_token=%s",
		apiVer, igUserID, accessToken,
	)
	if d := s.callInstagramGraphAPI(cityURL, "follower_city"); d != nil {
		if parsed := parseFollowerDemographics(d); len(parsed) > 0 {
			result["audienceCity"] = parsed
		}
	}

	// 11. follower_demographics + breakdown=age,gender → "M.18-24" 형식
	genderAgeURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=follower_demographics&period=lifetime&timeframe=this_month&breakdown=age,gender&metric_type=total_value&access_token=%s",
		apiVer, igUserID, accessToken,
	)
	if d := s.callInstagramGraphAPI(genderAgeURL, "follower_age_gender"); d != nil {
		if parsed := parseFollowerDemographicsAgeGender(d); len(parsed) > 0 {
			result["audienceGenderAge"] = parsed
		}
	}

	// ==================== 참여자 인구통계 (engaged_audience_demographics) ====================

	// 12. engaged_audience_demographics + breakdown=country
	engCountryURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=engaged_audience_demographics&period=lifetime&timeframe=this_month&breakdown=country&metric_type=total_value&access_token=%s",
		apiVer, igUserID, accessToken,
	)
	if d := s.callInstagramGraphAPI(engCountryURL, "engaged_country"); d != nil {
		if parsed := parseFollowerDemographics(d); len(parsed) > 0 {
			result["engagedAudienceCountry"] = parsed
		}
	}

	// 13. engaged_audience_demographics + breakdown=city
	engCityURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=engaged_audience_demographics&period=lifetime&timeframe=this_month&breakdown=city&metric_type=total_value&access_token=%s",
		apiVer, igUserID, accessToken,
	)
	if d := s.callInstagramGraphAPI(engCityURL, "engaged_city"); d != nil {
		if parsed := parseFollowerDemographics(d); len(parsed) > 0 {
			result["engagedAudienceCity"] = parsed
		}
	}

	// 14. engaged_audience_demographics + breakdown=age,gender
	engGenderAgeURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s/insights?metric=engaged_audience_demographics&period=lifetime&timeframe=this_month&breakdown=age,gender&metric_type=total_value&access_token=%s",
		apiVer, igUserID, accessToken,
	)
	if d := s.callInstagramGraphAPI(engGenderAgeURL, "engaged_age_gender"); d != nil {
		if parsed := parseFollowerDemographicsAgeGender(d); len(parsed) > 0 {
			result["engagedAudienceGenderAge"] = parsed
		}
	}

	return result
}

// extractTotalValue - period=day metric 응답에서 data[0].total_value.value 추출
// 응답 예: {"data":[{"total_value":{"value":12345}, ...}]}
func extractTotalValue(resp map[string]interface{}) int64 {
	arr, ok := resp["data"].([]interface{})
	if !ok || len(arr) == 0 {
		return 0
	}
	m, ok := arr[0].(map[string]interface{})
	if !ok {
		return 0
	}
	tv, ok := m["total_value"].(map[string]interface{})
	if !ok {
		return 0
	}
	switch v := tv["value"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// parseBreakdownSingle - 단일 breakdown (예: follow_type) 응답 파싱
// data[0].total_value.breakdowns[0].results[].dimension_values[last] → key
// 응답 예: {"data":[{"total_value":{"breakdowns":[{"results":[{"dimension_values":["FOLLOWER"],"value":1234}]}]}}]}
func parseBreakdownSingle(resp map[string]interface{}) map[string]int64 {
	parsed := make(map[string]int64)

	arr, ok := resp["data"].([]interface{})
	if !ok || len(arr) == 0 {
		return parsed
	}
	m, ok := arr[0].(map[string]interface{})
	if !ok {
		return parsed
	}
	tv, ok := m["total_value"].(map[string]interface{})
	if !ok {
		return parsed
	}
	breakdowns, ok := tv["breakdowns"].([]interface{})
	if !ok || len(breakdowns) == 0 {
		return parsed
	}
	breakdown, ok := breakdowns[0].(map[string]interface{})
	if !ok {
		return parsed
	}
	resultsRaw, ok := breakdown["results"].([]interface{})
	if !ok {
		return parsed
	}

	for _, r := range resultsRaw {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		dimVals, _ := rm["dimension_values"].([]interface{})
		if len(dimVals) == 0 {
			continue
		}
		key := fmt.Sprintf("%v", dimVals[len(dimVals)-1])
		switch v := rm["value"].(type) {
		case float64:
			parsed[key] = int64(v)
		case int64:
			parsed[key] = v
		}
	}
	return parsed
}

// callInstagramGraphAPI - Instagram Graph API 공통 호출 함수 (raw URL 입력)
// 에러/빈 응답시 nil 리턴. 호출부에서 초기값 유지.
func (s *StatsService) callInstagramGraphAPI(url, label string) map[string]interface{} {
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("⚠️ Instagram Graph API 실패 [%s]: %v", label, err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("📸 Instagram Graph API [%s]: %s", label, string(body))

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("⚠️ Instagram Graph API 파싱 실패 [%s]: %v", label, err)
		return nil
	}

	// error 필드 체크
	if errObj, ok := result["error"].(map[string]interface{}); ok {
		code, _ := errObj["code"].(float64)
		msg, _ := errObj["message"].(string)
		log.Printf("⚠️ Instagram Graph API 오류 [%s] (code %v): %s", label, code, msg)
		return nil
	}

	return result
}

// parseFollowerDemographics - follower_demographics + breakdown=country 응답 파싱
// dimension_values: [timeframe, country] → 마지막 값을 key로 사용
func parseFollowerDemographics(resp map[string]interface{}) map[string]int64 {
	parsed := make(map[string]int64)

	dataArr, ok := resp["data"].([]interface{})
	if !ok || len(dataArr) == 0 {
		return parsed
	}

	m, ok := dataArr[0].(map[string]interface{})
	if !ok {
		return parsed
	}

	tv, ok := m["total_value"].(map[string]interface{})
	if !ok {
		return parsed
	}

	breakdowns, ok := tv["breakdowns"].([]interface{})
	if !ok || len(breakdowns) == 0 {
		return parsed
	}

	breakdown, ok := breakdowns[0].(map[string]interface{})
	if !ok {
		return parsed
	}

	resultsRaw, ok := breakdown["results"].([]interface{})
	if !ok {
		return parsed
	}

	for _, r := range resultsRaw {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		dimVals, _ := rm["dimension_values"].([]interface{})
		if len(dimVals) == 0 {
			continue
		}
		// 마지막 요소가 실제 차원 값 (timeframe이 맨 앞이므로)
		key := fmt.Sprintf("%v", dimVals[len(dimVals)-1])
		switch v := rm["value"].(type) {
		case float64:
			parsed[key] = int64(v)
		case int64:
			parsed[key] = v
		}
	}

	return parsed
}

// parseFollowerDemographicsAgeGender - follower_demographics + breakdown=age,gender 응답 파싱
// dimension_values 예: [timeframe, age, gender] 또는 [timeframe, gender, age]
// → "M.18-24" / "F.25-34" 형식 (프론트 _buildGenderAgeTable 호환)
func parseFollowerDemographicsAgeGender(resp map[string]interface{}) map[string]int64 {
	parsed := make(map[string]int64)

	dataArr, ok := resp["data"].([]interface{})
	if !ok || len(dataArr) == 0 {
		return parsed
	}
	m, ok := dataArr[0].(map[string]interface{})
	if !ok {
		return parsed
	}
	tv, ok := m["total_value"].(map[string]interface{})
	if !ok {
		return parsed
	}
	breakdowns, ok := tv["breakdowns"].([]interface{})
	if !ok || len(breakdowns) == 0 {
		return parsed
	}
	breakdown, ok := breakdowns[0].(map[string]interface{})
	if !ok {
		return parsed
	}

	// dimension_keys 확인 (순서 파악용)
	dimKeys, _ := breakdown["dimension_keys"].([]interface{})
	ageIdx, genderIdx := -1, -1
	for i, k := range dimKeys {
		ks := fmt.Sprintf("%v", k)
		if ks == "age" {
			ageIdx = i
		} else if ks == "gender" {
			genderIdx = i
		}
	}

	resultsRaw, ok := breakdown["results"].([]interface{})
	if !ok {
		return parsed
	}

	for _, r := range resultsRaw {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		dimVals, _ := rm["dimension_values"].([]interface{})
		if len(dimVals) < 2 {
			continue
		}

		var age, gender string
		if ageIdx >= 0 && ageIdx < len(dimVals) {
			age = fmt.Sprintf("%v", dimVals[ageIdx])
		}
		if genderIdx >= 0 && genderIdx < len(dimVals) {
			gender = fmt.Sprintf("%v", dimVals[genderIdx])
		}
		// fallback: dimension_keys 못 읽었으면 마지막 2개 사용
		if age == "" || gender == "" {
			age = fmt.Sprintf("%v", dimVals[len(dimVals)-2])
			gender = fmt.Sprintf("%v", dimVals[len(dimVals)-1])
		}

		// gender 정규화: M/F
		g := "U"
		switch gender {
		case "M", "male", "MALE":
			g = "M"
		case "F", "female", "FEMALE":
			g = "F"
		}

		key := fmt.Sprintf("%s.%s", g, age)
		switch v := rm["value"].(type) {
		case float64:
			parsed[key] = int64(v)
		case int64:
			parsed[key] = v
		}
	}

	return parsed
}

// ==================== Realtime Database 함수들 ====================

// getEngagementFromRealtimeDB - Realtime DB에서 대회의 engagement 합산 조회
// likeCount, savedCount, shareCount는 Realtime DB에만 있음 (Firestore에는 currentViewCount만)
func (s *StatsService) getEngagementFromRealtimeDB(ctx context.Context, competitionID string) (totalLikes, totalSaved, totalShares int64) {
	if s.realtimeDB == nil {
		return 0, 0, 0
	}

	ref := s.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions", competitionID))
	var allSubmissions map[string]map[string]interface{}
	if err := ref.Get(ctx, &allSubmissions); err != nil || len(allSubmissions) == 0 {
		log.Printf("⚠️ Realtime DB engagement 조회 실패 또는 데이터 없음 [%s]: %v", competitionID, err)
		return 0, 0, 0
	}

	for _, sub := range allSubmissions {
		totalLikes += rtInt64(sub["likeCount"])
		totalSaved += rtInt64(sub["savedCount"])
		totalShares += rtInt64(sub["shareCount"])
	}

	log.Printf("✅ Realtime DB engagement 합산 [%s]: likes=%d, saved=%d, shares=%d", competitionID, totalLikes, totalSaved, totalShares)
	return totalLikes, totalSaved, totalShares
}

// rtInt64 - Realtime DB float64 타입 안전 변환
func rtInt64(v interface{}) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case int64:
		return val
	case int:
		return int64(val)
	}
	return 0
}

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
	var totalEstimatedEarnings int64 = 0 // ⭐ 성과형 추가
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

		// ⭐ estimatedEarnings 추출
		var earnings int64
		switch v := submission["estimatedEarnings"].(type) {
		case int64:
			earnings = v
		case float64:
			earnings = int64(v)
		case int:
			earnings = int64(v)
		}
		totalEstimatedEarnings += earnings

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
		"creatorName":            creatorName,
		"totalViews":             totalViews,
		"totalEstimatedEarnings": totalEstimatedEarnings, // ⭐ 성과형
		"submissionCount":        submissionCount,
		"lastUpdated":            time.Now().Unix(),
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

	log.Printf("✅ Leaderboard 업데이트: %s - 조회수 %d, 예상수익 %d (영상 %d개)", creatorName, totalViews, totalEstimatedEarnings, submissionCount)
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
