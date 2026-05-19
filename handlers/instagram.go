package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"adfit-oauth/models"
	"adfit-oauth/services"
)

type InstagramHandler struct {
	DB        *gorm.DB
	Firestore *firestore.Client
}

// ==================== 1. OAuth URL 생성 ====================

func (h *InstagramHandler) GetAuthURL(c *gin.Context) {
	state := c.Query("state")
	userID := c.Query("user_id")

	if state == "" {
		state = "default_state"
	}

	if userID != "" {
		state = fmt.Sprintf("%s_%s", state, userID)
		fmt.Printf("✅ Instagram State with user_id: %s\n", state)
	}

	clientID := os.Getenv("INSTAGRAM_APP_ID")
	redirectURI := os.Getenv("INSTAGRAM_REDIRECT_URI")

	if redirectURI == "" {
		redirectURI = "https://adfit-server-520676604613.asia-northeast3.run.app/api/instagram/callback"
	}

	// Instagram API with Instagram Login - scope
	// instagram_business_basic: 사용자 정보, 미디어 목록
	// instagram_business_manage_insights: 조회수, 도달 등 인사이트
	scopes := "instagram_business_basic,instagram_business_manage_insights"

	authURL := fmt.Sprintf(
		"https://www.instagram.com/oauth/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		clientID,
		url.QueryEscape(redirectURI),
		url.QueryEscape(scopes),
		url.QueryEscape(state),
	)

	fmt.Printf("📸 Instagram OAuth URL: %s\n", authURL)
	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// ==================== 2. OAuth Callback ====================

func (h *InstagramHandler) HandleCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")
	errorReason := c.Query("error_reason")

	// ⭐ 직접 접속 시 안내 페이지
	if code == "" && state == "" && errorParam == "" {
		c.JSON(200, gin.H{
			"endpoint": "Instagram OAuth Callback",
			"status":   "ready",
		})
		return
	}

	fmt.Printf("📸 Instagram Callback - Code: %s, State: %s, Error: %s\n", code, state, errorParam)

	if errorParam != "" {
		redirectURL := fmt.Sprintf(
			"https://adtown.ai/instagram/callback?error=%s&error_reason=%s&state=%s",
			errorParam, errorReason, state,
		)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	userID := extractUserIDFromState(state)
	if userID == "" {
		fmt.Println("❌ Instagram: Failed to extract user_id from state")
		c.Redirect(http.StatusTemporaryRedirect, "https://adtown.ai/instagram/callback?error=invalid_state")
		return
	}

	// 1. Short-lived Token 교환
	shortToken, igUserID, err := h.exchangeCodeForToken(code)
	if err != nil {
		fmt.Printf("❌ Instagram Token exchange failed: %v\n", err)
		c.Redirect(http.StatusTemporaryRedirect, fmt.Sprintf(
			"https://adtown.ai/instagram/callback?error=token_exchange_failed&state=%s", state))
		return
	}

	// 2. Long-lived Token 교환 (60일 유효)
	longToken, expiresIn, err := h.exchangeForLongLivedToken(shortToken)
	if err != nil {
		fmt.Printf("⚠️ Instagram Long-lived token failed, using short token: %v\n", err)
		longToken = shortToken
		expiresIn = 3600 // 1시간
	}

	// 3. 사용자 정보 조회
	username, err := h.getUserInfo(longToken)
	if err != nil {
		fmt.Printf("⚠️ Instagram user info failed: %v\n", err)
		username = "instagram_user"
	}

	fmt.Printf("✅ Instagram 연동 성공 - User: %s (@%s)\n", igUserID, username)

	// 4. SQLite에 Token 저장
	userToken := models.UserToken{
		UserID:       userID,
		Platform:     "instagram",
		AccessToken:  longToken,
		RefreshToken: "", // Instagram은 refresh token 없음, long-lived token 갱신 필요
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
		Scope:        "instagram_business_basic,instagram_business_manage_insights",
		OpenID:       igUserID, // Instagram User ID 저장
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("user_id = ? AND platform = ?", userID, "instagram").Delete(&models.UserToken{}).Error; err != nil {
			fmt.Printf("⚠️ Instagram: Failed to delete existing token: %v\n", err)
		}
		return tx.Create(&userToken).Error
	})

	if err != nil {
		fmt.Printf("❌ Instagram DB save failed: %v\n", err)
		c.Redirect(http.StatusTemporaryRedirect, fmt.Sprintf(
			"https://adtown.ai/instagram/callback?error=db_save_failed&state=%s", state))
		return
	}

	// 5. JWT 생성
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":      userID,
		"ig_user_id":   igUserID,
		"platform":     "instagram",
		"exp":          time.Now().Add(time.Hour * 24 * 7).Unix(),
	})

	tokenString, err := jwtToken.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		fmt.Printf("❌ Instagram JWT generation failed: %v\n", err)
		c.Redirect(http.StatusTemporaryRedirect, fmt.Sprintf(
			"https://adtown.ai/instagram/callback?error=jwt_failed&state=%s", state))
		return
	}

	// 테스트 모드: JSON 응답 (localhost에서 test=1 있으면)
	if c.Query("test") == "1" {
		c.JSON(http.StatusOK, gin.H{
			"success":      true,
			"jwt":          tokenString,
			"username":     username,
			"ig_user_id":   igUserID,
			"access_token": longToken,
			"expires_in":   expiresIn,
		})
		return
	}

	// Flutter로 리다이렉트
	redirectURL := fmt.Sprintf(
		"https://adtown.ai/instagram/callback?jwt=%s&username=%s&state=%s",
		tokenString, url.QueryEscape(username), state,
	)

	fmt.Printf("✅ Instagram Redirecting to Flutter: %s\n", redirectURL)
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

