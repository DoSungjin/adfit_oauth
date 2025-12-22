package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"adfit-oauth/config"
)

type TikTokCronHandler struct {
	firestore  *firestore.Client
	realtimeDB *db.Client
}

// TikTokSubmissionData - TikTok submission 데이터 구조
type TikTokSubmissionData struct {
	ID               string
	CompetitionID    string
	CreatorID        string
	CreatorName      string
	VideoID          string
	AccessToken      string
	CurrentViewCount int64
}

// TikTokVideoStats - TikTok API 응답 구조
type TikTokVideoStats struct {
	ViewCount    int64
	LikeCount    int64
	CommentCount int64
	ShareCount   int64
}

// TikTokTokenResponse - TikTok Token 응답 구조
type TikTokTokenResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// NewTikTokCronHandler - TikTok Cron Handler 생성
func NewTikTokCronHandler() (*TikTokCronHandler, error) {
	ctx := context.Background()

	// Firebase 초기화
	var app *firebase.App
	var err error

	databaseURL := "https://posted-app-c4ff5-default-rtdb.firebaseio.com"
	if config.Config != nil && config.Config.Firebase.DatabaseURL != "" {
		databaseURL = config.Config.Firebase.DatabaseURL
	}

	if config.Config != nil {
		if config.Config.Firebase.CredentialsPath != "" {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID:   config.Config.Firebase.ProjectID,
				DatabaseURL: databaseURL,
			}, option.WithCredentialsFile(config.Config.Firebase.CredentialsPath))
		} else {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID:   config.Config.Firebase.ProjectID,
				DatabaseURL: databaseURL,
			})
		}
	} else {
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID:   "posted-app-c4ff5",
			DatabaseURL: databaseURL,
		})
	}

	if err != nil {
		return nil, fmt.Errorf("firebase 초기화 실패: %v", err)
	}

	// Firestore 클라이언트
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore 초기화 실패: %v", err)
	}

	// Realtime Database 클라이언트
	realtimeDBClient, err := app.Database(ctx)
	if err != nil {
		log.Printf("⚠️ Realtime Database 초기화 실패: %v", err)
		realtimeDBClient = nil
	} else {
		log.Println("✅ Realtime Database 연동 완료 (TikTok)")
	}

	return &TikTokCronHandler{
		firestore:  firestoreClient,
		realtimeDB: realtimeDBClient,
	}, nil
}

// UpdateTikTokStatsInternal - 내부 호출용 (gin.Context 없이 호출 가능)
func (h *TikTokCronHandler) UpdateTikTokStatsInternal() {
	result := h.updateTikTokStatsCore()
	log.Printf("🎉 TikTok 업데이트 완료: %d/%d 성공", result.UpdatedCount, result.TotalVideos)
}

// UpdateTikTokStats - HTTP 핸들러 (gin.Context 필요)
func (h *TikTokCronHandler) UpdateTikTokStats(c *gin.Context) {
	result := h.updateTikTokStatsCore()
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"updatedCount": result.UpdatedCount,
		"errorCount":   result.ErrorCount,
		"totalVideos":  result.TotalVideos,
	})
}

// TikTokUpdateResult - 업데이트 결과 구조체
type TikTokUpdateResult struct {
	UpdatedCount int
	ErrorCount   int
	TotalVideos  int
}

