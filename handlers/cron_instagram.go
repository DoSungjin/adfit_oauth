package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"adfit-oauth/config"
)

type InstagramCronHandler struct {
	firestore  *firestore.Client
	realtimeDB *db.Client
}

// ⭐ 성과형 대회 정보 (캐싱용)
type InstagramPerformanceInfo struct {
	IsPerformance bool
	PricePerView  int64
	MinViews      int64
}

// InstagramMediaStats - Instagram API에서 수집한 미디어 통계
type InstagramMediaStats struct {
	ViewCount  int64 // views (조회수) — Firestore + Realtime DB
	ReachCount int64 // reach (순 도달 수) — Realtime DB only
	LikeCount  int64 // likes (좋아요) — Realtime DB only
	SavedCount int64 // saved (저장 수) — Realtime DB only
	ShareCount int64 // shares (공유 수) — Realtime DB only
}

// NewInstagramCronHandler - Instagram Cron Handler 생성
func NewInstagramCronHandler() (*InstagramCronHandler, error) {
	ctx := context.Background()

	databaseURL := "https://posted-app-c4ff5-default-rtdb.firebaseio.com"
	if config.Config != nil && config.Config.Firebase.DatabaseURL != "" {
		databaseURL = config.Config.Firebase.DatabaseURL
	}

	var app *firebase.App
	var err error

	if config.Config != nil && config.Config.Firebase.CredentialsPath != "" {
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID:   config.Config.Firebase.ProjectID,
			DatabaseURL: databaseURL,
		}, option.WithCredentialsFile(config.Config.Firebase.CredentialsPath))
	} else {
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID:   "posted-app-c4ff5",
			DatabaseURL: databaseURL,
		})
	}

	if err != nil {
		return nil, fmt.Errorf("firebase 초기화 실패: %v", err)
	}

	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore 초기화 실패: %v", err)
	}

	realtimeDBClient, err := app.Database(ctx)
	if err != nil {
		log.Printf("⚠️ Realtime Database 초기화 실패: %v", err)
		realtimeDBClient = nil
	} else {
		log.Println("✅ Realtime Database 연동 완료 (Instagram)")
	}

	return &InstagramCronHandler{
		firestore:  firestoreClient,
		realtimeDB: realtimeDBClient,
	}, nil
}

// UpdateInstagramStatsInternal - 내부 호출용
func (h *InstagramCronHandler) UpdateInstagramStatsInternal() {
	result := h.updateInstagramStatsCore()
	log.Printf("🎉 Instagram 업데이트 완료: %d/%d 성공", result.UpdatedCount, result.TotalVideos)
}

// UpdateInstagramStats - HTTP 핸들러
func (h *InstagramCronHandler) UpdateInstagramStats(c *gin.Context) {
	result := h.updateInstagramStatsCore()
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"updatedCount": result.UpdatedCount,
		"errorCount":   result.ErrorCount,
		"totalVideos":  result.TotalVideos,
	})
}

type InstagramUpdateResult struct {
	UpdatedCount int
	ErrorCount   int
	TotalVideos  int
}

