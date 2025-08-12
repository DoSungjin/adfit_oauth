package main

import (
    "log"
    "os"
    
    "github.com/gin-contrib/cors"
    "github.com/gin-gonic/gin"
    "github.com/joho/godotenv"
    "github.com/glebarez/sqlite"  // CGO 없이 작동하는 SQLite 드라이버
    "gorm.io/gorm"
    
    "adfit-oauth/config"
    "adfit-oauth/handlers"
    "adfit-oauth/middleware"
    "adfit-oauth/models"
)

func main() {
    // 환경 변수 로드
    if err := godotenv.Load(); err != nil {
        log.Println("No .env file found")
    }
    
    // OAuth2 설정 초기화
    config.InitOAuth2()
    
    // 데이터베이스 초기화 (CGO 없는 SQLite 드라이버 사용)
    db, err := gorm.Open(sqlite.Open("adfit.db"), &gorm.Config{})
    if err != nil {
        log.Fatal("Failed to connect to database:", err)
    }
    
    // 테이블 자동 생성
    if err := db.AutoMigrate(&models.UserToken{}); err != nil {
        log.Fatal("Failed to migrate database:", err)
    }
    
    // Gin 설정
    r := gin.Default()
    
    // CORS 설정
    r.Use(cors.New(cors.Config{
        AllowOrigins:     []string{"*"},
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
        ExposeHeaders:    []string{"Content-Length"},
        AllowCredentials: false,  // false로 변경
    }))
    
    // Handler 초기화
    tiktokHandler := &handlers.TikTokHandler{DB: db}
    youtubeHandler := handlers.NewYouTubeHandler(db)
    
    // 공개 라우트
    public := r.Group("/api/tiktok")
    {
        public.GET("/auth", tiktokHandler.GetAuthURL)           // 로그인 URL 생성
        public.GET("/callback", tiktokHandler.HandleCallback)    // OAuth 콜백 (웹용)
        public.POST("/token", tiktokHandler.ExchangeToken)      // 토큰 교환
    }
    
    // TikTok 인증 필요 라우트
    protected := r.Group("/api/tiktok")
    protected.Use(middleware.AuthRequired())
    {
        protected.GET("/user", tiktokHandler.GetUserInfo)       // 사용자 정보
        protected.GET("/videos", tiktokHandler.GetVideos)       // 비디오 목록
        protected.POST("/refresh", tiktokHandler.RefreshToken)  // 토큰 갱신
        protected.POST("/logout", tiktokHandler.Logout)         // 로그아웃
    }
    
    // YouTube 공개 라우트
    youtubePublic := r.Group("/api/youtube")
    {
        youtubePublic.GET("/auth", youtubeHandler.GetAuthURL)           // 로그인 URL 생성
        youtubePublic.GET("/callback", youtubeHandler.HandleCallback)   // OAuth 콜백
        youtubePublic.POST("/token", youtubeHandler.ExchangeToken)      // 토큰 교환
    }
    
    // YouTube 인증 필요 라우트
    youtubeProtected := r.Group("/api/youtube")
    youtubeProtected.Use(middleware.AuthRequired())
    {
        youtubeProtected.GET("/user", youtubeHandler.GetUserInfo)       // 사용자 정보
        youtubeProtected.GET("/channel", youtubeHandler.GetChannelInfo) // 채널 정보 (간단한 버전)
        youtubeProtected.GET("/videos", youtubeHandler.GetVideos)       // 비디오 목록
        youtubeProtected.GET("/analytics/:videoId", youtubeHandler.GetVideoAnalytics) // 비디오 분석
        youtubeProtected.POST("/refresh", youtubeHandler.RefreshToken)  // 토큰 갱신
        youtubeProtected.POST("/logout", youtubeHandler.Logout)         // 로그아웃
    }
    
    // Health check
    r.GET("/health", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })
    
    // 서버 시작
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
    
    log.Printf("Server starting on port %s", port)
    if err := r.Run(":" + port); err != nil {
        log.Fatal("Failed to start server:", err)
    }
}
