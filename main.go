package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	
	"github.com/gin-contrib/cors"
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
	// 설정 파일 로드
	if err := config.LoadConfig(""); err != nil {
		log.Printf("⚠️ 설정 파일 로드 실패, 기본값 사용: %v", err)
		// 기본 설정으로 계속 진행
	}

	// 데이터베이스 초기화
	db, err := initDatabase()
	if err != nil {
		log.Fatalf("❌ 데이터베이스 초기화 실패: %v", err)
	}

	// Gin 엔진 설정
	if config.Config != nil && !config.IsDebugMode() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// CORS 설정
	setupCORS(r)

	// 핸들러 초기화
	setupHandlers(r, db)

	// 헬스 체크
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

	// Cron 작업 시작 (설정이 있고 활성화되어 있을 때만)
	if config.Config != nil && config.IsFeatureEnabled("cron") {
		go startCronJobs()
	}

	// 서버 시작
	port := getPort()
	log.Printf("🚀 AdFit OAuth Server 시작 (포트: %s)", port)
	
	if config.Config != nil {
		log.Printf("   앱: %s v%s", config.Config.App.Name, config.Config.App.Version)
		log.Printf("   환경: %s", config.Config.App.Environment)
	}
	
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("❌ 서버 시작 실패: %v", err)
	}
}

// 포트 가져오기
func getPort() string {
	// 1. 환경 변수에서
	if port := os.Getenv("PORT"); port != "" {
		return port
	}
	
	// 2. config에서
	if config.Config != nil {
		return config.GetPort()
	}
	
	// 3. 기본값
	return "8080"
}

// 데이터베이스 초기화
func initDatabase() (*gorm.DB, error) {
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
	
	// 테이블 자동 생성
	if err := db.AutoMigrate(&models.UserToken{}); err != nil {
		return nil, err
	}
	
	log.Printf("✅ 데이터베이스 연결 완료: %s", dbPath)
	return db, nil
}

// CORS 설정
func setupCORS(r *gin.Engine) {
	var corsConfig cors.Config
	
	if config.Config != nil {
		corsConfig = cors.Config{
			AllowOrigins:     config.Config.CORS.AllowedOrigins,
			AllowMethods:     config.Config.CORS.AllowedMethods,
			AllowHeaders:     config.Config.CORS.AllowedHeaders,
			ExposeHeaders:    config.Config.CORS.ExposeHeaders,
			AllowCredentials: config.Config.CORS.AllowCredentials,
		}
	} else {
		// 기본 CORS 설정
		corsConfig = cors.Config{
			AllowOrigins:     []string{"*"},
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
			ExposeHeaders:    []string{"Content-Length"},
			AllowCredentials: false,
		}
	}
	
	r.Use(cors.New(corsConfig))
	log.Printf("✅ CORS 설정 완료")
}

// 핸들러 설정
func setupHandlers(r *gin.Engine, db *gorm.DB) {
	// TikTok 핸들러 (항상 활성화)
	setupTikTokRoutes(r, db)
	log.Println("✅ TikTok API 라우트 활성화")
	
	// YouTube 핸들러 (항상 활성화)
	setupYouTubeRoutes(r, db)
	log.Println("✅ YouTube API 라우트 활성화")
	
	// 통계 핸들러
	if config.Config == nil || config.IsFeatureEnabled("stats") {
		setupStatsRoutes(r)
		log.Println("✅ 통계 API 라우트 활성화")
	}
	
	// 관리자 핸들러
	setupAdminRoutes(r)
	log.Println("✅ 관리자 API 라우트 활성화")
}

// TikTok 라우트 설정
func setupTikTokRoutes(r *gin.Engine, db *gorm.DB) {
	tiktokHandler := &handlers.TikTokHandler{DB: db}
	
	// 공개 라우트
	public := r.Group("/api/tiktok")
	{
		public.GET("/auth", tiktokHandler.GetAuthURL)
		public.GET("/callback", tiktokHandler.HandleCallback)
		public.POST("/token", tiktokHandler.ExchangeToken)
	}
	
	// 인증 필요 라우트
	protected := r.Group("/api/tiktok")
	protected.Use(middleware.AuthRequired())
	{
		protected.GET("/user", tiktokHandler.GetUserInfo)
		protected.GET("/videos", tiktokHandler.GetVideos)
		protected.POST("/refresh", tiktokHandler.RefreshToken)
		protected.POST("/logout", tiktokHandler.Logout)
	}
}