// updateInstagramStatsCore - 핵심 로직
func (h *InstagramCronHandler) updateInstagramStatsCore() InstagramUpdateResult {
	ctx := context.Background()
	log.Println("🔄 Instagram 통계 업데이트 시작...")

	// 1️⃣ 진행 중인 대회만 조회
	competitionsIter := h.firestore.Collection("competitions").
		Where("status", "in", []string{"APPROVED", "ONGOING"}).
		Documents(ctx)

	updatedCount, errorCount, totalVideos := 0, 0, 0

	for {
		doc, err := competitionsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ 대회 조회 오류: %v", err)
			continue
		}

		competitionData := doc.Data()
		if deleted, ok := competitionData["deleted"].(bool); ok && deleted {
			continue
		}

		competitionID := doc.Ref.ID
		log.Printf("📸 Instagram 대회 처리 중: %s", competitionID)

		// 2️⃣ Instagram submissions 조회
		submissionsIter := h.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Where("platform", "==", "instagram").
			Documents(ctx)

		for {
			subDoc, err := submissionsIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("❌ submissions 조회 오류: %v", err)
				continue
			}

			submissionData := subDoc.Data()
			submissionID := subDoc.Ref.ID
			log.Printf("📸 Instagram submission 발견: %s", submissionID)

			if isDeleted, ok := submissionData["isDeleted"].(string); ok && isDeleted != "n" {
				log.Printf("⚠️ 삭제된 submission 스킵: %s", submissionID)
				continue
			}

			totalVideos++

			// 3️⃣ instagramAuth 확인
			instagramAuth, ok := submissionData["instagramAuth"].(map[string]interface{})
			if !ok {
				log.Printf("⚠️ instagramAuth 없음: %s", submissionID)
				continue
			}

			accessToken, _ := instagramAuth["accessToken"].(string)
			if accessToken == "" {
				log.Printf("⚠️ accessToken 없음: %s", submissionID)
				continue
			}
			log.Printf("✅ accessToken 확인됨: %s (길이: %d)", submissionID, len(accessToken))

			// 토큰 만료 체크 (7일 전이면 갱신)
			if expiresAtStr, ok := instagramAuth["expiresAt"].(string); ok {
				if expiresAt, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
					if time.Now().Add(7 * 24 * time.Hour).After(expiresAt) {
						if newToken, newExpires, err := h.refreshInstagramToken(accessToken); err == nil {
							accessToken = newToken
							subDoc.Ref.Update(ctx, []firestore.Update{
								{Path: "instagramAuth.accessToken", Value: newToken},
								{Path: "instagramAuth.expiresAt", Value: newExpires.Format(time.RFC3339)},
							})
							log.Printf("✅ Instagram Token 갱신 완료: %s", submissionID)
						}
					}
				}
			}

			// mediaId 추출
			var mediaID string
			if instagramData, ok := submissionData["instagramData"].(map[string]interface{}); ok {
				mediaID, _ = instagramData["mediaId"].(string)
				log.Printf("✅ instagramData 확인: mediaId=%s", mediaID)
			} else {
				log.Printf("⚠️ instagramData 없음: %s", submissionID)
			}
			if mediaID == "" {
				log.Printf("⚠️ mediaID 없음: %s", submissionID)
				continue
			}

			creatorID, _ := submissionData["creatorId"].(string)
			creatorName, _ := submissionData["creatorName"].(string)

			// mediaType 추출 (REELS 전용 metric 분기에 사용)
			var mediaType string
			if instagramData, ok := submissionData["instagramData"].(map[string]interface{}); ok {
				mediaType, _ = instagramData["mediaType"].(string)
			}

			// 4️⃣ Instagram API 호출 (상세 통계 수집)
			log.Printf("🔄 Instagram API 호출 시작: mediaId=%s, mediaType=%s", mediaID, mediaType)
			stats, err := h.fetchInstagramInsights(accessToken, mediaID, mediaType)
			if err != nil {
				log.Printf("❌ Instagram API 실패 [%s]: %v", mediaID, err)
				errorCount++
				continue
			}
			log.Printf("✅ Instagram API 성공: mediaId=%s, views=%d, reach=%d, likes=%d, saved=%d, shares=%d",
				mediaID, stats.ViewCount, stats.ReachCount, stats.LikeCount, stats.SavedCount, stats.ShareCount)

			// ⭐ 성과형 대회 정보 조회 및 estimatedEarnings 계산
			var estimatedEarnings int64 = 0
			perfInfo := h.getCompetitionPerformanceInfo(ctx, competitionID)
			if perfInfo.IsPerformance && stats.ViewCount >= perfInfo.MinViews {
				estimatedEarnings = stats.ViewCount * perfInfo.PricePerView
			}

			// 5️⃣ Firestore 업데이트 — currentViewCount만 (FinalizeCompetition 집계용)
			// likeCount/savedCount/shareCount는 Realtime DB에서 직접 참조
			updates := []firestore.Update{
				{Path: "currentViewCount", Value: stats.ViewCount},
				{Path: "lastStatsUpdate", Value: firestore.ServerTimestamp},
			}
			if perfInfo.IsPerformance {
				updates = append(updates, firestore.Update{Path: "estimatedEarnings", Value: estimatedEarnings})
			}
			subDoc.Ref.Update(ctx, updates)

			// 6️⃣ Realtime DB 업데이트
			if h.realtimeDB != nil {
				realtimeData := map[string]interface{}{
					"creatorId":         creatorID,
					"creatorName":       creatorName,
					"platform":          "instagram",
					"mediaId":           mediaID,
					"viewCount":         stats.ViewCount,
					"likeCount":         stats.LikeCount,
					"savedCount":        stats.SavedCount,
					"shareCount":        stats.ShareCount,
					"estimatedEarnings": estimatedEarnings,
					"lastUpdated":       time.Now().Unix(),
				}
				h.updateRealtimeSubmission(ctx, competitionID, submissionID, realtimeData)
				h.updateRealtimeLeaderboard(ctx, competitionID, creatorID)
			}

			log.Printf("✅ Instagram 업데이트: %s - 조회수 %d, 예상수익 %d", submissionID, stats.ViewCount, estimatedEarnings)
			updatedCount++

			time.Sleep(200 * time.Millisecond) // Rate Limit
		}

		// 대회 통계 업데이트
		if h.realtimeDB != nil {
			h.updateCompetitionStats(ctx, competitionID)
		}
	}

	return InstagramUpdateResult{updatedCount, errorCount, totalVideos}
}

