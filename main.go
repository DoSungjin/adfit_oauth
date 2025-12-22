package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/robfig/cron/v3"
	"google.golang.org/api/option"
	"gorm.io/gorm"

	"adfit-oauth/config"
	"adfit-oauth/handlers"
	"adfit-oauth/middleware"
	"adfit-oauth/models"
	"adfit-oauth/services"
)

func main() {
	// Config 로드
	if err := config.LoadConfig(""); err != nil {
		log.Printf("Warning: Failed to load config: %v", err)
	}

	// DB 초기화
	db, err := initDB()
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}

	// ⭐ Firebase Auth 초기화
	if err := middleware.InitFirebaseAuth(); err != nil {
		log.Printf("Warning: Firebase Auth initialization failed: %v", err)
	} else {
		log.Println("✅ Firebase Auth initialized")
	}

	// ⭐ Firestore Clients 초기화 (default + adtown-test)
	if _, err := services.InitFirestoreClients(); err != nil {
		log.Printf("Warning: FirestoreClients initialization failed: %v", err)
	}

	// Gin 모드 설정
	if config.Config != nil && !config.IsDebugMode() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// CORS 설정
	setupCORS(r)

	// 핸들러 설정
	setupHandlers(r, db)

	// Health Check
	r.GET("/health", func(c *gin.Context) {
		response := gin.H{
			"status": "ok",
			"services": gin.H{
				"oauth": "active",
				"stats": "active",
			},
		}

		if config.Config != nil {
			response["app"] = config.Config.App.Name
			response["version"] = config.Config.App.Version
			response["environment"] = config.Config.App.Environment
		}

		c.JSON(200, response)
	})

	// Cron 시작 - ⭐ setupHandlers에서 시작하도록 변경
	// go startTestCron()  // setupHandlers 내부에서 호출

	// 서버 시작
	port := getPort()
	log.Printf("Starting server on port %s", port)

	if config.Config != nil {
		log.Printf("Environment: %s", config.Config.App.Environment)
		log.Printf("Database: %s", config.GetDatabasePath())
	}

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}

// getPort returns the port to run the server on
func getPort() string {
	// 1. 환경변수에서 PORT 확인
	if port := os.Getenv("PORT"); port != "" {
		return port
	}

	// 2. Config에서 확인
	if config.Config != nil {
		return config.GetPort()
	}

	// 3. 기본값
	return "8080"
}

// initDB initializes the database
func initDB() (*gorm.DB, error) {
	var dbPath string

	if config.Config != nil {
		dbPath = config.GetDatabasePath()
	} else {
		dbPath = "adfit.db"
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Auto Migrate
	if err := db.AutoMigrate(&models.UserToken{}); err != nil {
		return nil, err
	}

	log.Printf("Database initialized at: %s", dbPath)
	return db, nil
}

// setupCORS configures CORS middleware
func setupCORS(r *gin.Engine) {
	// 기본 허용 origins
	allowedOrigins := config.GetAllowedOrigins()

	// 동적 CORS 미들웨어 (localhost의 모든 포트 허용)
	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// localhost로 시작하는 origin은 모두 허용 (개발 환경)
		if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, Session-Token, X-Admin-Token")
			c.Writer.Header().Set("Access-Control-Max-Age", "86400")

			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(204)
				return
			}
			c.Next()
			return
		}

		// 허용된 origins 체크
		for _, allowedOrigin := range allowedOrigins {
			if origin == allowedOrigin {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
				c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, Session-Token, X-Admin-Token")
				c.Writer.Header().Set("Access-Control-Max-Age", "86400")

				if c.Request.Method == "OPTIONS" {
					c.AbortWithStatus(204)
					return
				}
				break
			}
		}

		c.Next()
	})

	log.Printf("CORS configured with origins: %v (+ all localhost ports)", allowedOrigins)
}

// setupHandlers sets up all route handlers
func setupHandlers(r *gin.Engine, db *gorm.DB) {
	// TikTok routes
	setupTikTokRoutes(r, db)

	// YouTube routes
	setupYouTubeRoutes(r, db)

	// Instagram routes
	setupInstagramRoutes(r, db)

	// Admin routes
	setupAdminRoutes(r)

	// Report routes
	setupReportRoutes(r)

	// CSV routes
	setupCSVRoutes(r)

	// TikTok Cron routes - ⭐ 싱글톤 초기화 후 공유
	tiktokCronHandler, err := handlers.NewTikTokCronHandler()
	if err != nil {
		log.Printf("Warning: Failed to initialize TikTok Cron handler: %v", err)
		tiktokCronHandler = nil
	}
	setupTikTokCronRoutes(r, tiktokCronHandler)

	// ⭐ Cron 스케줄러에 동일한 핸들러 전달
	go startTestCron(tiktokCronHandler)

	log.Println("All handlers configured")
}

