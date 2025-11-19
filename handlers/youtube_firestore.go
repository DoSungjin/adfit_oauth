package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	firebase "firebase.google.com/go/v4"

	"adfit-oauth/config"
)

type YouTubeHandlerFirestore struct {
	firestore    *firestore.Client
	oauth2Config *oauth2.Config
}

// NewYouTubeHandlerFirestore creates a new YouTube handler with Firestore
func NewYouTubeHandlerFirestore() (*YouTubeHandlerFirestore, error) {
	ctx := context.Background()

	// Firebase 앱 초기화
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
		// 기본값
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID: "posted-app-c4ff5",
		})
	}

	if err != nil {
		return nil, fmt.Errorf("firebase 초기화 실패: %v", err)
	}

	// Firestore 클라이언트 생성
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore 초기화 실패: %v", err)
	}

	// OAuth2 설정
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

	return &YouTubeHandlerFirestore{
		firestore: firestoreClient,
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
	}, nil
}

type YouTubeToken struct {
	AccessToken  string    `firestore:"accessToken"`
	RefreshToken string    `firestore:"refreshToken"`
	ExpiresAt    time.Time `firestore:"expiresAt"`
	ChannelID    string    `firestore:"channelId,omitempty"`
	ChannelName  string    `firestore:"channelName,omitempty"`
	UpdatedAt    time.Time `firestore:"updatedAt"`
}

// GetAuthURL generates YouTube OAuth authorization URL
func (h *YouTubeHandlerFirestore) GetAuthURL(c *gin.Context) {
	state := c.Query("state")
	if state == "" {
		state = "default_state"
	}

	authURL := h.oauth2Config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("response_type", "code"),
	)

	fmt.Printf("🔵 YouTube Client ID: %s\n", h.oauth2Config.ClientID)
	fmt.Printf("🔵 Redirect URI: %s\n", h.oauth2Config.RedirectURL)
	fmt.Printf("🔵 Redirecting to YouTube Auth URL: %s\n", authURL)

	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// HandleCallback handles OAuth callback from YouTube
func (h *YouTubeHandlerFirestore) HandleCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	fmt.Printf("🔵 YouTube Callback received - Code: %s, State: %s, Error: %s\n", code, state, errorParam)

	baseURL := "https://adtown.ai"
	if os.Getenv("ENV") == "development" {
		baseURL = "http://localhost:9000"
	}

	if errorParam != "" {
		finalURL := fmt.Sprintf("%s/youtube/callback?error=%s&state=%s", baseURL, errorParam, state)
		c.Redirect(http.StatusTemporaryRedirect, finalURL)
		return
	}

	// State 파싱
	var stateData OAuthState
	if err := json.Unmarshal([]byte(state), &stateData); err != nil {
		stateData.UserID = state
		fmt.Printf("⚠️ State 파싱 실패, UserID로 사용: %s\n", state)
	}

	// Token 교환
	ctx := context.Background()
	token, err := h.oauth2Config.Exchange(ctx, code)
	if err != nil {
		fmt.Printf("❌ Token exchange error: %v\n", err)
		finalURL := fmt.Sprintf("%s/youtube/callback?error=token_exchange_failed&state=%s", baseURL, state)
		c.Redirect(http.StatusTemporaryRedirect, finalURL)
		return
	}

	fmt.Printf("✅ YouTube Token received for user: %s\n", stateData.UserID)

	// 채널 정보 조회
	channelInfo, err := h.fetchChannelInfo(ctx, token)
	if err != nil {
		fmt.Printf("⚠️ Failed to get channel info: %v\n", err)
	}

	// Token 저장
	tokenData := YouTubeToken{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
		UpdatedAt:    time.Now(),
	}

	if channelInfo != nil {
		tokenData.ChannelID = channelInfo["id"].(string)
		if snippet, ok := channelInfo["snippet"].(map[string]interface{}); ok {
			tokenData.ChannelName = snippet["title"].(string)
		}
	}

	// Firestore 저장
	_, err = h.firestore.Collection("users").Doc(stateData.UserID).
		Collection("connections").Doc("youtube").Set(ctx, tokenData)

	if err != nil {
		fmt.Printf("❌ Firestore 저장 실패: %v\n", err)
		finalURL := fmt.Sprintf("%s/youtube/callback?error=save_failed&state=%s", baseURL, state)
		c.Redirect(http.StatusTemporaryRedirect, finalURL)
		return
	}

	fmt.Printf("✅ Token saved to Firestore for user: %s\n", stateData.UserID)

	// URL 파라미터 구성
	params := map[string]string{
		"status": "success",
	}

	if stateData.FirebaseToken != "" {
		params["firebase_token"] = stateData.FirebaseToken
	}

	if channelInfo != nil {
		channelJSON, _ := json.Marshal(channelInfo)
		params["channel_info"] = string(channelJSON)
	}

	// 최종 URL 생성
	finalURL := baseURL + "/youtube/callback?"
	first := true
	for key, value := range params {
		if !first {
			finalURL += "&"
		}
		finalURL += fmt.Sprintf("%s=%s", key, value)
		first = false
	}

	fmt.Printf("✅ Redirecting to Flutter with session token\n")
	c.Redirect(http.StatusTemporaryRedirect, finalURL)
}

