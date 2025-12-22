package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	"google.golang.org/api/iterator"

	"adfit-oauth/services"
)

func main() {
	if err := godotenv.Load(); err != nil {
		// log.Println("No .env file found")
	}

	log.Println("🚀 Cron 서버 시작...")

	statsService, err := services.NewStatsService()
	if err != nil {
		log.Fatalf("❌ StatsService 초기화 실패: %v", err)
	}

	// TikTok Cron Service 초기화
	tiktokCron, err := NewTikTokCronService()
	if err != nil {
		log.Printf("⚠️ TikTok Cron Service 초기화 실패: %v", err)
		tiktokCron = nil
	} else {
		log.Println("✅ TikTok Cron Service 초기화 성공")
	}

	// ⭐ 서버 시작 시 한 번 실행 (5초 후)
	go func() {
		time.Sleep(5 * time.Second)
		// log.Println("🚀 [Cron 시작] 대회 상태 체크 및 통계 초기 업데이트...")
		
		statsService.CheckAndStartApprovedCompetitions()
		statsService.CheckAndFinishOngoingCompetitions()
		statsService.UpdateAllActiveCompetitions()
		
		// log.Println("✅ 초기화 완료")
	}()

	kst := time.FixedZone("KST", 9*60*60)
	c := cron.New(cron.WithLocation(kst), cron.WithSeconds())

	// 매일 자정 5분 - 대회 상태 체크
	_, err = c.AddFunc("0 5 0 * * *", func() {
		// log.Println("⏰ [00:05] 대회 상태 체크")
		statsService.CheckAndStartApprovedCompetitions()
		statsService.CheckAndFinishOngoingCompetitions()
	})
	if err != nil {
		log.Fatalf("❌ 자정 크론 등록 실패: %v", err)
	}

	// 매일 새벽 1시 - 백업 체크
	_, err = c.AddFunc("0 0 1 * * *", func() {
		// log.Println("⏰ [01:00] 백업 체크")
		statsService.CheckAndStartApprovedCompetitions()
		statsService.CheckAndFinishOngoingCompetitions()
	})
	if err != nil {
		log.Fatalf("❌ 새벽 1시 크론 등록 실패: %v", err)
	}

	// 매일 새벽 2시 - 시스템 통계
	_, err = c.AddFunc("0 0 2 * * *", func() {
		// log.Println("⏰ [02:00] 시스템 통계")
		statsService.SaveDailyAggregation()
		statsService.UpdateDailySystemStats()
	})
	if err != nil {
		log.Printf("⚠️ 새벽 2시 크론 등록 실패: %v", err)
	}

	// ⭐ 매 시간 정시 - 활성 대회 통계
	_, err = c.AddFunc("0 0 * * * *", func() {
		// log.Println("⏰ [매 시간] 활성 대회 통계")
		statsService.UpdateAllActiveCompetitions()
	})
	if err != nil {
		log.Printf("⚠️ 매 시간 크론 등록 실패: %v", err)
	}

	// ⭐ 매 시간 10분 - TikTok 조회수 업데이트
	if tiktokCron != nil {
		_, err = c.AddFunc("0 10 * * * *", func() {
			// log.Println("⏰ [매 시간] TikTok 조회수 업데이트")
			tiktokCron.UpdateTikTokStats()
		})
		if err != nil {
			log.Printf("⚠️ TikTok 크론 등록 실패: %v", err)
		}
	}

	// ⭐ 매일 새벽 3시 - TikTok Token 정리
	if tiktokCron != nil {
		_, err = c.AddFunc("0 0 3 * * *", func() {
			// log.Println("⏰ [03:00] TikTok Token 정리")
			tiktokCron.CleanupExpiredTokens()
		})
		if err != nil {
			log.Printf("⚠️ TikTok Token 정리 크론 등록 실패: %v", err)
		}
	}

	c.Start()
	log.Println("✅ Cron 스케줄러 시작 완료")

	entries := c.Entries()
	log.Printf("📋 등록된 Cron Job: %d개", len(entries))
	for i, entry := range entries {
		log.Printf("  %d. 다음 실행: %s", i+1, entry.Next.Format("2006-01-02 15:04:05"))
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("🛑 Cron 스케줄러 종료 중...")
	c.Stop()
	log.Println("✅ Cron 스케줄러 종료 완료")
}

// ==================== TikTok Cron Service ====================

// TikTokCronService handles TikTok-related cron jobs
type TikTokCronService struct {
	firestore *firestore.Client
}

// NewTikTokCronService creates a new TikTok cron service
func NewTikTokCronService() (*TikTokCronService, error) {
	ctx := context.Background()

	// Firebase 초기화
	app, err := firebase.NewApp(ctx, &firebase.Config{
		ProjectID: "posted-app-c4ff5",
	})
	if err != nil {
		return nil, fmt.Errorf("firebase 초기화 실패: %v", err)
	}

	// Firestore 클라이언트 생성
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore 초기화 실패: %v", err)
	}

	return &TikTokCronService{
		firestore: firestoreClient,
	}, nil
}