// Short-lived Token 교환
func (h *InstagramHandler) exchangeCodeForToken(code string) (string, string, error) {
	clientID := os.Getenv("INSTAGRAM_APP_ID")
	clientSecret := os.Getenv("INSTAGRAM_APP_SECRET")
	redirectURI := os.Getenv("INSTAGRAM_REDIRECT_URI")

	if redirectURI == "" {
		redirectURI = "https://adfit-server-520676604613.asia-northeast3.run.app/api/instagram/callback"
	}

	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirectURI)
	data.Set("code", code)

	resp, err := http.Post(
		"https://api.instagram.com/oauth/access_token",
		"application/x-www-form-urlencoded",
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return "", "", fmt.Errorf("token request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("📸 Instagram Token Response: %s\n", string(body))

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		UserID      int64  `json:"user_id"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", "", fmt.Errorf("token parse failed: %v", err)
	}

	if tokenResp.AccessToken == "" {
		return "", "", fmt.Errorf("no access token in response: %s", string(body))
	}

	return tokenResp.AccessToken, fmt.Sprintf("%d", tokenResp.UserID), nil
}

// Long-lived Token 교환 (60일 유효)
func (h *InstagramHandler) exchangeForLongLivedToken(shortToken string) (string, int64, error) {
	clientSecret := os.Getenv("INSTAGRAM_APP_SECRET")

	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/access_token?grant_type=ig_exchange_token&client_secret=%s&access_token=%s",
		clientSecret,
		shortToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("📸 Instagram Long-lived Token Response: %s\n", string(body))

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"` // 60일 (초 단위)
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", 0, err
	}

	return tokenResp.AccessToken, tokenResp.ExpiresIn, nil
}

// ==================== 3. 사용자 정보 조회 ====================

func (h *InstagramHandler) getUserInfo(accessToken string) (string, error) {
	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/me?fields=id,username&access_token=%s",
		accessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var user struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}

	if err := json.Unmarshal(body, &user); err != nil {
		return "", err
	}

	return user.Username, nil
}

// GetUserInfo - Protected 엔드포인트 (JWT 인증 필요)
func (h *InstagramHandler) GetUserInfoProtected(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "instagram").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Instagram token not found"})
		return
	}

	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/me?fields=id,username,account_type,media_count&access_token=%s",
		userToken.AccessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// ==================== 4. 미디어 목록 조회 ====================