// fetchInstagramInsights - Instagram API로 미디어 통계 수집 (1회 호출)
// mediaType: "FEED", "REELS", "STORY", "" (모름)
func (h *InstagramCronHandler) fetchInstagramInsights(accessToken, mediaID, mediaType string) (InstagramMediaStats, error) {
	var stats InstagramMediaStats

	// STORY는 likes/saved 없음 → views,reach,shares만
	// FEED/REELS/미분류: views,reach,likes,saved,shares
	var metrics string
	if mediaType == "STORY" {
		metrics = "views,reach,shares"
	} else {
		metrics = "views,reach,likes,saved,shares"
	}

	parsed, err := h.callInsightsAPI(accessToken, mediaID, metrics)
	if err != nil {
		return stats, err
	}
	stats.ViewCount = parsed["views"]
	stats.ReachCount = parsed["reach"]
	stats.LikeCount = parsed["likes"]
	stats.SavedCount = parsed["saved"]
	stats.ShareCount = parsed["shares"]

	return stats, nil
}

// callInsightsAPI - Instagram Insights API 단일 호출 → metric명:값 맵 반환
func (h *InstagramCronHandler) callInsightsAPI(accessToken, mediaID, metrics string) (map[string]int64, error) {
	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/v21.0/%s/insights?metric=%s&access_token=%s",
		mediaID, metrics, accessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("📸 Instagram Insights [%s] metrics=%s: %s", mediaID, metrics, string(body))

	var result struct {
		Data []struct {
			Name   string `json:"name"`
			Values []struct {
				Value int64 `json:"value"`
			} `json:"values"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("JSON 파싱 실패: %w", err)
	}

	if result.Error.Code != 0 {
		return nil, fmt.Errorf("Instagram API 오류 (code %d): %s", result.Error.Code, result.Error.Message)
	}

	parsed := make(map[string]int64)
	for _, metric := range result.Data {
		if len(metric.Values) > 0 {
			parsed[metric.Name] = metric.Values[0].Value
		}
	}
	return parsed, nil
}

// refreshInstagramToken - Long-lived Token 갱신
func (h *InstagramCronHandler) refreshInstagramToken(accessToken string) (string, time.Time, error) {
	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/refresh_access_token?grant_type=ig_refresh_token&access_token=%s",
		accessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, err
	}

	if result.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("token refresh failed")
	}

	expiresAt := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return result.AccessToken, expiresAt, nil
}

// updateRealtimeSubmission - Realtime DB submission 업데이트
func (h *InstagramCronHandler) updateRealtimeSubmission(ctx context.Context, competitionID, submissionID string, data map[string]interface{}) {
	if h.realtimeDB == nil {
		return
	}
	ref := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions/%s", competitionID, submissionID))
	ref.Set(ctx, data)
}

// updateRealtimeLeaderboard - Realtime DB leaderboard 업데이트
func (h *InstagramCronHandler) updateRealtimeLeaderboard(ctx context.Context, competitionID, creatorID string) {
	if h.realtimeDB == nil {
		return
	}

	submissionsRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions", competitionID))
	var allSubmissions map[string]map[string]interface{}
	if err := submissionsRef.Get(ctx, &allSubmissions); err != nil || allSubmissions == nil {
		return
	}

	var totalViews int64
	var totalEstimatedEarnings int64 // ⭐ 성과형 추가
	var submissionCount int
	var creatorName string

	for _, submission := range allSubmissions {
		if subCreatorID, ok := submission["creatorId"].(string); ok && subCreatorID == creatorID {
			// viewCount 추출
			switch v := submission["viewCount"].(type) {
			case int64:
				totalViews += v
			case float64:
				totalViews += int64(v)
			case int:
				totalViews += int64(v)
			}

			// ⭐ estimatedEarnings 추출
			switch v := submission["estimatedEarnings"].(type) {
			case int64:
				totalEstimatedEarnings += v
			case float64:
				totalEstimatedEarnings += int64(v)
			case int:
				totalEstimatedEarnings += int64(v)
			}

			submissionCount++
			if creatorName == "" {
				creatorName, _ = submission["creatorName"].(string)
			}
		}
	}

	if submissionCount == 0 {
		return
	}

	leaderboardRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/leaderboard/%s", competitionID, creatorID))
	leaderboardRef.Set(ctx, map[string]interface{}{
		"creatorName":            creatorName,
		"totalViews":             totalViews,
		"totalEstimatedEarnings": totalEstimatedEarnings, // ⭐ 성과형
		"submissionCount":        submissionCount,
		"lastUpdated":            time.Now().Unix(),
	})

	log.Printf("✅ Instagram Leaderboard 업데이트: %s - 조회수 %d, 예상수익 %d (영상 %d개)", creatorName, totalViews, totalEstimatedEarnings, submissionCount)
}

// updateCompetitionStats - 대회 전체 통계 업데이트
func (h *InstagramCronHandler) updateCompetitionStats(ctx context.Context, competitionID string) {
	if h.realtimeDB == nil {
		return
	}

	submissionsRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions", competitionID))
	var allSubmissions map[string]map[string]interface{}
	if err := submissionsRef.Get(ctx, &allSubmissions); err != nil || len(allSubmissions) == 0 {
		return
	}

	var totalViews int64
	creatorSet := make(map[string]bool)

	for _, submission := range allSubmissions {
		if vc, ok := submission["viewCount"].(float64); ok {
			totalViews += int64(vc)
		}
		if creatorID, ok := submission["creatorId"].(string); ok {
			creatorSet[creatorID] = true
		}
	}

	statsRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/stats", competitionID))
	statsRef.Set(ctx, map[string]interface{}{
		"totalSubmissions": len(allSubmissions),
		"totalViews":       totalViews,
		"uniqueCreators":   len(creatorSet),
		"lastUpdated":      time.Now().Unix(),
	})
}

// CleanupExpiredTokens - 대회 종료 후 7일 지난 토큰 삭제
func (h *InstagramCronHandler) CleanupExpiredTokens(c *gin.Context) {
	ctx := context.Background()
	sevenDaysAgo := time.Now().AddDate(0, 0, -7)

	competitionsIter := h.firestore.Collection("competitions").
		Where("status", "==", "FINISHED").
		Documents(ctx)

	cleanedCount := 0

	for {
		doc, err := competitionsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			continue
		}

		competitionData := doc.Data()
		var finishedAt time.Time
		if ts, ok := competitionData["finishedAt"].(time.Time); ok {
			finishedAt = ts
		} else {
			continue
		}
		if !finishedAt.Before(sevenDaysAgo) {
			continue
		}

		competitionID := doc.Ref.ID

		submissionsIter := h.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Where("platform", "==", "instagram").
			Documents(ctx)

		for {
			subDoc, err := submissionsIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				continue
			}

			subDoc.Ref.Update(ctx, []firestore.Update{
				{Path: "instagramAuth", Value: firestore.Delete},
			})
			cleanedCount++
		}

		// Realtime DB 정리
		if h.realtimeDB != nil {
			ref := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s", competitionID))
			ref.Delete(ctx)
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "cleanedTokens": cleanedCount})
}

// ⭐ 성과형 대회 정보 조회
func (h *InstagramCronHandler) getCompetitionPerformanceInfo(ctx context.Context, competitionID string) *InstagramPerformanceInfo {
	doc, err := h.firestore.Collection("competitions").Doc(competitionID).Get(ctx)
	if err != nil {
		return &InstagramPerformanceInfo{IsPerformance: false}
	}
	data := doc.Data()

	compType, _ := data["competitionType"].(string)
	if compType != "performance" {
		return &InstagramPerformanceInfo{IsPerformance: false}
	}

	var pricePerView, minViews int64
	switch v := data["pricePerView"].(type) {
	case int64:
		pricePerView = v
	case float64:
		pricePerView = int64(v)
	case int:
		pricePerView = int64(v)
	}
	switch v := data["minViews"].(type) {
	case int64:
		minViews = v
	case float64:
		minViews = int64(v)
	case int:
		minViews = int64(v)
	}

	return &InstagramPerformanceInfo{
		IsPerformance: true,
		PricePerView:  pricePerView,
		MinViews:      minViews,
	}
}
