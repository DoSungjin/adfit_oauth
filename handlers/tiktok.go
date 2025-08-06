package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"adfit-oauth/models"
)

type TikTokHandler struct {
	DB *gorm.DB
}

// 1. 로그인 URL 생성 (직접 리다이렉트)
func (h *TikTokHandler) GetAuthURL(c *gin.Context) {
	state := c.Query("state")
	if state == "" {
		state = "default_state"
	}

	// TikTok은 client_key를 사용하므로 커스텀 URL 생성
	clientKey := os.Getenv("TIKTOK_CLIENT_KEY")
	redirectURI := os.Getenv("TIKTOK_REDIRECT_URI")
	
	// 환경 변수가 없으면 기본값 사용
	if redirectURI == "" {
		redirectURI = "https://adfit-oauth-server-520676604613.asia-northeast3.run.app/api/tiktok/callback"
	}
	
	// 기본 scope만 사용 (확장 scope는 나중에 추가)
	scopes := "user.info.basic"

	// URL 구성
	authURL := fmt.Sprintf(
		"https://www.tiktok.com/v2/auth/authorize?client_key=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		clientKey,
		url.QueryEscape(redirectURI),
		scopes,
		url.QueryEscape(state),
	)

	// 디버깅을 위한 로그 추가
	fmt.Printf("🔑 Client Key: %s\n", clientKey)
	fmt.Printf("🔗 Redirect URI: %s\n", redirectURI)
	fmt.Printf("📋 Scopes: %s\n", scopes)
	fmt.Printf("🌐 Redirecting to TikTok Auth URL: %s\n", authURL)

	// JSON 반환 대신 직접 리다이렉트
	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// 2. OAuth 콜백 처리 (웹 리다이렉트용)
func (h *TikTokHandler) HandleCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	// 디버깅 로그
	fmt.Printf("🔔 Callback received - Code: %s, State: %s, Error: %s\n", code, state, errorParam)

	// Flutter 앱의 콜백 경로로 리다이렉트 (Hash 라우팅 사용)
	redirectURL := "https://adfit.ai/#/auth/callback/tiktok"

	if errorParam != "" {
		redirectURL = fmt.Sprintf("%s?error=%s&state=%s", redirectURL, errorParam, state)
	} else {
		redirectURL = fmt.Sprintf("%s?code=%s&state=%s", redirectURL, code, state)
	}

	fmt.Printf("🔁 Redirecting to: %s\n", redirectURL)
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

// 3. 토큰 교환 (TikTok OAuth 2.0 v2 커스텀 구현)
func (h *TikTokHandler) ExchangeToken(c *gin.Context) {
	var req struct {
		Code   string `json:"code" binding:"required"`
		UserID string `json:"user_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// TikTok OAuth 2.0 v2 토큰 교환 - 수동 구현
	tokenURL := "https://open.tiktokapis.com/v2/oauth/token/"
	
	// 요청 바디 구성 (URL-encoded form)
	data := url.Values{}
	data.Set("client_key", os.Getenv("TIKTOK_CLIENT_KEY"))
	data.Set("client_secret", os.Getenv("TIKTOK_CLIENT_SECRET"))
	data.Set("code", req.Code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", os.Getenv("TIKTOK_REDIRECT_URI"))

	fmt.Printf("🔑 Token Exchange Request:\n")
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
	fmt.Printf("📡 Token Response: %s\n", string(body))

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

	// UPSERT 방식으로 토큰 저장 (있으면 업데이트, 없으면 생성)
	userToken := models.UserToken{
		UserID:       req.UserID,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		Scope:        tokenResp.Scope,
		OpenID:       tokenResp.OpenID,
	}

	// 트랜잭션으로 안전하게 처리
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		// 먼저 기존 토큰 삭제 (Soft Delete 무시하고 완전 삭제)
		if err := tx.Unscoped().Where("user_id = ?", req.UserID).Delete(&models.UserToken{}).Error; err != nil {
			fmt.Printf("⚠️ Failed to delete existing token (may not exist): %v\n", err)
		}
		
		// 새 토큰 생성
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

// 4. 사용자 정보 조회 (간단한 버전)
func (h *TikTokHandler) GetUserInfo(c *gin.Context) {
	userID := c.GetString("user_id")
	fmt.Printf("\n========== GetUserInfo START ==========\n")
	fmt.Printf("🔍 User ID: %s\n", userID)

	// DB에서 토큰 조회
	var userToken models.UserToken
	if err := h.DB.Where("user_id = ?", userID).First(&userToken).Error; err != nil {
		fmt.Printf("❌ Token not found for user: %s\n", userID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}

	fmt.Printf("🆗 Found token for user %s, OpenID: %s\n", userID, userToken.OpenID)
	fmt.Printf("📋 Token Scope: %s\n", userToken.Scope)

	// TikTok API v2 - 기본 필드만 요청
	// user.info.basic scope에서 사용 가능한 필드만 요청
	fields := "open_id,union_id,avatar_url,display_name"
	
	// API URL 생성
	apiURL := fmt.Sprintf("https://open.tiktokapis.com/v2/user/info/?fields=%s", url.QueryEscape(fields))
	
	// HTTP 클라이언트로 직접 요청
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		fmt.Printf("❌ Failed to create request: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// 헤더 설정 - Bearer 토큰
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", userToken.AccessToken))
	req.Header.Set("Content-Type", "application/json")

	fmt.Printf("📤 Request URL: %s\n", apiURL)
	fmt.Printf("📤 Authorization: Bearer %s...\n", userToken.AccessToken[:20])

	// 요청 보내기
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ Failed to fetch user info: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user info"})
		return
	}
	defer resp.Body.Close()

	fmt.Printf("📡 Response Status: %d\n", resp.StatusCode)

	// 응답 본문 읽기
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("❌ Failed to read response: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	fmt.Printf("📦 Raw Response Body:\n%s\n", string(body))

	// JSON 파싱
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("❌ Failed to parse JSON: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response"})
		return
	}

	// 에러 체크 - TikTok API는 error.code가 "ok"일 때도 에러 객체를 반환함
	if errorData, ok := result["error"]; ok {
		if errorMap, isMap := errorData.(map[string]interface{}); isMap {
			// code가 "ok"가 아닌 경우만 에러로 처리
			if code, hasCode := errorMap["code"]; hasCode && code != "ok" {
				fmt.Printf("❌ TikTok API Error: %v\n", errorData)
				c.JSON(http.StatusBadRequest, gin.H{"error": errorData})
				return
			}
		}
	}

	// 성공적인 응답 처리
	// TikTok API v2는 data.user 구조로 반환
	if data, ok := result["data"].(map[string]interface{}); ok {
		if user, ok := data["user"].(map[string]interface{}); ok {
			fmt.Println("\n📊 ===== TikTok User Data =====")
			fmt.Printf("  OpenID: %v\n", user["open_id"])
			fmt.Printf("  DisplayName: %v\n", user["display_name"])
			fmt.Printf("  AvatarURL: %v\n", user["avatar_url"])
			fmt.Printf("  UnionID: %v\n", user["union_id"])
			fmt.Println("================================\n")
			
			c.JSON(http.StatusOK, gin.H{"data": user})
			return
		}
	}

	// data가 없거나 비어있는 경우 - 기본 사용자 정보 반환
	fmt.Printf("⚠️ No user data in response, using basic info from token\n")
	
	// 토큰에서 기본 정보 추출
	basicUser := map[string]interface{}{
		"open_id": userToken.OpenID,
		"display_name": "TikTok User",
		"avatar_url": "",
		"union_id": "",
	}
	
	c.JSON(http.StatusOK, gin.H{"data": basicUser})
}

// 5. 비디오 목록 조회
func (h *TikTokHandler) GetVideos(c *gin.Context) {
	userID := c.GetString("user_id")
	cursor := c.Query("cursor")
	maxCount := c.DefaultQuery("max_count", "20")

	fmt.Printf("\n========== GetVideos START ==========\n")
	fmt.Printf("🔍 User ID: %s\n", userID)

	// DB에서 토큰 조회
	var userToken models.UserToken
	if err := h.DB.Where("user_id = ?", userID).First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}

	// 요청 바디 구성
	reqBody := map[string]interface{}{
		"max_count": maxCount,
	}
	if cursor != "" {
		reqBody["cursor"] = cursor
	}

	bodyBytes, _ := json.Marshal(reqBody)

	// API 호출
	apiURL := "https://open.tiktokapis.com/v2/video/list/"
	
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

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
	fmt.Printf("📦 Videos Response: %s\n", string(body))

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// 6. 토큰 갱신
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

	// DB 업데이트
	userToken.AccessToken = tokenResp.AccessToken
	userToken.RefreshToken = tokenResp.RefreshToken
	userToken.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	h.DB.Save(&userToken)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// 7. 로그아웃 (토큰 삭제)
func (h *TikTokHandler) Logout(c *gin.Context) {
	userID := c.GetString("user_id")

	// DB에서 토큰 삭제
	result := h.DB.Where("user_id = ?", userID).Delete(&models.UserToken{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to logout"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