// updateTikTokStatsCore - 핵심 로직 (공통)
func (h *TikTokCronHandler) updateTikTokStatsCore() TikTokUpdateResult {
	ctx := context.Background()
	log.Println("🔄 TikTok 통계 업데이트 시작...")

	// 1️⃣ 진행 중인 대회만 조회 (단일 쿼리 + 클라이언트 필터링)
	competitionsIter := h.firestore.Collection("competitions").
		Where("status", "in", []string{"APPROVED", "ONGOING"}).
		Documents(ctx)

	updatedCount := 0
	errorCount := 0
	totalVideos := 0

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
		// ⭐ 클라이언트 필터링: deleted 체크
		if deleted, ok := competitionData["deleted"].(bool); ok && deleted {
			continue
		}

		competitionID := doc.Ref.ID
		log.Printf("📊 대회 처리 중: %s", competitionID)

		// 2️⃣ 해당 대회의 TikTok submissions 조회 (단일 쿼리 + 클라이언트 필터링)
		submissionsIter := h.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Where("platform", "==", "tiktok").
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

			// ⭐ 클라이언트 필터링: isDeleted 체크
			if isDeleted, ok := submissionData["isDeleted"].(string); ok && isDeleted != "n" {
				continue
			}

			totalVideos++

			// 3️⃣ tiktokAuth 확인
			tiktokAuth, ok := submissionData["tiktokAuth"].(map[string]interface{})
			if !ok {
				log.Printf("⚠️ tiktokAuth 없음: %s", submissionID)
				continue
			}

			accessToken, _ := tiktokAuth["accessToken"].(string)
			refreshToken, _ := tiktokAuth["refreshToken"].(string)

			// ⭐ 토큰 만료 체크 (expiresAt)
			var expiresAt time.Time
			if expiresAtVal, ok := tiktokAuth["expiresAt"]; ok {
				switch v := expiresAtVal.(type) {
				case time.Time:
					expiresAt = v
				case int64:
					expiresAt = time.Unix(v, 0)
				case string:
					// ⭐ 문자열 파싱 추가 (ISO 8601 형식)
					if parsed, err := time.Parse(time.RFC3339, v); err == nil {
						expiresAt = parsed
					}
				}
			}

			// ⭐ 만료 1시간 전이면 갱신
			if time.Now().Add(1 * time.Hour).After(expiresAt) {
				log.Printf("🔄 Token 갱신 필요: %s", submissionID)
				newToken, err := h.refreshTikTokToken(refreshToken)
				if err != nil {
					log.Printf("❌ Token 갱신 실패 [%s]: %v", submissionID, err)
					errorCount++
					continue
				}

				// Firestore에 새 토큰 저장
				_, err = subDoc.Ref.Update(ctx, []firestore.Update{
					{Path: "tiktokAuth.accessToken", Value: newToken.AccessToken},
					{Path: "tiktokAuth.expiresAt", Value: newToken.ExpiresAt},
				})
				if err != nil {
					log.Printf("❌ Token 저장 실패 [%s]: %v", submissionID, err)
				} else {
					log.Printf("✅ Token 갱신 완료: %s", submissionID)
					accessToken = newToken.AccessToken
				}
			}

			// videoId 추출
			var videoID string
			if tiktokData, ok := submissionData["tiktokData"].(map[string]interface{}); ok {
				videoID, _ = tiktokData["videoId"].(string)
			}

			if accessToken == "" || videoID == "" {
				log.Printf("⚠️ Token/VideoID 없음: %s", submissionID)
				continue
			}

			creatorID, _ := submissionData["creatorId"].(string)
			creatorName, _ := submissionData["creatorName"].(string)

			// 4️⃣ TikTok API 호출 (갱신된 토큰 사용)
			stats, err := h.fetchTikTokVideoStats(accessToken, videoID)
			if err != nil {
				log.Printf("❌ TikTok API 실패 [%s]: %v", videoID, err)
				errorCount++
				continue
			}

			// 5️⃣ Firestore submissions 업데이트
			_, err = subDoc.Ref.Update(ctx, []firestore.Update{
				{Path: "currentViewCount", Value: stats.ViewCount},
				{Path: "likeCount", Value: stats.LikeCount},
				{Path: "commentCount", Value: stats.CommentCount},
				{Path: "shareCount", Value: stats.ShareCount},
				{Path: "lastStatsUpdate", Value: firestore.ServerTimestamp},
			})

			if err != nil {
				log.Printf("❌ Firestore 업데이트 실패 [%s]: %v", submissionID, err)
				errorCount++
				continue
			}

			// 6️⃣ ⭐ Realtime DB 업데이트 (YouTube와 동일한 방식)
			if h.realtimeDB != nil {
				realtimeData := map[string]interface{}{
					"creatorId":    creatorID,
					"creatorName":  creatorName,
					"platform":     "tiktok",
					"videoId":      videoID,
					"viewCount":    stats.ViewCount,
					"likeCount":    stats.LikeCount,
					"commentCount": stats.CommentCount,
					"shareCount":   stats.ShareCount,
					"lastUpdated":  time.Now().Unix(),
				}

				if err := h.updateRealtimeSubmission(ctx, competitionID, submissionID, realtimeData); err != nil {
					log.Printf("⚠️ Realtime DB submission 업데이트 실패 [%s]: %v", submissionID, err)
				} else {
					// ⭐ 리더보드도 업데이트
					if err := h.updateRealtimeLeaderboard(ctx, competitionID, creatorID); err != nil {
						log.Printf("⚠️ Realtime DB leaderboard 업데이트 실패 [%s/%s]: %v", competitionID, creatorID, err)
					}
				}
			}

			log.Printf("✅ 업데이트 완료: %s - 조회수 %d, 좋아요 %d", submissionID, stats.ViewCount, stats.LikeCount)
			updatedCount++

			// Rate Limit 방지 (TikTok API: 초당 10 requests)
			time.Sleep(150 * time.Millisecond)
		}

		// ⭐ 대회 전체 통계 업데이트
		if h.realtimeDB != nil {
			if err := h.updateCompetitionStats(ctx, competitionID); err != nil {
				log.Printf("⚠️ 대회 통계 업데이트 실패 [%s]: %v", competitionID, err)
			}
		}
	}

	log.Printf("🎉 TikTok 업데이트 완료: %d/%d 성공 (총 %d개 영상)", updatedCount, totalVideos, totalVideos)

	return TikTokUpdateResult{
		UpdatedCount: updatedCount,
		ErrorCount:   errorCount,
		TotalVideos:  totalVideos,
	}
}

