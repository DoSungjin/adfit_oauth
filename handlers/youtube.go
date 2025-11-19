package handlers

import (
	"context"
	"encoding/json"
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

// NewYouTubeHandler creates a new YouTube handler
func NewYouTubeHandler(db *gorm.DB) *YouTubeHandler {
	clientID := os.Getenv("YOUTUBE_CLIENT_ID")
	clientSecret := os.Getenv("YOUTUBE_CLIENT_SECRET")
	redirectURI := os.Getenv("YOUTUBE_REDIRECT_URI")

	if clientID == "" {
		fmt.Println("⚠️ WARNING: YOUTUBE_CLIENT_ID not set in environment")
	}
	if clientSecret == "" {
		fmt.Println("⚠️ WARNING: YOUTUBE_CLIENT_SECRET not set in environment")
	}
	if redirectURI == "" {
		if os.Getenv("ENV") == "development" {
			redirectURI = "http://localhost:8080/api/youtube/callback"
		} else {
			redirectURI = "https://adfit-server-520676604613.asia-northeast3.run.app/api/youtube/callback"
		}
	}

	return &YouTubeHandler{
		DB: db,
		oauth2Config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  redirectURI,
			Scopes: []string{
				"https://www.googleapis.com/auth/youtube.readonly",
				"https://www.googleapis.com/auth/yt-analytics.readonly",
				"https://www.googleapis.com/auth/userinfo.profile",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
	}
}

type OAuthState struct {
	UserID        string `json:"userId"`
	FirebaseToken string `json:"firebaseToken,omitempty"`
	ReturnURL     string `json:"returnUrl,omitempty"`
}

// GetAuthURL generates YouTube OAuth authorization URL
func (h *YouTubeHandler) GetAuthURL(c *gin.Context) {
	state := c.Query("state")
	if state == "" {
		state = "default_state"
	}

	// OAuth URL 생성
	authURL := h.oauth2Config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("response_type", "code"),
	)

	// 로그 출력
	fmt.Printf("🔵 YouTube Client ID: %s\n", h.oauth2Config.ClientID)
	fmt.Printf("🔵 Redirect URI: %s\n", h.oauth2Config.RedirectURL)
	fmt.Printf("🔵 Scopes: %v\n", h.oauth2Config.Scopes)
	fmt.Printf("🔵 Redirecting to YouTube Auth URL: %s\n", authURL)

	// YouTube OAuth 페이지로 리다이렉트
	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// HandleCallback handles OAuth callback from YouTube
func (h *YouTubeHandler) HandleCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	// 로그 출력
	fmt.Printf("🔵 YouTube Callback received - Code: %s, State: %s, Error: %s\n", code, state, errorParam)

	// 기본 URL 설정
	baseURL := "https://adtown.ai"
	if os.Getenv("ENV") == "development" {
		baseURL = "http://localhost:9000"
	}

	// 에러 처리
	if errorParam != "" {
		finalURL := fmt.Sprintf("%s?error=%s&state=%s#/youtube/callback", baseURL, errorParam, state)
		fmt.Printf("🔵 Redirecting to: %s\n", finalURL)
		c.Redirect(http.StatusTemporaryRedirect, finalURL)
		return
	}

	// State 파싱
	var stateData OAuthState
	if err := json.Unmarshal([]byte(state), &stateData); err != nil {
		// 파싱 실패 시 state를 userId로 사용
		stateData.UserID = state
		fmt.Printf("⚠️ State 파싱 실패, UserID로 사용: %s\n", state)
	} else {
		fmt.Printf("✅ State 파싱 성공: UserID=%s, HasFirebaseToken=%v\n",
			stateData.UserID, stateData.FirebaseToken != "")
	}

	// Token 교환
	ctx := context.Background()
	token, err := h.oauth2Config.Exchange(ctx, code)
	if err != nil {
		fmt.Printf("❌ Token exchange error: %v\n", err)
		finalURL := fmt.Sprintf("%s?error=token_exchange_failed&state=%s#/youtube/callback", baseURL, state)
		c.Redirect(http.StatusTemporaryRedirect, finalURL)
		return
	}

	fmt.Printf("✅ YouTube Token received for user: %s\n", stateData.UserID)

	// 채널 정보 조회
	channelInfo, err := h.fetchChannelInfo(ctx, token)
	if err != nil {
		fmt.Printf("⚠️ Failed to get channel info: %v\n", err)
		// 채널 정보 없어도 계속 진행
	}

	// DB 저장
	userToken := &models.UserToken{
		UserID:       stateData.UserID,
		Platform:     "youtube",
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
		UpdatedAt:    time.Now(),
	}

	// 기존 토큰이 있으면 업데이트, 없으면 생성
	var existingToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", stateData.UserID, "youtube").First(&existingToken).Error; err == nil {
		h.DB.Model(&existingToken).Updates(userToken)
	} else {
		h.DB.Create(userToken)
	}

	// JWT 생성
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  stateData.UserID,
		"platform": "youtube",
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	})

	sessionToken, err := jwtToken.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		fmt.Printf("❌ Failed to create JWT: %v\n", err)
		finalURL := fmt.Sprintf("%s?error=jwt_creation_failed&state=%s#/youtube/callback", baseURL, state)
		c.Redirect(http.StatusTemporaryRedirect, finalURL)
		return
	}

	// URL 파라미터 구성
	params := map[string]string{
		"session_token": sessionToken,
	}

	// Firebase 토큰이 있으면 추가
	if stateData.FirebaseToken != "" {
		params["firebase_token"] = stateData.FirebaseToken
	}

	// 채널 정보가 있으면 추가
	if channelInfo != nil {
		channelJSON, _ := json.Marshal(channelInfo)
		params["channel_info"] = string(channelJSON)
	}

	// 최종 URL 생성
	finalURL := baseURL + "?"
	first := true
	for key, value := range params {
		if !first {
			finalURL += "&"
		}
		finalURL += fmt.Sprintf("%s=%s", key, value)
		first = false
	}
	finalURL += "#/youtube/callback"

	fmt.Printf("✅ Redirecting to Flutter with session token\n")
	fmt.Printf("🔵 Final URL: %s\n", finalURL)
	c.Redirect(http.StatusTemporaryRedirect, finalURL)
}