func (h *InstagramHandler) GetMedia(c *gin.Context) {
	userID := c.GetString("user_id")
	cursor := c.Query("cursor")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "instagram").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Instagram token not found"})
		return
	}

	// 필드: id, caption, media_type, permalink, timestamp, like_count, comments_count
	fields := "id,caption,media_type,media_url,permalink,thumbnail_url,timestamp,like_count,comments_count"

	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/me/media?fields=%s&access_token=%s&limit=20",
		fields,
		userToken.AccessToken,
	)

	if cursor != "" {
		reqURL += "&after=" + cursor
	}

	resp, err := http.Get(reqURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("📸 Instagram Media Response: %s\n", string(body))

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	// ⭐ 각 미디어에 대해 조회수 조회 추가 (타입별 다른 metric)
	if data, ok := result["data"].([]interface{}); ok {
		for i, item := range data {
			if media, ok := item.(map[string]interface{}); ok {
				mediaID, _ := media["id"].(string)
				mediaType := ""
				if mt, ok := media["media_type"].(string); ok {
					mediaType = mt
				}

				// 모든 미디어 타입에 대해 Insights 조회
				viewCount := h.getMediaPlays(mediaID, userToken.AccessToken, mediaType)
				media["view_count"] = viewCount
				data[i] = media
			}
		}
		result["data"] = data
	}

	c.JSON(http.StatusOK, result)
}