// setupTikTokRoutes sets up TikTok OAuth routes
func setupTikTokRoutes(r *gin.Engine, db *gorm.DB) {
	// Firestore 클라이언트 초기화
	firestoreClient, err := initFirestoreClient()
	if err != nil {
		log.Printf("Warning: Failed to initialize Firestore for TikTok: %v", err)
		firestoreClient = nil
	}

	tiktokHandler := &handlers.TikTokHandler{
		DB:        db,
		Firestore: firestoreClient,
	}

	// Public routes
	public := r.Group("/api/tiktok")
	{
		public.GET("/auth", tiktokHandler.GetAuthURL)
		public.GET("/callback", tiktokHandler.HandleCallback)
		public.POST("/token", tiktokHandler.ExchangeToken)
	}

	// Protected routes (JWT)
	protected := r.Group("/api/tiktok")
	protected.Use(middleware.AuthRequired())
	{
		protected.GET("/user", tiktokHandler.GetUserInfo)
		protected.GET("/videos", tiktokHandler.GetVideos)
		protected.POST("/refresh", tiktokHandler.RefreshToken)
		protected.POST("/logout", tiktokHandler.Logout)
	}

	// Protected routes (Firebase Auth)
	firestoreProtected := r.Group("/api/tiktok")
	firestoreProtected.Use(middleware.FirebaseAuthRequired())
	{
		firestoreProtected.POST("/submit-video", tiktokHandler.SubmitVideo)
	}

	log.Println("TikTok routes configured")
}

// setupYouTubeRoutes sets up YouTube OAuth routes
func setupYouTubeRoutes(r *gin.Engine, db *gorm.DB) {
	youtubeHandler, err := handlers.NewYouTubeHandlerFirestore()
	if err != nil {
		log.Printf("Warning: Failed to initialize YouTube Firestore handler: %v", err)
		return
	}

	// Public routes
	youtubePublic := r.Group("/api/youtube")
	{
		youtubePublic.GET("/auth", youtubeHandler.GetAuthURL)
		youtubePublic.GET("/callback", youtubeHandler.HandleCallback)
		youtubePublic.POST("/token", youtubeHandler.ExchangeToken)
		youtubePublic.GET("/video/:videoId", youtubeHandler.GetVideoInfo) // 영상 정보 조회 (공개)
	}

	// Protected routes
	youtubeProtected := r.Group("/api/youtube")
	youtubeProtected.Use(middleware.FirebaseAuthRequired())
	{
		youtubeProtected.GET("/user", youtubeHandler.GetUserInfo)
		youtubeProtected.GET("/channel", youtubeHandler.GetChannelInfo)
		youtubeProtected.GET("/videos", youtubeHandler.GetVideos)
		youtubeProtected.GET("/analytics/:videoId", youtubeHandler.GetVideoAnalytics)
		youtubeProtected.POST("/verify-and-save", youtubeHandler.VerifyAndSaveAnalytics)
		youtubeProtected.POST("/refresh", youtubeHandler.RefreshToken)
		youtubeProtected.POST("/logout", youtubeHandler.Logout)
	}

	log.Println("YouTube routes configured")
}

// setupInstagramRoutes sets up Instagram OAuth routes
func setupInstagramRoutes(r *gin.Engine, db *gorm.DB) {
	// Firestore 클라이언트 초기화
	firestoreClient, err := initFirestoreClient()
	if err != nil {
		log.Printf("Warning: Failed to initialize Firestore for Instagram: %v", err)
		firestoreClient = nil
	}

	instagramHandler := &handlers.InstagramHandler{
		DB:        db,
		Firestore: firestoreClient,
	}

	// Public routes (인증 불필요)
	public := r.Group("/api/instagram")
	{
		public.GET("/auth", instagramHandler.GetAuthURL)
		public.GET("/callback", instagramHandler.HandleCallback)
		// Facebook 정책 필수 엔드포인트
		public.POST("/data-deletion", instagramHandler.DataDeletion)
		public.POST("/deauthorize", instagramHandler.Deauthorize)
		// 테스트용 엔드포인트 (PowerShell 테스트)
		public.GET("/test/user", instagramHandler.TestGetUser)
		public.GET("/test/media", instagramHandler.TestGetMedia)
		public.GET("/test/insights/:mediaId", instagramHandler.TestGetInsights)
	}

	// Protected routes (JWT 인증)
	protected := r.Group("/api/instagram")
	protected.Use(middleware.AuthRequired())
	{
		protected.GET("/user", instagramHandler.GetUserInfoProtected)
		protected.GET("/media", instagramHandler.GetMedia)
		protected.GET("/insights/:mediaId", instagramHandler.GetMediaInsights)
		protected.POST("/refresh", instagramHandler.RefreshLongLivedToken)
		protected.POST("/logout", instagramHandler.Logout)
	}

	// Protected routes (Firebase Auth)
	firebaseProtected := r.Group("/api/instagram")
	firebaseProtected.Use(middleware.FirebaseAuthRequired())
	{
		firebaseProtected.POST("/submit-video", instagramHandler.SubmitVideo)
	}

	log.Println("Instagram routes configured")
}