// UpdateTikTokStats updates view counts for all TikTok submissions
func (s *TikTokCronService) UpdateTikTokStats() {
	ctx := context.Background()
	log.Println("🎯 [TikTok Cron] 조회수 업데이트 시작...")

	// 1. APPROVED, ONGOING 상태의 모든 대회 조회
	competitionsIter := s.firestore.Collection("competitions").
		Where("status", "in", []string{"APPROVED", "ONGOING"}).
		Where("deleted", "==", false).
		Documents(ctx)

	competitionCount := 0
	submissionCount := 0
	updateCount := 0
	errorCount := 0

	for {
		competitionDoc, err := competitionsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ 대회 조회 실패: %v", err)
			continue
		}

		competitionID := competitionDoc.Ref.ID
		competitionCount++

		// 2. 해당 대회의 TikTok submissions 조회
		submissionsIter := s.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Where("platform", "==", "tiktok").
			Where("isDeleted", "==", "n").
			Documents(ctx)

		for {
			submissionDoc, err := submissionsIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("❌ Submission 조회 실패: %v", err)
				continue
			}

			submissionCount++
			submissionID := submissionDoc.Ref.ID
			submissionData := submissionDoc.Data()

			// ⭐ tiktokAuth에서 직접 토큰 조회 (Firestore submission에서)
			tiktokAuth, ok := submissionData["tiktokAuth"].(map[string]interface{})
			if !ok || tiktokAuth == nil {
				log.Printf("⚠️ tiktokAuth 없음: %s", submissionID)
				continue
			}

			accessToken, _ := tiktokAuth["accessToken"].(string)
			refreshToken, _ := tiktokAuth["refreshToken"].(string)

			if accessToken == "" {
				log.Printf("⚠️ accessToken 없음: %s", submissionID)
				continue
			}

			// ⭐ 토큰 만료 체크 (expiresAt)
			var expiresAt time.Time
			if expiresAtVal, ok := tiktokAuth["expiresAt"]; ok {
				switch v := expiresAtVal.(type) {
				case time.Time:
					expiresAt = v
				case string:
					expiresAt, _ = time.Parse(time.RFC3339, v)
				}
			}

			// ⭐ 만료 1시간 전이면 갱신
			if !expiresAt.IsZero() && time.Now().Add(1*time.Hour).After(expiresAt) && refreshToken != "" {
				log.Printf("🔄 Token 갱신 필요: %s", submissionID)
				newToken, err := s.refreshTikTokToken(refreshToken)
				if err != nil {
					log.Printf("❌ Token 갱신 실패 [%s]: %v", submissionID, err)
					errorCount++
					continue
				}

				// Firestore에 새 토큰 저장
				_, err = submissionDoc.Ref.Update(ctx, []firestore.Update{
					{Path: "tiktokAuth.accessToken", Value: newToken.AccessToken},
					{Path: "tiktokAuth.refreshToken", Value: newToken.RefreshToken},
					{Path: "tiktokAuth.expiresAt", Value: newToken.ExpiresAt.Format(time.RFC3339)},
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

			if videoID == "" {
				log.Printf("⚠️ videoId 없음: %s", submissionID)
				continue
			}

			// 3. TikTok API 호출 (accessToken 직접 사용)
			stats, err := s.fetchTikTokVideoStatsWithToken(accessToken, videoID)
			if err != nil {
				log.Printf("⚠️ TikTok API 실패 [%s]: %v", videoID, err)
				errorCount++
				continue
			}

			// 4. Firestore submission 문서 업데이트
			_, err = submissionDoc.Ref.Update(ctx, []firestore.Update{
				{Path: "currentViewCount", Value: stats.ViewCount},
				{Path: "viewCount", Value: stats.ViewCount},
				{Path: "likeCount", Value: stats.LikeCount},
				{Path: "commentCount", Value: stats.CommentCount},
				{Path: "shareCount", Value: stats.ShareCount},
				{Path: "lastStatsUpdate", Value: time.Now()},
			})

			if err != nil {
				log.Printf("❌ Firestore 업데이트 실패 [%s]: %v", videoID, err)
				errorCount++
				continue
			}

			log.Printf("✅ [%s] 업데이트 완료 - Views: %d, Likes: %d", videoID, stats.ViewCount, stats.LikeCount)
			updateCount++

			// Rate Limit 방지 (TikTok API: 초당 10 requests)
			time.Sleep(150 * time.Millisecond)
		}
	}

	log.Printf("✅ [TikTok Cron] 완료 - 대회: %d, Submissions: %d, 업데이트: %d, 오류: %d",
		competitionCount, submissionCount, updateCount, errorCount)
}

// TikTokVideoStats represents video statistics from TikTok API
type TikTokVideoStats struct {
	ViewCount    int64 `json:"view_count"`
	LikeCount    int64 `json:"like_count"`
	CommentCount int64 `json:"comment_count"`
	ShareCount   int64 `json:"share_count"`
}

// TikTokTokenResponse - Token 갱신 응답
type TikTokTokenResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// fetchTikTokVideoStatsWithToken - accessToken으로 직접 TikTok API 호출
func (s *TikTokCronService) fetchTikTokVideoStatsWithToken(accessToken string, videoID string) (*TikTokVideoStats, error) {
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
func (s *TikTokCronService) refreshTikTokToken(refreshToken string) (*TikTokTokenResponse, error) {
	apiURL := "https://open.tiktokapis.com/v2/oauth/token/"

	clientKey := os.Getenv("TIKTOK_CLIENT_KEY")
	clientSecret := os.Getenv("TIKTOK_CLIENT_SECRET")

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
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("JSON 파싱 실패: %w", err)
	}

	if result.Error.Code != "" && result.Error.Code != "ok" {
		return nil, fmt.Errorf("TikTok Token 갱신 오류: %s - %s", result.Error.Code, result.Error.Message)
	}

	expiresAt := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)

	return &TikTokTokenResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// CleanupExpiredTokens removes tiktokAuth from submissions of completed competitions
func (s *TikTokCronService) CleanupExpiredTokens() {
	ctx := context.Background()
	log.Println("🧹 [TikTok Cron] 만료 Token 정리 시작...")

	// 대회 종료 후 7일 지난 대회 조회
	cutoffDate := time.Now().AddDate(0, 0, -7)

	competitionsIter := s.firestore.Collection("competitions").
		Where("status", "==", "FINISHED").
		Where("finishedAt", "<", cutoffDate).
		Documents(ctx)

	deleteCount := 0

	for {
		competitionDoc, err := competitionsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ 대회 조회 실패: %v", err)
			continue
		}

		competitionID := competitionDoc.Ref.ID

		// submissions에서 tiktokAuth 삭제
		submissionsIter := s.firestore.Collection("competitions").
			Doc(competitionID).
			Collection("submissions").
			Where("platform", "==", "tiktok").
			Documents(ctx)

		batch := s.firestore.Batch()
		batchCount := 0

		for {
			submissionDoc, err := submissionsIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				continue
			}

			// tiktokAuth 삭제
			batch.Update(submissionDoc.Ref, []firestore.Update{
				{Path: "tiktokAuth", Value: firestore.Delete},
			})
			batchCount++

			// Batch 크기 제한 (500개)
			if batchCount >= 500 {
				if _, err := batch.Commit(ctx); err != nil {
					log.Printf("❌ Batch commit 실패: %v", err)
				} else {
					deleteCount += batchCount
				}
				batch = s.firestore.Batch()
				batchCount = 0
			}
		}

		// 남은 batch commit
		if batchCount > 0 {
			if _, err := batch.Commit(ctx); err != nil {
				log.Printf("❌ Batch commit 실패: %v", err)
			} else {
				deleteCount += batchCount
			}
		}
	}

	log.Printf("✅ [TikTok Cron] Token 정리 완료 - %d개 삭제", deleteCount)
}