// fetchTikTokVideoStats - TikTok API로 영상 통계 조회
func (h *TikTokCronHandler) fetchTikTokVideoStats(accessToken, videoID string) (*TikTokVideoStats, error) {
	// TikTok Video Query API
	apiURL := "https://open.tiktokapis.com/v2/video/query/?fields=id,view_count,like_count,comment_count,share_count"

	reqBody := map[string]interface{}{
		"filters": map[string]interface{}{
			"video_ids": []string{videoID},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("request 생성 실패: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API 호출 실패: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			Videos []struct {
				ID           string `json:"id"`
				ViewCount    int64  `json:"view_count"`
				LikeCount    int64  `json:"like_count"`
				CommentCount int64  `json:"comment_count"`
				ShareCount   int64  `json:"share_count"`
			} `json:"videos"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("JSON 파싱 실패: %w", err)
	}

	if result.Error.Code != "" && result.Error.Code != "ok" {
		return nil, fmt.Errorf("TikTok API 오류: %s - %s", result.Error.Code, result.Error.Message)
	}

	if len(result.Data.Videos) == 0 {
		return nil, fmt.Errorf("영상을 찾을 수 없음")
	}

	video := result.Data.Videos[0]
	return &TikTokVideoStats{
		ViewCount:    video.ViewCount,
		LikeCount:    video.LikeCount,
		CommentCount: video.CommentCount,
		ShareCount:   video.ShareCount,
	}, nil
}

// refreshTikTokToken - Refresh Token으로 Access Token 갱신
func (h *TikTokCronHandler) refreshTikTokToken(refreshToken string) (*TikTokTokenResponse, error) {
	// TikTok OAuth Token Refresh
	apiURL := "https://open.tiktokapis.com/v2/oauth/token/"

	// ⭐ Client Key/Secret (config 또는 환경변수)
	var clientKey, clientSecret string
	if config.Config != nil && config.Config.OAuth.TikTok.ClientID != "" {
		clientKey = config.Config.OAuth.TikTok.ClientID
		clientSecret = config.Config.OAuth.TikTok.ClientSecret
	} else {
		// 환경변수에서 가져오기
		clientKey = os.Getenv("TIKTOK_CLIENT_KEY")
		clientSecret = os.Getenv("TIKTOK_CLIENT_SECRET")
	}

	if clientKey == "" || clientSecret == "" {
		return nil, fmt.Errorf("TikTok Client credentials not found")
	}

	reqBody := map[string]string{
		"client_key":    clientKey,
		"client_secret": clientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("request 생성 실패: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API 호출 실패: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		} `json:"data"`
		Error       interface{} `json:"error"`        // ⭐ string 또는 struct 모두 처리
		ErrorCode   string      `json:"error_code"`   // ⭐ 대체 필드
		Description string      `json:"error_description"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("JSON 파싱 실패: %w, body: %s", err, string(body))
	}

	// ⭐ 에러 체크 (여러 형식 지원)
	if result.Error != nil {
		switch e := result.Error.(type) {
		case string:
			if e != "" && e != "ok" {
				return nil, fmt.Errorf("TikTok Token 갱신 오류: %s", e)
			}
		case map[string]interface{}:
			if code, ok := e["code"].(string); ok && code != "" && code != "ok" {
				msg, _ := e["message"].(string)
				return nil, fmt.Errorf("TikTok Token 갱신 오류: %s - %s", code, msg)
			}
		}
	}
	if result.ErrorCode != "" {
		return nil, fmt.Errorf("TikTok Token 갱신 오류: %s - %s", result.ErrorCode, result.Description)
	}

	expiresAt := time.Now().Add(time.Duration(result.Data.ExpiresIn) * time.Second)

	return &TikTokTokenResponse{
		AccessToken:  result.Data.AccessToken,
		RefreshToken: result.Data.RefreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// updateRealtimeSubmission - Realtime DB submission 업데이트
func (h *TikTokCronHandler) updateRealtimeSubmission(ctx context.Context, competitionID, submissionID string, data map[string]interface{}) error {
	if h.realtimeDB == nil {
		return fmt.Errorf("Realtime DB 없음")
	}

	ref := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions/%s", competitionID, submissionID))

	if err := ref.Set(ctx, data); err != nil {
		return fmt.Errorf("Realtime DB 저장 실패: %w", err)
	}

	return nil
}

// updateRealtimeLeaderboard - Realtime DB leaderboard 업데이트 (크리에이터별 합산)
func (h *TikTokCronHandler) updateRealtimeLeaderboard(ctx context.Context, competitionID, creatorID string) error {
	if h.realtimeDB == nil {
		return fmt.Errorf("Realtime DB 없음")
	}

	// 1. 해당 크리에이터의 모든 submissions 조회
	submissionsRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions", competitionID))

	var allSubmissions map[string]map[string]interface{}
	if err := submissionsRef.Get(ctx, &allSubmissions); err != nil {
		// ⭐ 데이터가 없으면 빈 맵으로 초기화 (에러가 아님)
		log.Printf("⚠️ Realtime DB submissions 조회 실패 (데이터 없음 가능): %v", err)
		allSubmissions = make(map[string]map[string]interface{})
	}

	// ⭐ nil 체크
	if allSubmissions == nil {
		allSubmissions = make(map[string]map[string]interface{})
	}

	// 2. creatorID로 필터링하여 합산
	var totalViews int64 = 0
	var totalLikes int64 = 0
	var submissionCount int = 0
	var creatorName string
	var topVideo struct {
		SubmissionID string
		ViewCount    int64
	}

	for submissionID, submission := range allSubmissions {
		// creatorId 체크
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
		}

		// likeCount 추출
		var likeCount int64
		switch v := submission["likeCount"].(type) {
		case int64:
			likeCount = v
		case float64:
			likeCount = int64(v)
		case int:
			likeCount = int64(v)
		}

		totalViews += viewCount
		totalLikes += likeCount
		submissionCount++

		// creatorName 저장
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

	// ⭐ 데이터가 없으면 leaderboard도 생성하지 않음 (정상)
	if submissionCount == 0 {
		log.Printf("⚠️ 크리에이터 %s의 submission이 아직 Realtime DB에 없음", creatorID)
		return nil
	}

	// 3. Leaderboard 업데이트
	leaderboardRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/leaderboard/%s", competitionID, creatorID))

	leaderboardData := map[string]interface{}{
		"creatorName":     creatorName,
		"totalViews":      totalViews,
		"totalLikes":      totalLikes,
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
		return fmt.Errorf("leaderboard 업데이트 실패: %w", err)
	}

	log.Printf("✅ Leaderboard 업데이트: %s - 조회수 %d (영상 %d개)", creatorName, totalViews, submissionCount)
	return nil
}

// updateCompetitionStats - 대회 전체 통계 계산 및 Realtime DB 업데이트
func (h *TikTokCronHandler) updateCompetitionStats(ctx context.Context, competitionID string) error {
	if h.realtimeDB == nil {
		return fmt.Errorf("Realtime DB 없음")
	}

	// 1. Realtime DB에서 해당 대회의 모든 submissions 조회
	submissionsRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/submissions", competitionID))

	var allSubmissions map[string]map[string]interface{}
	if err := submissionsRef.Get(ctx, &allSubmissions); err != nil {
		// ⭐ 데이터가 없으면 빈 맵으로 초기화
		log.Printf("⚠️ Realtime DB submissions 조회 실패 (데이터 없음 가능) [%s]: %v", competitionID, err)
		allSubmissions = make(map[string]map[string]interface{})
	}

	// ⭐ nil 체크
	if allSubmissions == nil {
		allSubmissions = make(map[string]map[string]interface{})
	}

	if len(allSubmissions) == 0 {
		log.Printf("⚠️ submissions 없음 (대회 시작 전 또는 데이터 아직 없음): %s", competitionID)
		return nil
	}

	// 2. 통계 계산
	var totalViews int64 = 0
	var totalLikes int64 = 0
	var totalComments int64 = 0
	var totalShares int64 = 0
	creatorSet := make(map[string]bool)

	for _, submission := range allSubmissions {
		// viewCount
		if viewCount, ok := submission["viewCount"]; ok {
			switch v := viewCount.(type) {
			case int64:
				totalViews += v
			case float64:
				totalViews += int64(v)
			case int:
				totalViews += int64(v)
			}
		}

		// likeCount
		if likeCount, ok := submission["likeCount"]; ok {
			switch v := likeCount.(type) {
			case int64:
				totalLikes += v
			case float64:
				totalLikes += int64(v)
			case int:
				totalLikes += int64(v)
			}
		}

		// commentCount
		if commentCount, ok := submission["commentCount"]; ok {
			switch v := commentCount.(type) {
			case int64:
				totalComments += v
			case float64:
				totalComments += int64(v)
			case int:
				totalComments += int64(v)
			}
		}

		// shareCount
		if shareCount, ok := submission["shareCount"]; ok {
			switch v := shareCount.(type) {
			case int64:
				totalShares += v
			case float64:
				totalShares += int64(v)
			case int:
				totalShares += int64(v)
			}
		}

		// uniqueCreators
		if creatorID, ok := submission["creatorId"].(string); ok && creatorID != "" {
			creatorSet[creatorID] = true
		}
	}

	totalSubmissions := len(allSubmissions)
	uniqueCreators := len(creatorSet)
	averageViews := float64(0)
	if totalSubmissions > 0 {
		averageViews = float64(totalViews) / float64(totalSubmissions)
	}

	// 3. Realtime DB에 대회 통계 저장
	statsRef := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s/stats", competitionID))

	statsData := map[string]interface{}{
		"totalSubmissions": totalSubmissions,
		"totalViews":       totalViews,
		"totalLikes":       totalLikes,
		"totalComments":    totalComments,
		"totalShares":      totalShares,
		"uniqueCreators":   uniqueCreators,
		"averageViews":     averageViews,
		"lastUpdated":      time.Now().Unix(),
	}

	if err := statsRef.Set(ctx, statsData); err != nil {
		return fmt.Errorf("stats 업데이트 실패: %w", err)
	}

	log.Printf("✅ 대회 통계 업데이트 완료 [%s]: 영상 %d개, 조회수 %d, 크리에이터 %d명",
		competitionID, totalSubmissions, totalViews, uniqueCreators)

	return nil
}

// CleanupExpiredTokens - 대회 종료 후 7일 지난 토큰 삭제 (매일 실행)
func (h *TikTokCronHandler) CleanupExpiredTokens(c *gin.Context) {
	ctx := context.Background()
	log.Println("🧹 TikTok 토큰 정리 시작...")

	sevenDaysAgo := time.Now().AddDate(0, 0, -7)

	// 종료된 대회 조회 (단일 쿼리 + 클라이언트 필터링)
	competitionsIter := h.firestore.Collection("competitions").
		Where("status", "==", "FINISHED").
		Documents(ctx)

	cleanedCount := 0
	cleanedCompetitions := 0

	for {
		doc, err := competitionsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			continue
		}

		competitionData := doc.Data()

		// ⭐ 클라이언트 필터링: finishedAt < 7일 전 체크
		var finishedAt time.Time
		if ts, ok := competitionData["finishedAt"].(time.Time); ok {
			finishedAt = ts
		} else {
			continue // finishedAt 없으면 스킵
		}
		if !finishedAt.Before(sevenDaysAgo) {
			continue // 7일 안 지났으면 스킵
		}

		competitionID := doc.Ref.ID
		cleanedCompetitions++

		// submissions의 tiktokAuth 필드 삭제
		submissionsIter := h.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Where("platform", "==", "tiktok"). // ⭐ 단일 필드로 조회
			Documents(ctx)

		for {
			subDoc, err := submissionsIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				continue
			}

			_, err = subDoc.Ref.Update(ctx, []firestore.Update{
				{Path: "tiktokAuth", Value: firestore.Delete},
			})

			if err == nil {
				cleanedCount++
			}
		}

		// ⭐ Realtime DB 정리
		if h.realtimeDB != nil {
			ref := h.realtimeDB.NewRef(fmt.Sprintf("realtime-stats/%s", competitionID))
			if err := ref.Delete(ctx); err != nil {
				log.Printf("⚠️ Realtime DB 정리 실패 [%s]: %v", competitionID, err)
			} else {
				log.Printf("✅ Realtime DB 정리 완료: %s", competitionID)
			}
		}
	}

	log.Printf("✅ 토큰 정리 완료: %d개 토큰, %d개 대회", cleanedCount, cleanedCompetitions)

	c.JSON(http.StatusOK, gin.H{
		"success":             true,
		"cleanedTokens":       cleanedCount,
		"cleanedCompetitions": cleanedCompetitions,
	})
}

// ⭐ UpdateSingleSubmissionStats - 단일 영상 통계 업데이트
func (h *TikTokCronHandler) UpdateSingleSubmissionStats(c *gin.Context) {
	ctx := context.Background()
	competitionID := c.Param("competitionId")
	submissionID := c.Param("submissionId")

	log.Printf("🔄 TikTok 단일 영상 업데이트: %s/%s", competitionID, submissionID)

	// 1. Firestore에서 submission 조회
	subDoc, err := h.firestore.Collection("competitions").
		Doc(competitionID).
		Collection("submissions").
		Doc(submissionID).
		Get(ctx)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Submission not found", "detail": err.Error()})
		return
	}

	submissionData := subDoc.Data()

	// platform 체크
	if platform, _ := submissionData["platform"].(string); platform != "tiktok" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Not a TikTok submission"})
		return
	}

	// tiktokAuth 확인
	tiktokAuth, ok := submissionData["tiktokAuth"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tiktokAuth not found"})
		return
	}

	accessToken, _ := tiktokAuth["accessToken"].(string)
	refreshToken, _ := tiktokAuth["refreshToken"].(string)

	// Token 만료 체크
	var expiresAt time.Time
	if expiresAtVal, ok := tiktokAuth["expiresAt"]; ok {
		switch v := expiresAtVal.(type) {
		case time.Time:
			expiresAt = v
		case int64:
			expiresAt = time.Unix(v, 0)
		case string:
			if parsed, err := time.Parse(time.RFC3339, v); err == nil {
				expiresAt = parsed
			}
		}
	}

	// 만료 1시간 전이면 갱신
	if time.Now().Add(1 * time.Hour).After(expiresAt) && refreshToken != "" {
		newToken, err := h.refreshTikTokToken(refreshToken)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token refresh failed", "detail": err.Error()})
			return
		}
		_, _ = subDoc.Ref.Update(ctx, []firestore.Update{
			{Path: "tiktokAuth.accessToken", Value: newToken.AccessToken},
			{Path: "tiktokAuth.expiresAt", Value: newToken.ExpiresAt},
		})
		accessToken = newToken.AccessToken
	}

	// videoId 추출
	var videoID string
	if tiktokData, ok := submissionData["tiktokData"].(map[string]interface{}); ok {
		videoID, _ = tiktokData["videoId"].(string)
	}

	if accessToken == "" || videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing accessToken or videoId"})
		return
	}

	creatorID, _ := submissionData["creatorId"].(string)
	creatorName, _ := submissionData["creatorName"].(string)

	// 2. TikTok API 호출
	stats, err := h.fetchTikTokVideoStats(accessToken, videoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "TikTok API failed", "detail": err.Error()})
		return
	}

	// 3. Firestore 업데이트
	_, err = subDoc.Ref.Update(ctx, []firestore.Update{
		{Path: "currentViewCount", Value: stats.ViewCount},
		{Path: "likeCount", Value: stats.LikeCount},
		{Path: "commentCount", Value: stats.CommentCount},
		{Path: "shareCount", Value: stats.ShareCount},
		{Path: "lastStatsUpdate", Value: firestore.ServerTimestamp},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Firestore update failed", "detail": err.Error()})
		return
	}

	// 4. Realtime DB 업데이트
	if h.realtimeDB != nil {
		realtimeData := map[string]interface{}{
			"creatorId":    creatorID,
			"creatorName":  creatorName,
			"platform":     "tiktok",
			"videoId":      videoID,
			"viewCount":    stats.ViewCount,
			"likeCount":    stats.LikeCount,
			"commentCount": stats.CommentCount,
			"shareCount":   stats.ShareCount,
			"lastUpdated":  time.Now().Unix(),
		}
		_ = h.updateRealtimeSubmission(ctx, competitionID, submissionID, realtimeData)
		_ = h.updateRealtimeLeaderboard(ctx, competitionID, creatorID)
	}

	log.Printf("✅ 단일 영상 업데이트 완료: %s - 조회수 %d", submissionID, stats.ViewCount)

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"submissionId": submissionID,
		"stats": gin.H{
			"viewCount":    stats.ViewCount,
			"likeCount":    stats.LikeCount,
			"commentCount": stats.CommentCount,
			"shareCount":   stats.ShareCount,
		},
	})
}