// setupAdminRoutes sets up admin routes
func setupAdminRoutes(r *gin.Engine) {
	adminHandler, err := handlers.NewAdminStatsHandler()
	if err != nil {
		log.Printf("Warning: Failed to initialize admin handler: %v", err)
		return
	}

	// Admin routes
	adminGroup := r.Group("/api/admin")
	adminGroup.Use(adminHandler.AdminAuthRequired())
	{
		// System health
		adminGroup.GET("/system/health", adminHandler.GetSystemHealth)

		// ⭐ 통계 즉시 업데이트 (수동 트리거)
		adminGroup.POST("/stats/update-all", adminHandler.UpdateAllCompetitionStats)
		adminGroup.POST("/stats/update/:competitionId", adminHandler.UpdateSingleCompetitionStats)

		// 개별 대회 수상자 계산
		adminGroup.POST("/competitions/:competitionId/calculate-winners", adminHandler.CalculateWinners)

		// 대회 리포트 생성
		adminGroup.POST("/competitions/:competitionId/generate-report", adminHandler.GenerateReport)

		// ⭐ 상금 입금 완료 처리
		adminGroup.POST("/competitions/:competitionId/prize/complete/:userId", adminHandler.CompletePrizePayment)
	}

	log.Println("Admin routes configured")
}

// setupCSVRoutes sets up CSV validation routes
func setupCSVRoutes(r *gin.Engine) {
	csvHandler, err := handlers.NewCSVHandler("posted-app-c4ff5.firebasestorage.app")
	if err != nil {
		log.Printf("Warning: Failed to initialize CSV handler: %v", err)
		return
	}

	// Protected routes (Firebase Auth 필수)
	csvProtected := r.Group("/api/csv")
	csvProtected.Use(middleware.FirebaseAuthRequired())
	{
		csvProtected.POST("/validate-file1", csvHandler.ValidateAndSaveFile1)
		csvProtected.POST("/validate-file2", csvHandler.ValidateAndSaveFile2)
	}

	log.Println("CSV routes configured")
}

// setupReportRoutes sets up report management routes
func setupReportRoutes(r *gin.Engine) {
	reportHandler, err := handlers.NewReportHandler()
	if err != nil {
		log.Printf("Warning: Failed to initialize report handler: %v", err)
		return
	}

	// Admin routes for report management
	reportGroup := r.Group("/api/admin/reports")
	reportGroup.Use(middleware.AdminAuthRequired())
	{
		// 전체 신고 목록 조회 (필터링 가능)
		reportGroup.GET("", reportHandler.GetAllReports)

		// 대회별 신고 조회
		reportGroup.GET("/competition/:competitionId", reportHandler.GetReportsByCompetition)

		// 신고 상태 업데이트
		reportGroup.PUT("/:reportId", reportHandler.UpdateReportStatus)

		// 신고 통계
		reportGroup.GET("/stats", reportHandler.GetReportStats)
	}

	// Public route for creating reports (brand users)
	reportPublic := r.Group("/api/reports")
	reportPublic.Use(middleware.FirebaseAuthRequired())
	{
		// 신고 생성
		reportPublic.POST("", reportHandler.CreateReport)
	}

	log.Println("Report routes configured")
}

