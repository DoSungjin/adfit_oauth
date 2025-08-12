package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"gorm.io/gorm"

	"adfit-oauth/models"
)

type YouTubeHandler struct {
	DB           *gorm.DB
	oauth2Config *oauth2.Config
}

// YouTube OAuth2 설정 초기화
func NewYouTubeHandler(db *gorm.DB) *YouTubeHandler {
	clientSecret := os.Getenv("YOUTUBE_CLIENT_SECRET")
	if clientSecret == "" {
		fmt.Println("⚠️ WARNING: YOUTUBE_CLIENT_SECRET not set in environment")
	}

	return &YouTubeHandler{
		DB: db,
		oauth2Config: &oauth2.Config{
			ClientID:     "520676604613-vfqmgvsi58jgrd1s80kbj3ja7rqihrtf.apps.googleusercontent.com",
			ClientSecret: clientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  "https://adfit-oauth-server-520676604613.asia-northeast3.run.app/api/youtube/callback",
			Scopes: []string{
				"https://www.googleapis.com/auth/youtube.readonly",
				"https://www.googleapis.com/auth/yt-analytics.readonly",
				"https://www.googleapis.com/auth/userinfo.profile",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
	}
}

// 1. 로그인 URL 생성 (직접 리다이렉트)
func (h *YouTubeHandler) GetAuthURL(c *gin.Context) {
	state := c.Query("state")
	if state == "" {
		state = "default_state"
	}

	// YouTube OAuth URL 생성
	authURL := h.oauth2Config.AuthCodeURL(state, oauth2.AccessTypeOffline)

	// 디버깅을 위한 로그
	fmt.Printf("🔑 YouTube Client ID: %s\n", h.oauth2Config.ClientID)
	fmt.Printf("🔗 Redirect URI: %s\n", h.oauth2Config.RedirectURL)
	fmt.Printf("📋 Scopes: %v\n", h.oauth2Config.Scopes)
	fmt.Printf("🌐 Redirecting to YouTube Auth URL: %s\n", authURL)

	// 직접 리다이렉트
	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// 2. OAuth 콜백 처리
func (h *YouTubeHandler) HandleCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	// 디버깅 로그
	fmt.Printf("🔔 YouTube Callback received - Code: %s, State: %s, Error: %s\n", code, state, errorParam)

	// Flutter 앱의 콜백 경로로 리다이렉트
	redirectURL := "https://posted-app-c4ff5.web.app/#/youtube/callback"

	if os.Getenv("ENV") == "production" {
		redirectURL = "https://adfit.ai/#/youtube/callback"
	}

	if errorParam != "" {
		redirectURL = fmt.Sprintf("%s?error=%s&state=%s", redirectURL, errorParam, state)
	} else {
		redirectURL = fmt.Sprintf("%s?code=%s&state=%s", redirectURL, code, state)
	}

	fmt.Printf("🔁 Redirecting to: %s\n", redirectURL)
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

// 3. 토큰 교환
func (h *YouTubeHandler) ExchangeToken(c *gin.Context) {
	var req struct {
		Code   string `json:"code" binding:"required"`
		UserID string `json:"user_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// YouTube OAuth 토큰 교환
	ctx := context.Background()
	token, err := h.oauth2Config.Exchange(ctx, req.Code)
	if err != nil {
		fmt.Printf("❌ Token exchange error: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to exchange token: " + err.Error()})
		return
	}

	fmt.Printf("✅ YouTube Token received for user: %s\n", req.UserID)

	// YouTube 서비스 초기화
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 정보 가져오기
	channelsResponse, err := youtubeService.Channels.List([]string{"snippet", "statistics"}).Mine(true).Do()
	if err != nil {
		fmt.Printf("❌ Failed to get channel info: %v\n", err)
		// 채널 정보를 가져오지 못해도 토큰은 반환
	}

	var channelInfo map[string]interface{}
	if channelsResponse != nil && len(channelsResponse.Items) > 0 {
		channel := channelsResponse.Items[0]
		channelInfo = map[string]interface{}{
			"id":          channel.Id,
			"snippet": map[string]interface{}{
				"title":       channel.Snippet.Title,
				"description": channel.Snippet.Description,
				"thumbnails": map[string]interface{}{
					"default": map[string]interface{}{
						"url": channel.Snippet.Thumbnails.Default.Url,
					},
				},
			},
			"statistics": map[string]interface{}{
				"subscriberCount": channel.Statistics.SubscriberCount,
				"videoCount":      channel.Statistics.VideoCount,
				"viewCount":       channel.Statistics.ViewCount,
			},
			"connected": true,
		}
		fmt.Printf("✅ Channel info retrieved: %s\n", channel.Snippet.Title)
	} else {
		fmt.Printf("⚠️ No channel found for user\n")
	}

	// 토큰 저장
	userToken := &models.UserToken{
		UserID:       req.UserID,
		Platform:     "youtube",
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
		UpdatedAt:    time.Now(),
	}

	// 기존 토큰이 있으면 업데이트, 없으면 생성
	var existingToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", req.UserID, "youtube").First(&existingToken).Error; err == nil {
		h.DB.Model(&existingToken).Updates(userToken)
	} else {
		h.DB.Create(userToken)
	}

	// JWT 토큰 생성
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  req.UserID,
		"platform": "youtube",
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	})

	tokenString, err := jwtToken.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create JWT"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_token": tokenString,  // Flutter 앱에서 session_token으로 받음
		"access_token":  tokenString,  // 호환성을 위해 둘 다 제공
		"channel_info":  channelInfo,
		"expires_in":    86400,
	})
}

// 4. 사용자 정보 조회
func (h *YouTubeHandler) GetUserInfo(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth2 토큰 복원
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// YouTube 서비스 초기화
	ctx := context.Background()
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 정보 가져오기
	channelsResponse, err := youtubeService.Channels.List([]string{"snippet", "statistics", "contentDetails"}).Mine(true).Do()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get channel info"})
		return
	}

	if len(channelsResponse.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No channel found"})
		return
	}

	channel := channelsResponse.Items[0]
	c.JSON(http.StatusOK, gin.H{
		"channel":   channel,
		"connected": true,
	})
}

// 5. 비디오 목록 조회
func (h *YouTubeHandler) GetVideos(c *gin.Context) {
	userID := c.GetString("user_id")
	pageToken := c.Query("page_token")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth2 토큰 복원
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// YouTube 서비스 초기화
	ctx := context.Background()
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 내 채널 ID 가져오기
	channelsResponse, err := youtubeService.Channels.List([]string{"id"}).Mine(true).Do()
	if err != nil || len(channelsResponse.Items) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get channel"})
		return
	}

	channelID := channelsResponse.Items[0].Id

	// 비디오 목록 조회
	searchCall := youtubeService.Search.List([]string{"id", "snippet"}).
		ChannelId(channelID).
		Type("video").
		Order("date").
		MaxResults(20)

	if pageToken != "" {
		searchCall = searchCall.PageToken(pageToken)
	}

	searchResponse, err := searchCall.Do()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get videos"})
		return
	}

	// 비디오 ID 수집
	var videoIDs []string
	for _, item := range searchResponse.Items {
		videoIDs = append(videoIDs, item.Id.VideoId)
	}

	// 비디오 상세 정보 조회
	if len(videoIDs) > 0 {
		videosResponse, err := youtubeService.Videos.List([]string{"snippet", "statistics", "contentDetails"}).
			Id(videoIDs...).Do()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get video details"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"videos":        videosResponse.Items,
			"nextPageToken": searchResponse.NextPageToken,
		})
	} else {
		c.JSON(http.StatusOK, gin.H{
			"videos":        []interface{}{},
			"nextPageToken": "",
		})
	}
}

// 6. 토큰 갱신
func (h *YouTubeHandler) RefreshToken(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth2 토큰 복원
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// 토큰 갱신
	ctx := context.Background()
	tokenSource := h.oauth2Config.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh token"})
		return
	}

	// 새 토큰 저장
	userToken.AccessToken = newToken.AccessToken
	if newToken.RefreshToken != "" {
		userToken.RefreshToken = newToken.RefreshToken
	}
	userToken.ExpiresAt = newToken.Expiry
	userToken.UpdatedAt = time.Now()

	h.DB.Save(&userToken)

	// 새 JWT 생성
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  userID,
		"platform": "youtube",
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	})

	tokenString, err := jwtToken.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create JWT"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": tokenString,
		"expires_in":   86400,
	})
}

// 7. 채널 정보 조회 (간단한 버전)
func (h *YouTubeHandler) GetChannelInfo(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth2 토큰 복원
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// YouTube 서비스 초기화
	ctx := context.Background()
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 정보 가져오기
	channelsResponse, err := youtubeService.Channels.List([]string{"snippet", "statistics"}).Mine(true).Do()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get channel info"})
		return
	}

	if len(channelsResponse.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No channel found"})
		return
	}

	channel := channelsResponse.Items[0]
	channelInfo := map[string]interface{}{
		"id":          channel.Id,
		"snippet": map[string]interface{}{
			"title":       channel.Snippet.Title,
			"description": channel.Snippet.Description,
			"thumbnails": map[string]interface{}{
				"default": map[string]interface{}{
					"url": channel.Snippet.Thumbnails.Default.Url,
				},
			},
		},
		"statistics": map[string]interface{}{
			"subscriberCount": channel.Statistics.SubscriberCount,
			"videoCount":      channel.Statistics.VideoCount,
			"viewCount":       channel.Statistics.ViewCount,
		},
		"connected": true,
	}

	c.JSON(http.StatusOK, gin.H{
		"channel": channelInfo,
	})
}

// 8. 로그아웃
func (h *YouTubeHandler) Logout(c *gin.Context) {
	userID := c.GetString("user_id")

	// YouTube 토큰 삭제
	h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").Delete(&models.UserToken{})

	c.JSON(http.StatusOK, gin.H{
		"message": "Successfully logged out from YouTube",
	})
}