// fetchChannelInfo retrieves channel information
func (h *YouTubeHandlerFirestore) fetchChannelInfo(ctx context.Context, token *oauth2.Token) (map[string]interface{}, error) {
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

	return channelInfo, nil
}

// GetVideoInfo retrieves video information by video ID (public endpoint - no auth required)
func (h *YouTubeHandlerFirestore) GetVideoInfo(c *gin.Context) {
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
func (h *YouTubeHandlerFirestore) GetChannelInfo(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	ctx := context.Background()

	// Firestore에서 토큰 조회
	doc, err := h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Get(ctx)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	var tokenData YouTubeToken
	if err := doc.DataTo(&tokenData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse token"})
		return
	}

	// Token 복원
	token := &oauth2.Token{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		Expiry:       tokenData.ExpiresAt,
	}

	// Token 만료 체크 및 갱신
	if token.Expiry.Before(time.Now()) && tokenData.RefreshToken != "" {
		newToken, err := h.oauth2Config.TokenSource(ctx, token).Token()
		if err == nil {
			token = newToken
			// Firestore 업데이트
			_, err = h.firestore.Collection("users").Doc(userID).
				Collection("connections").Doc("youtube").Update(ctx, []firestore.Update{
				{Path: "accessToken", Value: newToken.AccessToken},
				{Path: "expiresAt", Value: newToken.Expiry},
				{Path: "updatedAt", Value: time.Now()},
			})
			if err != nil {
				fmt.Printf("⚠️ Token 업데이트 실패: %v\n", err)
			}
		}
	}

	// YouTube 서비스 생성
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

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

	c.JSON(http.StatusOK, gin.H{"channel": channelInfo})
}

// GetVideos retrieves user's videos
func (h *YouTubeHandlerFirestore) GetVideos(c *gin.Context) {
	userID := c.GetString("user_id")
	pageToken := c.Query("page_token")

	ctx := context.Background()

	// Firestore에서 토큰 조회
	doc, err := h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Get(ctx)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	var tokenData YouTubeToken
	if err := doc.DataTo(&tokenData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse token"})
		return
	}

	token := &oauth2.Token{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		Expiry:       tokenData.ExpiresAt,
	}

	// YouTube 서비스 생성
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
func (h *YouTubeHandlerFirestore) RefreshToken(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	// Firestore에서 토큰 조회
	doc, err := h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Get(ctx)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	var tokenData YouTubeToken
	if err := doc.DataTo(&tokenData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse token"})
		return
	}

	// Token 갱신
	token := &oauth2.Token{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		Expiry:       tokenData.ExpiresAt,
	}

	tokenSource := h.oauth2Config.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh token"})
		return
	}

	// Firestore 업데이트
	_, err = h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Update(ctx, []firestore.Update{
		{Path: "accessToken", Value: newToken.AccessToken},
		{Path: "refreshToken", Value: newToken.RefreshToken},
		{Path: "expiresAt", Value: newToken.Expiry},
		{Path: "updatedAt", Value: time.Now()},
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token refreshed successfully",
	})
}

// Logout deletes user's token
func (h *YouTubeHandlerFirestore) Logout(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	// Firestore에서 토큰 삭제
	_, err := h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Delete(ctx)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to logout"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Successfully logged out from YouTube",
	})
}

// GetUserInfo retrieves user's YouTube channel info
func (h *YouTubeHandlerFirestore) GetUserInfo(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	// Firestore에서 토큰 조회
	doc, err := h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Get(ctx)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "YouTube not connected"})
		return
	}

	var tokenData YouTubeToken
	if err := doc.DataTo(&tokenData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse token"})
		return
	}

	token := &oauth2.Token{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		Expiry:       tokenData.ExpiresAt,
	}

	// YouTube 서비스 생성
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 채널 정보 조회
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

// VerifyAndSaveAnalytics는 youtube_analytics.go에서 구현됨 (상세 Analytics 포함)

// ExchangeToken exchanges authorization code for access token
func (h *YouTubeHandlerFirestore) ExchangeToken(c *gin.Context) {
	var req struct {
		Code   string `json:"code" binding:"required"`
		UserID string `json:"user_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	token, err := h.oauth2Config.Exchange(ctx, req.Code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to exchange token"})
		return
	}

	// 채널 정보 조회
	channelInfo, _ := h.fetchChannelInfo(ctx, token)

	// Token 저장
	tokenData := YouTubeToken{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
		UpdatedAt:    time.Now(),
	}

	if channelInfo != nil {
		tokenData.ChannelID = channelInfo["id"].(string)
		if snippet, ok := channelInfo["snippet"].(map[string]interface{}); ok {
			tokenData.ChannelName = snippet["title"].(string)
		}
	}

	_, err = h.firestore.Collection("users").Doc(req.UserID).
		Collection("connections").Doc("youtube").Set(ctx, tokenData)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"channel_info": channelInfo,
	})
}