// startTestCron starts the cron scheduler for competition status checks
func startTestCron(tiktokCronHandler *handlers.TikTokCronHandler) {
	log.Println("Starting cron scheduler...")

	statsService, err := services.NewStatsService()
	if err != nil {
		log.Printf("Failed to initialize stats service: %v", err)
		return
	}

	// ⭐ TikTok Cron Handler는 매개변수로 전달받음 (중복 초기화 방지)

	// ⭐ 서버 시작 시 한 번 실행 (5초 후)
	go func() {
		time.Sleep(5 * time.Second)

		// 1. 대회 상태 체크 (APPROVED -> ONGOING, ONGOING -> FINISHED)
		runCompetitionStatusChecks(statsService)

		// 2. 활성 대회 통계 업데이트 (YouTube)
		if err := statsService.UpdateAllActiveCompetitions(); err != nil {
			log.Printf("❌ YouTube 초기 통계 업데이트 실패: %v", err)
		}

		// 3. ⭐ TikTok 통계 업데이트 (서버 시작 시 1회)
		if tiktokCronHandler != nil {
			log.Println("🚀 [서버 시작] TikTok 통계 초기 업데이트...")
			tiktokCronHandler.UpdateTikTokStatsInternal()
		}
	}()

	c := cron.New(cron.WithSeconds())

	// 매일 자정 5분에 실행 (대회 상태 체크)
	c.AddFunc("0 5 0 * * *", func() {
		// log.Println("Running daily competition status checks (00:05)")
		runCompetitionStatusChecks(statsService)
	})

	// 매일 새벽 1시에도 실행 (백업)
	c.AddFunc("0 0 1 * * *", func() {
		// log.Println("Running daily competition status checks (01:00)")
		runCompetitionStatusChecks(statsService)
	})

	// ⭐ 매 시간마다 ONGOING/FINISHED 대회 통계 업데이트 (참가자, 영상수, 조회수)
	c.AddFunc("0 0 * * * *", func() {
		// YouTube 통계 업데이트
		if err := statsService.UpdateAllActiveCompetitions(); err != nil {
			log.Printf("❌ [Hourly] YouTube 통계 업데이트 실패: %v", err)
		}

		// ⭐ TikTok 통계 업데이트
		if tiktokCronHandler != nil {
			log.Println("🔄 [Hourly] TikTok 통계 업데이트...")
			tiktokCronHandler.UpdateTikTokStatsInternal()
		}
	})

	c.Start()
	log.Println("Cron scheduler started")

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down cron scheduler...")
	c.Stop()
}

// runCompetitionStatusChecks runs all competition status checks
func runCompetitionStatusChecks(s *services.StatsService) {
	// 승인된 대회 시작 체크
	if err := s.CheckAndStartApprovedCompetitions(); err != nil {
		log.Printf("Error checking approved competitions: %v", err)
	}

	// 진행 중인 대회 종료 체크
	if err := s.CheckAndFinishOngoingCompetitions(); err != nil {
		log.Printf("Error checking ongoing competitions: %v", err)
	}

	// 보류 중인 대회 취소 체크
	if err := s.CheckAndCancelPendingCompetitions(); err != nil {
		log.Printf("Error checking pending competitions: %v", err)
	}

	log.Println("Competition status checks completed")
}

// initFirestoreClient initializes Firestore client
func initFirestoreClient() (*firestore.Client, error) {
	ctx := context.Background()

	var app *firebase.App
	var err error

	if config.Config != nil {
		if config.Config.Firebase.CredentialsPath != "" {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID: config.Config.Firebase.ProjectID,
			}, option.WithCredentialsFile(config.Config.Firebase.CredentialsPath))
		} else {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID: config.Config.Firebase.ProjectID,
			})
		}
	} else {
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID: "posted-app-c4ff5",
		})
	}

	if err != nil {
		return nil, fmt.Errorf("firebase 초기화 실패: %v", err)
	}

	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore 초기화 실패: %v", err)
	}

	return firestoreClient, nil
}

// setupTikTokCronRoutes sets up TikTok Cron routes
func setupTikTokCronRoutes(r *gin.Engine, tiktokCronHandler *handlers.TikTokCronHandler) {
	if tiktokCronHandler == nil {
		log.Println("Warning: TikTok Cron handler is nil, skipping routes")
		return
	}

	// Cron 라우트 (보안 토큰 필요)
	cronGroup := r.Group("/api/cron/tiktok")
	cronGroup.Use(func(c *gin.Context) {
		token := c.GetHeader("X-Cron-Token")
		if token == "" {
			token = c.GetHeader("Authorization")
			if token != "" && len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
		}
		
		// 환경변수에서 CRON_SECRET_TOKEN 확인
		expectedToken := os.Getenv("CRON_SECRET_TOKEN")
		if expectedToken == "" {
			expectedToken = "adfit-cron-secret-2025" // 기본값
		}
		
		if token != expectedToken {
			c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized"})
			return
		}
		c.Next()
	})
	{
		// 매시간 실행: TikTok 영상 통계 업데이트 (전체)
		cronGroup.POST("/update-stats", tiktokCronHandler.UpdateTikTokStats)
		
		// ⭐ 단일 대회 업데이트
		cronGroup.POST("/update-stats/:competitionId", tiktokCronHandler.UpdateSingleCompetitionStats)
		
		// ⭐ 단일 영상 업데이트
		cronGroup.POST("/update-submission/:competitionId/:submissionId", tiktokCronHandler.UpdateSingleSubmissionStats)
		
		// 매일 실행: 만료된 토큰 정리
		cronGroup.POST("/cleanup-tokens", tiktokCronHandler.CleanupExpiredTokens)
	}

	log.Println("TikTok Cron routes configured")
}