// YouTube 라우트 설정
func setupYouTubeRoutes(r *gin.Engine, db *gorm.DB) {
	youtubeHandler := handlers.NewYouTubeHandler(db)
	
	// 공개 라우트
	youtubePublic := r.Group("/api/youtube")
	{
		youtubePublic.GET("/auth", youtubeHandler.GetAuthURL)
		youtubePublic.GET("/callback", youtubeHandler.HandleCallback)
		youtubePublic.POST("/token", youtubeHandler.ExchangeToken)
	}
	
	// 인증 필요 라우트
	youtubeProtected := r.Group("/api/youtube")
	youtubeProtected.Use(middleware.AuthRequired())
	{
		youtubeProtected.GET("/user", youtubeHandler.GetUserInfo)
		youtubeProtected.GET("/channel", youtubeHandler.GetChannelInfo)
		youtubeProtected.GET("/videos", youtubeHandler.GetVideos)
		youtubeProtected.GET("/analytics/:videoId", youtubeHandler.GetVideoAnalytics)
		youtubeProtected.POST("/refresh", youtubeHandler.RefreshToken)
		youtubeProtected.POST("/logout", youtubeHandler.Logout)
	}
}

// 통계 라우트 설정
func setupStatsRoutes(r *gin.Engine) {
	statsHandler, err := handlers.NewStatsHandler()
	if err != nil {
		log.Printf("⚠️ StatsHandler 초기화 실패: %v", err)
		return
	}

	statsGroup := r.Group("/api/stats")
	{
		statsGroup.GET("/health", statsHandler.GetStatsStatus)
		statsGroup.POST("/update/all", statsHandler.UpdateAllActiveCompetitions)
		statsGroup.POST("/update/competition/:id", statsHandler.UpdateCompetitionStats)
	}
}

// 관리자 라우트 설정
func setupAdminRoutes(r *gin.Engine) {
	adminHandler, err := handlers.NewAdminStatsHandler()
	if err != nil {
		log.Printf("⚠️ AdminStatsHandler 초기화 실패: %v", err)
		return
	}

	// 관리자 API 그룹 (인증 필요)
	adminGroup := r.Group("/api/admin")
	adminGroup.Use(adminHandler.AdminAuthRequired())
	{
		// 저장소 통계
		adminGroup.GET("/storage/stats", adminHandler.GetStorageStats)
		adminGroup.GET("/storage/backup-info", adminHandler.GetBackupInfo)
		
		// 데이터 정리
		adminGroup.DELETE("/cleanup/old-snapshots", adminHandler.CleanupOldSnapshots)
		adminGroup.DELETE("/cleanup/date-range", adminHandler.DeleteDataByDateRange)
		adminGroup.DELETE("/cleanup/competition/:id", adminHandler.DeleteCompetitionHistory)
		
		// 수동 실행
		adminGroup.POST("/trigger/daily-aggregation", adminHandler.TriggerDailyAggregation)
		adminGroup.POST("/trigger/hourly-snapshots", adminHandler.TriggerHourlySnapshots)
		
		// 시스템 상태
		adminGroup.GET("/system/health", adminHandler.GetSystemHealth)
	}
}

// Cron 작업 시작
func startCronJobs() {
	log.Println("🕐 Cron 작업 스케줄러 시작 중...")

	// StatsService 초기화
	statsService, err := services.NewStatsService()
	if err != nil {
		log.Printf("❌ Cron용 StatsService 초기화 실패: %v", err)
		return
	}

	// 크론 스케줄러 생성
	c := cron.New(cron.WithSeconds())

	// 매시간 통계 업데이트
	schedule := "0 0 * * * *" // 매시간 정각
	if config.Config != nil {
		if s, exists := config.GetCronSchedule("hourly_stats"); exists {
			schedule = s
		}
	}
	
	_, err = c.AddFunc(schedule, func() {
		log.Println("⏰ [매시간] 활성 대회 통계 업데이트 시작")
		if err := statsService.UpdateAllActiveCompetitions(); err != nil {
			log.Printf("❌ 활성 대회 통계 업데이트 실패: %v", err)
		} else {
			log.Println("✅ [매시간] 활성 대회 통계 업데이트 완료")
		}
	})
	if err != nil {
		log.Printf("❌ 매시간 크론잡 등록 실패: %v", err)
		return
	}
	log.Printf("📅 매시간 통계 업데이트 스케줄 등록: %s", schedule)

	// 일별 시스템 통계 업데이트
	dailySchedule := "0 0 2 * * *" // 매일 새벽 2시
	if config.Config != nil {
		if s, exists := config.GetCronSchedule("daily_stats"); exists {
			dailySchedule = s
		}
	}
	
	_, err = c.AddFunc(dailySchedule, func() {
		log.Println("⏰ [매일] 전체 시스템 통계 업데이트 시작")
		if err := statsService.SaveDailyAggregation(); err != nil {
			log.Printf("❌ 시스템 통계 업데이트 실패: %v", err)
		} else {
			log.Println("✅ [매일] 전체 시스템 통계 업데이트 완료")
		}
	})
	if err != nil {
		log.Printf("⚠️ 일별 크론잡 등록 실패: %v", err)
	} else {
		log.Printf("📅 일별 시스템 통계 스케줄 등록: %s", dailySchedule)
	}

	// 스케줄러 시작
	c.Start()
	log.Printf("✅ Cron 작업 스케줄러 실행 중 (%d개 작업)", len(c.Entries()))

	// 종료 신호 대기
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	
	log.Println("🛑 Cron 작업 스케줄러 종료 중...")
	c.Stop()
}
