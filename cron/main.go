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
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
	
	"adfit-oauth/models"
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
	db        *gorm.DB
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

	// SQLite DB 초기화 (Token 조회용)
	db, err := initCronDB()
	if err != nil {
		log.Printf("⚠️ SQLite DB 초기화 실패: %v", err)
		db = nil
	}

	return &TikTokCronService{
		firestore: firestoreClient,
		db:        db,
	}, nil
}

// initCronDB initializes SQLite database for TikTok tokens
func initCronDB() (*gorm.DB, error) {
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "../adfit.db" // cron 디렉토리에서 상대 경로
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	log.Printf("✅ SQLite DB 연결: %s", dbPath)
	return db, nil
}

// UpdateTikTokStats updates view counts for all TikTok submissions
func (s *TikTokCronService) UpdateTikTokStats() {
	ctx := context.Background()
	log.Println("🎯 [TikTok Cron] 조회수 업데이트 시작...")

	// 1. ONGOING 상태의 모든 대회 조회
	competitionsIter := s.firestore.Collection("competitions").
		Where("status", "==", "ONGOING").
		Documents(ctx)

	competitionCount := 0
	submissionCount := 0
	updateCount := 0

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

		// 2. 해당 대회의 TikTok submissions 조회 (tiktokAuth 있는 것만)
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
			submissionData := submissionDoc.Data()

			// tiktokAuth가 있는지 확인
			tiktokAuth, ok := submissionData["tiktokAuth"].(map[string]interface{})
			if !ok || tiktokAuth == nil {
				continue
			}

			jwt, ok := tiktokAuth["jwt"].(string)
			if !ok || jwt == "" {
				continue
			}

			videoID, ok := submissionData["tiktokData"].(map[string]interface{})["videoId"].(string)
			if !ok || videoID == "" {
				continue
			}

			// 3. TikTok API 호출하여 최신 통계 가져오기
			stats, err := s.fetchTikTokVideoStats(jwt, videoID)
			if err != nil {
				log.Printf("⚠️ TikTok API 실패 [%s]: %v", videoID, err)
				continue
			}

			// 4. Firestore submission 문서 업데이트 (YouTube와 동일한 방식)
			updateData := map[string]interface{}{
				"currentViewCount": stats.ViewCount,
				"viewCount":        stats.ViewCount,
				"likeCount":        stats.LikeCount,
				"commentCount":     stats.CommentCount,
				"shareCount":       stats.ShareCount,
				"updatedAt":        time.Now(),
			}

			if _, err := submissionDoc.Ref.Set(ctx, updateData, firestore.MergeAll); err != nil {
				log.Printf("❌ Firestore 업데이트 실패 [%s]: %v", videoID, err)
				continue
			}

			log.Printf("✅ [%s] 업데이트 완료 - Views: %d, Likes: %d", videoID, stats.ViewCount, stats.LikeCount)
			updateCount++
		}
	}

	log.Printf("✅ [TikTok Cron] 완료 - 대회: %d, Submissions: %d, 업데이트: %d",
		competitionCount, submissionCount, updateCount)
}

// TikTokVideoStats represents video statistics from TikTok API
type TikTokVideoStats struct {
	ViewCount    int `json:"view_count"`
	LikeCount    int `json:"like_count"`
	CommentCount int `json:"comment_count"`
	ShareCount   int `json:"share_count"`
}

// fetchTikTokVideoStats fetches video stats from TikTok API
func (s *TikTokCronService) fetchTikTokVideoStats(jwtToken string, videoID string) (*TikTokVideoStats, error) {
	// JWT 파싱하여 access token 가져오기
	token, err := jwt.Parse(jwtToken, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(os.Getenv("JWT_SECRET")), nil
	})

	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid JWT: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid JWT claims")
	}

	userID, _ := claims["user_id"].(string)

	// DB에서 access token 조회
	var userToken models.UserToken
	if s.db != nil {
		if err := s.db.Where("user_id = ?", userID).First(&userToken).Error; err != nil {
			return nil, fmt.Errorf("token not found: %v", err)
		}
	} else {
		return nil, fmt.Errorf("DB not initialized")
	}

	// TikTok API 호출
	fields := "view_count,like_count,comment_count,share_count"
	apiURL := fmt.Sprintf("https://open.tiktokapis.com/v2/video/query/?fields=%s", fields)

	reqBody := map[string]interface{}{
		"filters": map[string]interface{}{
			"video_ids": []string{videoID},
		},
	}

	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", userToken.AccessToken))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			Videos []TikTokVideoStats `json:"videos"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if result.Error.Code != "" && result.Error.Code != "ok" {
		return nil, fmt.Errorf("TikTok API error: %s - %s", result.Error.Code, result.Error.Message)
	}

	if len(result.Data.Videos) == 0 {
		return nil, fmt.Errorf("no video data found")
	}

	return &result.Data.Videos[0], nil
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
