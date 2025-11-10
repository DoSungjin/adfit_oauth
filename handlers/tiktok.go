package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"adfit-oauth/models"
)

type TikTokHandler struct {
	DB *gorm.DB
}

// GetAuthURL generates TikTok OAuth authorization URL
func (h *TikTokHandler) GetAuthURL(c *gin.Context) {
	state := c.Query("state")
	if state == "" {
		state = "default_state"
	}

	// 환경변수에서 클라이언트 설정 가져오기
	clientKey := os.Getenv("TIKTOK_CLIENT_KEY")
	redirectURI := os.Getenv("TIKTOK_REDIRECT_URI")

	// 기본 redirect URI 설정
	if redirectURI == "" {
		redirectURI = "https://adfit-server-520676604613.asia-northeast3.run.app/api/tiktok/callback"
	}

	// 요청 스코프 설정
	scopes := "user.info.basic,video.list"

	// URL 구성
	authURL := fmt.Sprintf(
		"https://www.tiktok.com/v2/auth/authorize/?client_key=%s&redirect_uri=%s&scope=%s&state=%s&response_type=code",
		clientKey,
		url.QueryEscape(redirectURI),
		scopes,
		url.QueryEscape(state),
	)

	// 로그 출력
	fmt.Printf("🔵 Client Key: %s\n", clientKey)
	fmt.Printf("🔵 Redirect URI: %s\n", redirectURI)
	fmt.Printf("🔵 Scopes: %s\n", scopes)
	fmt.Printf("🔵 Redirecting to TikTok Auth URL: %s\n", authURL)

	// TikTok OAuth 페이지로 리다이렉트
	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// HandleCallback handles OAuth callback from TikTok
func (h *TikTokHandler) HandleCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	fmt.Printf("🔵 Callback received - Code: %s, State: %s, Error: %s\n", code, state, errorParam)

	// 에러 처리
	if errorParam != "" {
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=%s&state=%s",
			errorParam, state,
		)
		fmt.Printf("❌ OAuth Error, redirecting to: %s\n", redirectURL)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	// state에서 user_id 추출
	userID := extractUserIDFromState(state)
	if userID == "" {
		fmt.Println("❌ Failed to extract user_id from state")
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=invalid_state&state=%s",
			state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	fmt.Printf("✅ Extracted user_id: %s from state: %s\n", userID, state)

	// Token 교환
	tokenURL := "https://open.tiktokapis.com/v2/oauth/token/"
	data := url.Values{}
	data.Set("client_key", os.Getenv("TIKTOK_CLIENT_KEY"))
	data.Set("client_secret", os.Getenv("TIKTOK_CLIENT_SECRET"))
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", os.Getenv("TIKTOK_REDIRECT_URI"))

	fmt.Println("🔵 Exchanging code for tokens...")
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		fmt.Printf("❌ Token exchange failed: %v\n", err)
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=token_exchange_failed&state=%s",
			state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}
	defer resp.Body.Close()

	// 응답 파싱
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		OpenID       string `json:"open_id"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
		Error        struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			Description string `json:"description"`
		} `json:"error"`
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewBuffer(body))
	fmt.Printf("🔵 Token Response: %s\n", string(body))

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		fmt.Printf("❌ Token parse failed: %v\n", err)
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=token_parse_failed&state=%s",
			state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	// API 에러 확인
	if tokenResp.Error.Code != "" {
		errorMsg := fmt.Sprintf("TikTok API Error: %s - %s", tokenResp.Error.Code, tokenResp.Error.Message)
		fmt.Printf("❌ %s\n", errorMsg)
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=%s&state=%s",
			url.QueryEscape(tokenResp.Error.Code), state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	if tokenResp.AccessToken == "" {
		fmt.Println("❌ No access token in response")
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=no_access_token&state=%s",
			state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	fmt.Printf("✅ Token Exchange Success! OpenID: %s, Scope: %s\n", tokenResp.OpenID, tokenResp.Scope)

	userToken := models.UserToken{
		UserID:       userID,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		Scope:        tokenResp.Scope,
		OpenID:       tokenResp.OpenID,
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("user_id = ?", userID).Delete(&models.UserToken{}).Error; err != nil {
			fmt.Printf("⚠️ Failed to delete existing token: %v\n", err)
		}
		if err := tx.Create(&userToken).Error; err != nil {
			fmt.Printf("❌ Failed to create new token: %v\n", err)
			return err
		}
		fmt.Printf("✅ Token saved successfully for user: %s\n", userID)
		return nil
	})

	if err != nil {
		fmt.Printf("❌ DB save failed: %v\n", err)
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=db_save_failed&state=%s",
			state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	// JWT 생성
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"open_id": tokenResp.OpenID,
		"exp":     time.Now().Add(time.Hour * 24 * 7).Unix(),
	})

	tokenString, err := jwtToken.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		fmt.Printf("❌ JWT generation failed: %v\n", err)
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/tiktok/callback?error=jwt_generation_failed&state=%s",
			state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	// Flutter로 리다이렉트 (JWT 포함)
	redirectURL := fmt.Sprintf(
		"https://adtown.ai/tiktok/callback?jwt=%s&state=%s",
		tokenString, state,
	)

	fmt.Printf("✅ Redirecting to Flutter with JWT: %s\n", redirectURL)
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

// ExchangeToken exchanges authorization code for access token
func (h *TikTokHandler) ExchangeToken(c *gin.Context) {
	var req struct {
		Code   string `json:"code" binding:"required"`
		UserID string `json:"user_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Token 교환 API 호출
	tokenURL := "https://open.tiktokapis.com/v2/oauth/token/"

	// 요청 데이터 준비
	data := url.Values{}
	data.Set("client_key", os.Getenv("TIKTOK_CLIENT_KEY"))
	data.Set("client_secret", os.Getenv("TIKTOK_CLIENT_SECRET"))
	data.Set("code", req.Code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", os.Getenv("TIKTOK_REDIRECT_URI"))

	fmt.Printf("🔵 Token Exchange Request:\n")
	fmt.Printf("  URL: %s\n", tokenURL)
	fmt.Printf("  client_key: %s\n", os.Getenv("TIKTOK_CLIENT_KEY"))
	fmt.Printf("  redirect_uri: %s\n", os.Getenv("TIKTOK_REDIRECT_URI"))
	codePreview := req.Code
	if len(req.Code) > 20 {
		codePreview = req.Code[:20] + "..."
	}
	fmt.Printf("  code: %s\n", codePreview)

	// HTTP 요청
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to request token: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// 응답 파싱
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		OpenID       string `json:"open_id"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
		Error        struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			Description string `json:"description"`
		} `json:"error"`
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewBuffer(body))
	fmt.Printf("🔵 Token Response: %s\n", string(body))

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse token response: " + err.Error()})
		return
	}

	// 에러 체크
	if tokenResp.Error.Code != "" {
		errorMsg := fmt.Sprintf("TikTok API Error: %s - %s (%s)", tokenResp.Error.Code, tokenResp.Error.Message, tokenResp.Error.Description)
		fmt.Printf("❌ %s\n", errorMsg)
		c.JSON(http.StatusBadRequest, gin.H{"error": errorMsg})
		return
	}

	if tokenResp.AccessToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No access token received"})
		return
	}

	fmt.Printf("✅ Token Exchange Success! OpenID: %s, Scope: %s\n", tokenResp.OpenID, tokenResp.Scope)

	// DB 저장
	userToken := models.UserToken{
		UserID:       req.UserID,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		Scope:        tokenResp.Scope,
		OpenID:       tokenResp.OpenID,
	}

	// 트랜잭션으로 저장
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		// 기존 토큰 삭제
		if err := tx.Unscoped().Where("user_id = ?", req.UserID).Delete(&models.UserToken{}).Error; err != nil {
			fmt.Printf("⚠️ Failed to delete existing token (may not exist): %v\n", err)
		}

		// 새 토큰 저장
		if err := tx.Create(&userToken).Error; err != nil {
			fmt.Printf("❌ Failed to create new token: %v\n", err)
			return err
		}

		fmt.Printf("✅ Token saved successfully for user: %s\n", req.UserID)
		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save token: " + err.Error()})
		return
	}

	// JWT 생성
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": req.UserID,
		"open_id": tokenResp.OpenID,
		"exp":     time.Now().Add(time.Hour * 24 * 7).Unix(),
	})

	tokenString, err := jwtToken.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate JWT"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"jwt":     tokenString,
		"open_id": tokenResp.OpenID,
	})
}

// GetUserInfo retrieves user information from TikTok
func (h *TikTokHandler) GetUserInfo(c *gin.Context) {
	userID := c.GetString("user_id")
	fmt.Printf("\n========== GetUserInfo START ==========\n")
	fmt.Printf("🔵 User ID: %s\n", userID)

	// DB에서 토큰 조회
	var userToken models.UserToken
	if err := h.DB.Where("user_id = ?", userID).First(&userToken).Error; err != nil {
		fmt.Printf("❌ Token not found for user: %s\n", userID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}

	fmt.Printf("🔵 Found token for user %s, OpenID: %s\n", userID, userToken.OpenID)
	fmt.Printf("🔵 Token Scope: %s\n", userToken.Scope)

	// TikTok API URL (User Info)
	fields := "open_id,union_id,avatar_url,display_name"

	// API URL 구성
	apiURL := fmt.Sprintf("https://open.tiktokapis.com/v2/user/info/?fields=%s", fields)

	// HTTP Client 생성
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		fmt.Printf("❌ Failed to create request: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// 헤더 설정
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", userToken.AccessToken))
	req.Header.Set("Content-Type", "application/json")

	fmt.Printf("🔵 Request URL: %s\n", apiURL)
	fmt.Printf("🔵 Authorization: Bearer %s...\n", userToken.AccessToken[:20])

	// HTTP 요청 실행
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ Failed to fetch user info: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user info"})
		return
	}
	defer resp.Body.Close()

	fmt.Printf("🔵 Response Status: %d\n", resp.StatusCode)

	// 응답 읽기
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("❌ Failed to read response: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	fmt.Printf("🔵 Raw Response Body:\n%s\n", string(body))

	// JSON 파싱
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("❌ Failed to parse JSON: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response"})
		return
	}

	// TikTok API 에러 체크
	if errorData, ok := result["error"]; ok {
		if errorMap, isMap := errorData.(map[string]interface{}); isMap {
			// 에러 코드 확인
			if code, hasCode := errorMap["code"]; hasCode && code != "ok" {
				fmt.Printf("❌ TikTok API Error: %v\n", errorData)
				c.JSON(http.StatusBadRequest, gin.H{"error": errorData})
				return
			}
		}
	}

	// 사용자 데이터 추출
	// data.user에서 실제 사용자 정보 가져오기
	if data, ok := result["data"].(map[string]interface{}); ok {
		if user, ok := data["user"].(map[string]interface{}); ok {
			fmt.Println("\n🔵 ===== TikTok User Data =====")
			fmt.Printf("  OpenID: %v\n", user["open_id"])
			fmt.Printf("  DisplayName: %v\n", user["display_name"])
			fmt.Printf("  AvatarURL: %v\n", user["avatar_url"])
			fmt.Printf("  UnionID: %v\n", user["union_id"])
			fmt.Println("================================\n")

			c.JSON(http.StatusOK, gin.H{"data": user})
			return
		}
	}

	// 사용자 정보가 없으면 기본 정보 반환
	fmt.Printf("⚠️ No user data in response, using basic info from token\n")

	// 기본 사용자 정보 생성
	basicUser := map[string]interface{}{
		"open_id":      userToken.OpenID,
		"display_name": "TikTok User",
		"avatar_url":   "",
		"union_id":     "",
	}

	c.JSON(http.StatusOK, gin.H{"data": basicUser})
}

// GetVideos retrieves user's videos from TikTok
func (h *TikTokHandler) GetVideos(c *gin.Context) {
	userID := c.GetString("user_id")
	cursor := c.Query("cursor")

	fmt.Printf("\n========== GetVideos START ==========\n")
	fmt.Printf("🔵 User ID: %s\n", userID)

	// DB에서 토큰 조회
	var userToken models.UserToken
	if err := h.DB.Where("user_id = ?", userID).First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}

	// 요청할 필드들
	fields := []string{
		"id",
		"create_time",
		"cover_image_url",
		"share_url",
		"video_description",
		"duration",
		"height",
		"width",
		"title",
		"view_count",
		"like_count",
		"comment_count",
		"share_count",
	}

	// 요청 바디 구성
	reqBody := map[string]interface{}{
		"max_count": 20,
	}
	if cursor != "" {
		reqBody["cursor"], _ = strconv.ParseInt(cursor, 10, 64)
	}

	bodyBytes, _ := json.Marshal(reqBody)

	// API URL 구성
	apiURL := fmt.Sprintf("https://open.tiktokapis.com/v2/video/list/?fields=%s",
		url.QueryEscape(strings.Join(fields, ",")))

	fmt.Printf("🔵 Request URL: %s\n", apiURL)
	fmt.Printf("🔵 Request Body: %s\n", string(bodyBytes))

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// 헤더 설정
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", userToken.AccessToken))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch videos"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("🔵 Videos Response Status: %d\n", resp.StatusCode)
	fmt.Printf("🔵 Videos Response Body: %s\n", string(body))

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("❌ Failed to parse JSON: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response"})
		return
	}

	// 에러 체크
	if errorData, ok := result["error"].(map[string]interface{}); ok {
		if code, hasCode := errorData["code"]; hasCode && code != "ok" {
			fmt.Printf("❌ TikTok API Error: %v\n", errorData)
			c.JSON(http.StatusBadRequest, gin.H{"error": errorData})
			return
		}
	}

	// 비디오 데이터 추출
	if data, ok := result["data"].(map[string]interface{}); ok {
		if videos, ok := data["videos"].([]interface{}); ok {
			fmt.Printf("✅ Found %d videos\n", len(videos))

			if len(videos) > 0 {
				if firstVideo, ok := videos[0].(map[string]interface{}); ok {
					fmt.Printf("\n🔵 First Video Details:\n")
					fmt.Printf("  ID: %v\n", firstVideo["id"])
					fmt.Printf("  Title: %v\n", firstVideo["title"])
					fmt.Printf("  View Count: %v\n", firstVideo["view_count"])
					fmt.Printf("  Like Count: %v\n", firstVideo["like_count"])
					fmt.Printf("  Comment Count: %v\n", firstVideo["comment_count"])
					fmt.Printf("  Cover URL: %v\n", firstVideo["cover_image_url"])
					fmt.Printf("  Share URL: %v\n", firstVideo["share_url"])
					fmt.Printf("  Duration: %vs\n", firstVideo["duration"])
				}
			}
		}
	}

	c.JSON(http.StatusOK, result)
}

// RefreshToken refreshes access token using refresh token
func (h *TikTokHandler) RefreshToken(c *gin.Context) {
	userID := c.GetString("user_id")

	// DB에서 토큰 조회
	var userToken models.UserToken
	if err := h.DB.Where("user_id = ?", userID).First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}

	// TikTok refresh token endpoint
	tokenURL := "https://open.tiktokapis.com/v2/oauth/token/"

	data := url.Values{}
	data.Set("client_key", os.Getenv("TIKTOK_CLIENT_KEY"))
	data.Set("client_secret", os.Getenv("TIKTOK_CLIENT_SECRET"))
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", userToken.RefreshToken)

	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to refresh token"})
		return
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse response"})
		return
	}

	// 토큰 업데이트
	userToken.AccessToken = tokenResp.AccessToken
	userToken.RefreshToken = tokenResp.RefreshToken
	userToken.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	h.DB.Save(&userToken)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Logout deletes user's token
func (h *TikTokHandler) Logout(c *gin.Context) {
	userID := c.GetString("user_id")

	// 토큰 삭제
	result := h.DB.Where("user_id = ?", userID).Delete(&models.UserToken{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to logout"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// extractUserIDFromState extracts user ID from state parameter
func extractUserIDFromState(state string) string {
	// state가 비어있으면 빈 문자열 반환
	if state == "" {
		return ""
	}

	// 언더스코어로 구분된 경우 첫 부분만 추출
	parts := strings.Split(state, "_")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}

	// 그대로 반환
	return state
}