// fetchChannelInfo retrieves channel information
func (h *YouTubeHandler) fetchChannelInfo(ctx context.Context, token *oauth2.Token) (map[string]interface{}, error) {
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}

	channelsResponse, err := youtubeService.Channels.List([]string{"snippet", "statistics"}).Mine(true).Do()
	if err != nil || len(channelsResponse.Items) == 0 {
		return nil, err
	}

	channel := channelsResponse.Items[0]
	channelInfo := map[string]interface{}{
		"id": channel.Id,
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
	return channelInfo, nil
}

// ExchangeToken exchanges authorization code for access token
func (h *YouTubeHandler) ExchangeToken(c *gin.Context) {
	var req struct {
		Code   string `json:"code" binding:"required"`
		UserID string `json:"user_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Token 교환
	ctx := context.Background()
	token, err := h.oauth2Config.Exchange(ctx, req.Code)
	if err != nil {
		fmt.Printf("❌ Token exchange error: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to exchange token: " + err.Error()})
		return
	}

	fmt.Printf("✅ YouTube Token received for user: %s\n", req.UserID)

	// YouTube 서비스 생성
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 정보 조회
	channelsResponse, err := youtubeService.Channels.List([]string{"snippet", "statistics"}).Mine(true).Do()
	if err != nil {
		fmt.Printf("❌ Failed to get channel info: %v\n", err)
	}

	var channelInfo map[string]interface{}
	if channelsResponse != nil && len(channelsResponse.Items) > 0 {
		channel := channelsResponse.Items[0]
		channelInfo = map[string]interface{}{
			"id": channel.Id,
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

	// DB 저장
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

	// JWT 생성
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
		"session_token": tokenString,
		"access_token":  tokenString,
		"channel_info":  channelInfo,
		"expires_in":    86400,
	})
}

// GetUserInfo retrieves user's YouTube channel info
func (h *YouTubeHandler) GetUserInfo(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth Token 생성
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// YouTube 서비스 생성
	ctx := context.Background()
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 정보 조회
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
	c.JSON(http.StatusOK, gin.H{
		"channel":   channel,
		"connected": true,
	})
}

// GetVideos retrieves user's videos
func (h *YouTubeHandler) GetVideos(c *gin.Context) {
	userID := c.GetString("user_id")
	pageToken := c.Query("page_token")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth Token 생성
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// YouTube 서비스 생성
	ctx := context.Background()
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 ID 조회
	channelsResponse, err := youtubeService.Channels.List([]string{"id"}).Mine(true).Do()
	if err != nil || len(channelsResponse.Items) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get channel"})
		return
	}

	channelID := channelsResponse.Items[0].Id

	// 비디오 검색
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

// RefreshToken refreshes access token
func (h *YouTubeHandler) RefreshToken(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth Token 생성
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// Token 갱신
	ctx := context.Background()
	tokenSource := h.oauth2Config.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh token"})
		return
	}

	// DB 업데이트
	userToken.AccessToken = newToken.AccessToken
	if newToken.RefreshToken != "" {
		userToken.RefreshToken = newToken.RefreshToken
	}
	userToken.ExpiresAt = newToken.Expiry
	userToken.UpdatedAt = time.Now()

	h.DB.Save(&userToken)

	// JWT 생성
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

// GetVideoInfo retrieves video information by video ID (public endpoint - no auth required)
func (h *YouTubeHandler) GetVideoInfo(c *gin.Context) {
	videoID := c.Param("videoId")
	if videoID == "" {
		videoID = c.Query("videoId")
	}

	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "videoId is required"})
		return
	}

	// YouTube Data API v3를 사용하여 공개 정보 조회
	apiKey := os.Getenv("YOUTUBE_API_KEY")
	if apiKey == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "YOUTUBE_API_KEY not configured"})
		return
	}

	ctx := context.Background()
	youtubeService, err := youtube.NewService(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 영상 정보 조회
	videoResponse, err := youtubeService.Videos.List([]string{"snippet", "statistics", "contentDetails", "status"}).Id(videoID).Do()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get video info: " + err.Error()})
		return
	}

	if len(videoResponse.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Video not found"})
		return
	}

	video := videoResponse.Items[0]
	c.JSON(http.StatusOK, gin.H{
		"items": []interface{}{video},
	})
}

// GetChannelInfo retrieves channel information
func (h *YouTubeHandler) GetChannelInfo(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	// OAuth Token 생성
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.ExpiresAt,
	}

	// YouTube 서비스 생성
	ctx := context.Background()
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 정보 조회
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
		"id": channel.Id,
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

// Logout deletes user's token
func (h *YouTubeHandler) Logout(c *gin.Context) {
	userID := c.GetString("user_id")

	// 토큰 삭제
	h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").Delete(&models.UserToken{})

	c.JSON(http.StatusOK, gin.H{
		"message": "Successfully logged out from YouTube",
	})
}
