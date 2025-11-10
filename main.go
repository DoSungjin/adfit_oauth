package main

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/robfig/cron/v3"
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

	// Cron 시작
	go startTestCron()

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
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, Session-Token")
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
				c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, Session-Token")
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

	// Admin routes
	setupAdminRoutes(r)

	log.Println("All handlers configured")
}

// setupTikTokRoutes sets up TikTok OAuth routes
func setupTikTokRoutes(r *gin.Engine, db *gorm.DB) {
	tiktokHandler := &handlers.TikTokHandler{DB: db}

	// Public routes
	public := r.Group("/api/tiktok")
	{
		public.GET("/auth", tiktokHandler.GetAuthURL)
		public.GET("/callback", tiktokHandler.HandleCallback)
		public.POST("/token", tiktokHandler.ExchangeToken)
	}

	// Protected routes
	protected := r.Group("/api/tiktok")
	protected.Use(middleware.AuthRequired())
	{
		protected.GET("/user", tiktokHandler.GetUserInfo)
		protected.GET("/videos", tiktokHandler.GetVideos)
		protected.POST("/refresh", tiktokHandler.RefreshToken)
		protected.POST("/logout", tiktokHandler.Logout)
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
	}

	log.Println("Admin routes configured")
}

// startTestCron starts the cron scheduler for competition status checks
func startTestCron() {
	log.Println("Starting cron scheduler...")

	statsService, err := services.NewStatsService()
	if err != nil {
		log.Printf("Failed to initialize stats service: %v", err)
		return
	}

	c := cron.New(cron.WithSeconds())

	// 매일 자정에 실행
	c.AddFunc("0 0 0 * * *", func() {
		log.Println("Running daily competition status checks (00:00)")
		runCompetitionStatusChecks(statsService)
	})

	// 매일 새벽 1시에도 실행 (백업)
	c.AddFunc("0 0 1 * * *", func() {
		log.Println("Running daily competition status checks (01:00)")
		runCompetitionStatusChecks(statsService)
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