// getMediaPlays - 미디어의 조회수 조회
// mediaType: VIDEO, REELS, IMAGE, CAROUSEL_ALBUM
func (h *InstagramHandler) getMediaPlays(mediaID, accessToken string, mediaType string) int {
	// ⭐ 모든 미디어 타입에 대해 views 사용 (Instagram API 2024+)
	metrics := "views"

	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/insights?metric=%s&access_token=%s",
		mediaID,
		metrics,
		accessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		fmt.Printf("⚠️ Instagram plays fetch failed for %s: %v\n", mediaID, err)
		return 0
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data []struct {
			Name   string `json:"name"`
			Values []struct {
				Value int `json:"value"`
			} `json:"values"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("⚠️ Instagram plays parse failed for %s: %v\n", mediaID, err)
		return 0
	}

	// 에러 체크
	if result.Error.Code != 0 {
		fmt.Printf("⚠️ Instagram API error for %s: %s\n", mediaID, result.Error.Message)
		return 0
	}

	// 값 추출 (plays, video_views, impressions 중 하나)
	for _, metric := range result.Data {
		if len(metric.Values) > 0 {
			fmt.Printf("✅ Instagram %s for %s: %d\n", metric.Name, mediaID, metric.Values[0].Value)
			return metric.Values[0].Value
		}
	}

	return 0
}

// ==================== 5. 미디어 Insights ====================

// GetMediaInsights - 미디어 타입별 상세 인사이트 조회
// instagram_business_manage_insights 권한 필요
//
// 지원 metric 목록 (공식 문서 기준, 2024+):
//   FEED/REELS 공통: views, reach, likes, comments, saved, shares, total_interactions, profile_visits, follows
//   FEED 전용:       profile_activity(breakdown=action_type)
//   REELS 전용:      ig_reels_avg_watch_time, ig_reels_video_view_total_time
//   STORY 전용:      navigation(breakdown=story_navigation_action_type), replies
//
// ⚠️ 국가/나이/성별 breakdown은 개별 미디어 레벨에서 Instagram API가 제공하지 않음.
//    계정 전체 레벨(GET /<USER_ID>/insights)에서만 제공됨.
func (h *InstagramHandler) GetMediaInsights(c *gin.Context) {
	userID := c.GetString("user_id")
	mediaID := c.Param("mediaId")
	mediaType := c.DefaultQuery("media_type", "FEED") // FEED, REELS, STORY

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "instagram").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Instagram token not found"})
		return
	}

	insightsData := h.fetchDetailedMediaInsights(mediaID, userToken.AccessToken, mediaType)
	c.JSON(http.StatusOK, insightsData)
}

// fetchDetailedMediaInsights - 미디어 타입별 인사이트 조회 (내부 함수)
func (h *InstagramHandler) fetchDetailedMediaInsights(mediaID, accessToken, mediaType string) map[string]interface{} {
	result := map[string]interface{}{"mediaId": mediaID, "mediaType": mediaType}

	// 1️⃣ 공통 기본 metrics (FEED, REELS)
	if mediaType != "STORY" {
		metrics := "views,reach,likes,comments,saved,shares,total_interactions,profile_visits,follows"
		if data := h.callInsightsAPI(mediaID, accessToken, metrics, ""); data != nil {
			result["basic"] = data
		}
	}

	// 2️⃣ REELS 전용: 평균 시청시간, 총 재생시간
	if mediaType == "REELS" {
		reelsMetrics := "ig_reels_avg_watch_time,ig_reels_video_view_total_time"
		if data := h.callInsightsAPI(mediaID, accessToken, reelsMetrics, ""); data != nil {
			result["reels"] = data
		}
	}

	// 3️⃣ FEED 전용: 프로필 방문 후 행동 breakdown
	if mediaType == "FEED" {
		if data := h.callInsightsAPI(mediaID, accessToken, "profile_activity", "action_type"); data != nil {
			result["profileActivity"] = data
		}
	}

	// 4️⃣ STORY 전용: 도달, 공유, 답글, 내비게이션
	if mediaType == "STORY" {
		storyMetrics := "reach,shares,replies,total_interactions"
		if data := h.callInsightsAPI(mediaID, accessToken, storyMetrics, ""); data != nil {
			result["basic"] = data
		}
		if data := h.callInsightsAPI(mediaID, accessToken, "navigation", "story_navigation_action_type"); data != nil {
			result["navigation"] = data
		}
	}

	return result
}

// callInsightsAPI - Instagram Insights API 단일 호출
func (h *InstagramHandler) callInsightsAPI(mediaID, accessToken, metrics, breakdown string) []interface{} {
	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/v21.0/%s/insights?metric=%s&access_token=%s",
		mediaID, metrics, accessToken,
	)
	if breakdown != "" {
		reqURL += "&breakdown=" + breakdown
	}

	resp, err := http.Get(reqURL)
	if err != nil {
		fmt.Printf("⚠️ Instagram Insights API 오류 [%s/%s]: %v\n", mediaID, metrics, err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("📸 Instagram Insights [%s] metrics=%s: %s\n", mediaID, metrics, string(body))

	var result struct {
		Data  []interface{} `json:"data"`
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Error.Code != 0 {
		if result.Error.Code != 0 {
			fmt.Printf("⚠️ Instagram API error [%s]: %s\n", mediaID, result.Error.Message)
		}
		return nil
	}
	return result.Data
}

// ==================== 6. 테스트용 엔드포인트 ====================

// TestGetMedia - 직접 Token으로 미디어 조회 (PowerShell 테스트용)
func (h *InstagramHandler) TestGetMedia(c *gin.Context) {
	accessToken := c.Query("access_token")
	if accessToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "access_token required"})
		return
	}

	fields := "id,caption,media_type,permalink,timestamp,like_count,comments_count"
	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/me/media?fields=%s&access_token=%s&limit=10",
		fields,
		accessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("📸 [TEST] Instagram Media: %s\n", string(body))

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	c.JSON(http.StatusOK, result)
}

// TestGetInsights - 직접 Token으로 Insights 조회 (PowerShell 테스트용)
// ?access_token=xxx&media_type=FEED|REELS|STORY
func (h *InstagramHandler) TestGetInsights(c *gin.Context) {
	accessToken := c.Query("access_token")
	mediaID := c.Param("mediaId")
	mediaType := c.DefaultQuery("media_type", "FEED")

	if accessToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "access_token required"})
		return
	}

	result := h.fetchDetailedMediaInsights(mediaID, accessToken, mediaType)
	c.JSON(http.StatusOK, result)
}

// TestGetUser - 직접 Token으로 사용자 정보 조회
func (h *InstagramHandler) TestGetUser(c *gin.Context) {
	accessToken := c.Query("access_token")
	if accessToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "access_token required"})
		return
	}

	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/me?fields=id,username,account_type,media_count&access_token=%s",
		accessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("📸 [TEST] Instagram User: %s\n", string(body))

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	c.JSON(http.StatusOK, result)
}

// ==================== 7. 로그아웃 ====================

func (h *InstagramHandler) Logout(c *gin.Context) {
	userID := c.GetString("user_id")

	result := h.DB.Where("user_id = ? AND platform = ?", userID, "instagram").Delete(&models.UserToken{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to logout"})
		return
	}

	fmt.Printf("✅ Instagram Token deleted for user: %s\n", userID)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Instagram disconnected"})
}

// ==================== 8. Long-lived Token 갱신 ====================

// RefreshLongLivedToken - 60일 토큰 만료 전 갱신 (만료 1일 전부터 갱신 가능)
func (h *InstagramHandler) RefreshLongLivedToken(c *gin.Context) {
	userID := c.GetString("user_id")

	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "instagram").First(&userToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}

	reqURL := fmt.Sprintf(
		"https://graph.instagram.com/refresh_access_token?grant_type=ig_refresh_token&access_token=%s",
		userToken.AccessToken,
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Parse failed"})
		return
	}

	// DB 업데이트
	userToken.AccessToken = tokenResp.AccessToken
	userToken.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	h.DB.Save(&userToken)

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"expiresIn": tokenResp.ExpiresIn,
	})
}

// ==================== 10. Data Deletion Callback (Facebook 필수) ====================

// DataDeletion - Facebook 데이터 삭제 요청 처리
// Facebook 정책상 필수 엔드포인트
func (h *InstagramHandler) DataDeletion(c *gin.Context) {
	// Facebook이 POST로 signed_request 전송
	signedRequest := c.PostForm("signed_request")
	
	if signedRequest == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "signed_request required"})
		return
	}

	// signed_request 파싱 (base64.payload 형식)
	parts := strings.Split(signedRequest, ".")
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid signed_request"})
		return
	}

	// Payload 디코딩
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decode failed"})
		return
	}

	var data struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse failed"})
		return
	}

	fmt.Printf("📸 Instagram Data Deletion Request - User: %s\n", data.UserID)

	// SQLite에서 해당 사용자의 Instagram 토큰 삭제
	if h.DB != nil {
		h.DB.Where("open_id = ? AND platform = ?", data.UserID, "instagram").Delete(&models.UserToken{})
	}

	// 확인 코드 생성 (Facebook이 요구하는 형식)
	confirmationCode := fmt.Sprintf("adtown_del_%s_%d", data.UserID, time.Now().Unix())

	// Facebook이 요구하는 응답 형식
	c.JSON(http.StatusOK, gin.H{
		"url":               fmt.Sprintf("https://adtown.ai/deletion-status?code=%s", confirmationCode),
		"confirmation_code": confirmationCode,
	})
}

// DataDeletionStatus - 데이터 삭제 엔드포인트 상태 확인 (GET)
func (h *InstagramHandler) DataDeletionStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"endpoint":    "Instagram Data Deletion Callback",
		"status":      "ready",
		"description": "This endpoint handles user data deletion requests from Facebook as required by platform policy.",
		"method":      "POST (from Facebook)",
	})
}

// Deauthorize - Instagram 연동 해제 콜백
func (h *InstagramHandler) Deauthorize(c *gin.Context) {
	signedRequest := c.PostForm("signed_request")
	
	if signedRequest == "" {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	parts := strings.Split(signedRequest, ".")
	if len(parts) != 2 {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var data struct {
		UserID string `json:"user_id"`
	}
	json.Unmarshal(payload, &data)

	fmt.Printf("📸 Instagram Deauthorize - User: %s\n", data.UserID)

	// 토큰 삭제
	if h.DB != nil {
		h.DB.Where("open_id = ? AND platform = ?", data.UserID, "instagram").Delete(&models.UserToken{})
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeauthorizeStatus - 연동 해제 엔드포인트 상태 확인 (GET)
func (h *InstagramHandler) DeauthorizeStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"endpoint":    "Instagram Deauthorize Callback",
		"status":      "ready",
		"description": "This endpoint handles Instagram app deauthorization requests from Facebook.",
		"method":      "POST (from Facebook)",
	})
}

// ==================== 11. 영상 제출 (대회 참가) ====================

func (h *InstagramHandler) SubmitVideo(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var req struct {
		CompetitionID string `json:"competitionId" binding:"required"`
		MediaID       string `json:"mediaId" binding:"required"`
		MediaURL      string `json:"mediaUrl"`
		Permalink     string `json:"permalink" binding:"required"`
		MediaType     string `json:"mediaType"` // IMAGE, VIDEO, CAROUSEL_ALBUM
		Caption       string `json:"caption"`
		ThumbnailURL  string `json:"thumbnailUrl"`
		ViewCount     int    `json:"viewCount"`
		LikeCount     int    `json:"likeCount"`
		CommentsCount int    `json:"commentsCount"`
		CreatorName   string `json:"creatorName"`
		JWT           string `json:"jwt" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	fmt.Printf("📸 Instagram Video Submission - User: %s, Competition: %s, Media: %s\n",
		userID, req.CompetitionID, req.MediaID)

	// JWT 검증
	token, err := jwt.Parse(req.JWT, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(os.Getenv("JWT_SECRET")), nil
	})

	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid JWT"})
		return
	}

	// SQLite에서 Instagram Token 조회
	var userToken models.UserToken
	if err := h.DB.Where("user_id = ? AND platform = ?", userID, "instagram").First(&userToken).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Instagram token not found. Please reconnect."})
		return
	}

	// 멀티 DB 지원: 대회가 있는 DB 찾기
	ctx := context.Background()
	clients := services.GetFirestoreClients()
	competitionDB, isTestDB, err := clients.FindCompetitionDB(ctx, req.CompetitionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Competition not found"})
		return
	}

	if isTestDB {
		fmt.Printf("🧪 Using adtown-test DB for Instagram submission\n")
	}

	// ⭐ 성과형 대회 정보 조회
	compDoc, err := competitionDB.Collection("competitions").Doc(req.CompetitionID).Get(ctx)
	var estimatedEarnings int64 = 0
	if err == nil {
		data := compDoc.Data()
		if data["competitionType"] == "performance" {
			pricePerView := getInt64(data, "pricePerView")
			minViews := getInt64(data, "minViews")
			if int64(req.ViewCount) >= minViews {
				estimatedEarnings = int64(req.ViewCount) * pricePerView
			}
		}
	}

	// Firestore에 저장
	submissionRef := competitionDB.Collection("competitions").Doc(req.CompetitionID).
		Collection("submissions").NewDoc()

	submissionData := map[string]interface{}{
		"competitionId": req.CompetitionID,
		"creatorId":     userID,
		"creatorName":   req.CreatorName,
		"videoUrl":      req.Permalink, // Instagram permalink
		"videoTitle":    req.Caption,
		"thumbnailUrl":  req.ThumbnailURL,
		"viewCount":     req.ViewCount,
		"currentViewCount": req.ViewCount,
		"estimatedEarnings": estimatedEarnings, // ⭐ 성과형
		"likeCount":     req.LikeCount,
		"commentCount":  req.CommentsCount,
		"platform":      "instagram",
		"platforms":     []string{"instagram"},
		"instagramData": map[string]interface{}{
			"mediaId":   req.MediaID,
			"mediaType": req.MediaType,
			"permalink": req.Permalink,
		},
		"instagramAuth": map[string]interface{}{
			"accessToken": userToken.AccessToken,
			"igUserId":    userToken.OpenID,
			"expiresAt":   userToken.ExpiresAt.Format(time.RFC3339),
			"savedAt":     time.Now().Format(time.RFC3339),
		},
		"submittedAt":  firestore.ServerTimestamp,
		"isWinner":     false,
		"hasAnalytics": false,
		"isDeleted":    "n",
	}

	// Batch 작업
	batch := competitionDB.Batch()
	batch.Set(submissionRef, submissionData)

	// participant 업데이트
	participantRef := competitionDB.Collection("competitions").Doc(req.CompetitionID).
		Collection("participants").Doc(userID)

	batch.Update(participantRef, []firestore.Update{
		{Path: "submissionCount", Value: firestore.Increment(1)},
		{Path: "totalViewCount", Value: firestore.Increment(req.ViewCount)},
		{Path: "lastSubmittedAt", Value: firestore.ServerTimestamp},
	})

	if _, err := batch.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit"})
		return
	}

	fmt.Printf("✅ Instagram submission saved - ID: %s\n", submissionRef.ID)

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"submissionId": submissionRef.ID,
	})
}

// getInt64 - 데이터에서 int64 추출
func getInt64(data map[string]interface{}, key string) int64 {
	switch v := data[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}