// ⭐ UpdateSingleCompetitionStats - 단일 대회 통계 업데이트
func (h *TikTokCronHandler) UpdateSingleCompetitionStats(c *gin.Context) {
	ctx := context.Background()
	competitionID := c.Param("competitionId")

	log.Printf("🔄 TikTok 단일 대회 업데이트: %s", competitionID)

	// 1. 대회 존재 확인
	compDoc, err := h.firestore.Collection("competitions").Doc(competitionID).Get(ctx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Competition not found", "detail": err.Error()})
		return
	}
	_ = compDoc // 사용 확인

	// 2. 해당 대회의 TikTok submissions 조회
	submissionsIter := h.firestore.Collection("competitions").
		Doc(competitionID).
		Collection("submissions").
		Where("platform", "==", "tiktok").
		Documents(ctx)

	updatedCount := 0
	errorCount := 0
	totalVideos := 0

	for {
		subDoc, err := submissionsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			continue
		}

		submissionData := subDoc.Data()
		submissionID := subDoc.Ref.ID

		// isDeleted 체크
		if isDeleted, ok := submissionData["isDeleted"].(string); ok && isDeleted != "n" {
			continue
		}

		totalVideos++

		// tiktokAuth 확인
		tiktokAuth, ok := submissionData["tiktokAuth"].(map[string]interface{})
		if !ok {
			continue
		}

		accessToken, _ := tiktokAuth["accessToken"].(string)
		refreshToken, _ := tiktokAuth["refreshToken"].(string)

		// Token 만료 체크
		var expiresAt time.Time
		if expiresAtVal, ok := tiktokAuth["expiresAt"]; ok {
			switch v := expiresAtVal.(type) {
			case time.Time:
				expiresAt = v
			case int64:
				expiresAt = time.Unix(v, 0)
			case string:
				if parsed, err := time.Parse(time.RFC3339, v); err == nil {
					expiresAt = parsed
				}
			}
		}

		if time.Now().Add(1*time.Hour).After(expiresAt) && refreshToken != "" {
			newToken, err := h.refreshTikTokToken(refreshToken)
			if err != nil {
				errorCount++
				continue
			}
			_, _ = subDoc.Ref.Update(ctx, []firestore.Update{
				{Path: "tiktokAuth.accessToken", Value: newToken.AccessToken},
				{Path: "tiktokAuth.expiresAt", Value: newToken.ExpiresAt},
			})
			accessToken = newToken.AccessToken
		}

		// videoId 추출
		var videoID string
		if tiktokData, ok := submissionData["tiktokData"].(map[string]interface{}); ok {
			videoID, _ = tiktokData["videoId"].(string)
		}

		if accessToken == "" || videoID == "" {
			continue
		}

		creatorID, _ := submissionData["creatorId"].(string)
		creatorName, _ := submissionData["creatorName"].(string)

		// TikTok API 호출
		stats, err := h.fetchTikTokVideoStats(accessToken, videoID)
		if err != nil {
			errorCount++
			continue
		}

		// Firestore 업데이트
		_, err = subDoc.Ref.Update(ctx, []firestore.Update{
			{Path: "currentViewCount", Value: stats.ViewCount},
			{Path: "likeCount", Value: stats.LikeCount},
			{Path: "commentCount", Value: stats.CommentCount},
			{Path: "shareCount", Value: stats.ShareCount},
			{Path: "lastStatsUpdate", Value: firestore.ServerTimestamp},
		})
		if err != nil {
			errorCount++
			continue
		}

		// Realtime DB 업데이트
		if h.realtimeDB != nil {
			realtimeData := map[string]interface{}{
				"creatorId":    creatorID,
				"creatorName":  creatorName,
				"platform":     "tiktok",
				"videoId":      videoID,
				"viewCount":    stats.ViewCount,
				"likeCount":    stats.LikeCount,
				"commentCount": stats.CommentCount,
				"shareCount":   stats.ShareCount,
				"lastUpdated":  time.Now().Unix(),
			}
			_ = h.updateRealtimeSubmission(ctx, competitionID, submissionID, realtimeData)
			_ = h.updateRealtimeLeaderboard(ctx, competitionID, creatorID)
		}

		updatedCount++
		time.Sleep(150 * time.Millisecond) // Rate Limit
	}

	// 대회 전체 통계 업데이트
	if h.realtimeDB != nil {
		_ = h.updateCompetitionStats(ctx, competitionID)
	}

	log.Printf("✅ 단일 대회 업데이트 완료: %s - %d/%d 성공", competitionID, updatedCount, totalVideos)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"competitionId": competitionID,
		"updatedCount":  updatedCount,
		"errorCount":    errorCount,
		"totalVideos":   totalVideos,
	})
}
