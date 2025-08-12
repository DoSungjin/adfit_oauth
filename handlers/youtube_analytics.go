package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"google.golang.org/api/youtubeanalytics/v2"

	"adfit-oauth/models"
)

// GetVideoAnalytics - 영상의 상세 분석 데이터 가져오기
func (h *YouTubeHandler) GetVideoAnalytics(c *gin.Context) {
	videoID := c.Param("videoId")
	
	// Authorization 헤더에서 토큰 추출
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(401, gin.H{"error": "No authorization header"})
		return
	}
	
	sessionToken := strings.Replace(authHeader, "Bearer ", "", 1)
	
	// 세션 토큰으로 사용자 확인
	var userToken models.UserToken
	if err := h.DB.Where("access_token = ? AND platform = ?", sessionToken, "youtube").First(&userToken).Error; err != nil {
		// JWT 토큰일 수도 있으므로 user_id로 다시 검색
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(401, gin.H{"error": "Unauthorized - no valid session"})
			return
		}
		
		if err := h.DB.Where("user_id = ? AND platform = ?", userID, "youtube").First(&userToken).Error; err != nil {
			c.JSON(401, gin.H{"error": "YouTube not connected"})
			return
		}
	}
	
	// OAuth 토큰 복원
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		RefreshToken: userToken.RefreshToken,
		TokenType:    "Bearer",
		Expiry:       userToken.ExpiresAt,
	}
	
	// 토큰 만료 체크 및 갱신
	if token.Expiry.Before(time.Now()) && userToken.RefreshToken != "" {
		newToken, err := h.oauth2Config.TokenSource(context.Background(), token).Token()
		if err == nil {
			token = newToken
			// DB 업데이트
			userToken.AccessToken = newToken.AccessToken
			userToken.ExpiresAt = newToken.Expiry
			h.DB.Save(&userToken)
		}
	}
	
	// YouTube 서비스 초기화
	ctx := context.Background()
	client := h.oauth2Config.Client(ctx, token)
	
	// 먼저 YouTube Data API로 기본 정보 가져오기
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to create YouTube service"})
		return
	}
	
	// 비디오 기본 정보
	videoResponse, err := youtubeService.Videos.List([]string{"snippet", "statistics", "contentDetails"}).
		Id(videoID).Do()
	
	if err != nil || len(videoResponse.Items) == 0 {
		c.JSON(404, gin.H{"error": "Video not found"})
		return
	}
	
	video := videoResponse.Items[0]
	
	// YouTube Analytics 서비스 초기화
	analyticsService, err := youtubeanalytics.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		// Analytics 실패해도 기본 데이터는 반환
		c.JSON(200, gin.H{
			"videoId": videoID,
			"basic": gin.H{
				"title":       video.Snippet.Title,
				"viewCount":   video.Statistics.ViewCount,
				"likeCount":   video.Statistics.LikeCount,
				"commentCount": video.Statistics.CommentCount,
			},
			"analytics": gin.H{
				"available": false,
				"message": "Analytics API not available",
			},
		})
		return
	}
	
	// 날짜 범위 설정 (최근 30일)
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	
	// Analytics 데이터 수집
	result := gin.H{
		"videoId": videoID,
		"basic": gin.H{
			"title":        video.Snippet.Title,
			"description":  video.Snippet.Description,
			"publishedAt":  video.Snippet.PublishedAt,
			"duration":     video.ContentDetails.Duration,
			"viewCount":    video.Statistics.ViewCount,
			"likeCount":    video.Statistics.LikeCount,
			"commentCount": video.Statistics.CommentCount,
		},
		"period": gin.H{
			"startDate": startDate,
			"endDate":   endDate,
		},
	}
	
	// 1. 인구통계 데이터 (성별, 연령대)
	demographicsData := gin.H{
		"gender": gin.H{},
		"ageGroup": gin.H{},
	}
	
	// 성별 데이터
	genderReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("viewerPercentage").
		Dimensions("gender").
		Filters(fmt.Sprintf("video==%s", videoID)).
		StartDate(startDate).
		EndDate(endDate).
		Do()
	
	if err == nil && genderReport.Rows != nil {
		for _, row := range genderReport.Rows {
			if len(row) >= 2 {
				gender := fmt.Sprintf("%v", row[0])
				percentage := row[1].(float64)
				demographicsData["gender"].(gin.H)[gender] = percentage
			}
		}
	}
	
	// 연령대 데이터
	ageReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("viewerPercentage").
		Dimensions("ageGroup").
		Filters(fmt.Sprintf("video==%s", videoID)).
		StartDate(startDate).
		EndDate(endDate).
		Do()
	
	if err == nil && ageReport.Rows != nil {
		for _, row := range ageReport.Rows {
			if len(row) >= 2 {
				ageGroup := fmt.Sprintf("%v", row[0])
				percentage := row[1].(float64)
				demographicsData["ageGroup"].(gin.H)[ageGroup] = percentage
			}
		}
	}
	
	result["demographics"] = demographicsData
	
	// 2. 지역별 데이터
	geographyData := []gin.H{}
	geoReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("views,estimatedMinutesWatched").
		Dimensions("country").
		Filters(fmt.Sprintf("video==%s", videoID)).
		StartDate(startDate).
		EndDate(endDate).
		Sort("-views").
		MaxResults(10).
		Do()
	
	if err == nil && geoReport.Rows != nil {
		for _, row := range geoReport.Rows {
			if len(row) >= 3 {
				geographyData = append(geographyData, gin.H{
					"country": fmt.Sprintf("%v", row[0]),
					"views": row[1].(float64),
					"minutesWatched": row[2].(float64),
				})
			}
		}
	}
	
	result["geography"] = geographyData
	
	// 3. 시청 지속 시간
	retentionData := gin.H{}
	retentionReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("averageViewDuration,averageViewPercentage").
		Filters(fmt.Sprintf("video==%s", videoID)).
		StartDate(startDate).
		EndDate(endDate).
		Do()
	
	if err == nil && retentionReport.Rows != nil && len(retentionReport.Rows) > 0 {
		row := retentionReport.Rows[0]
		if len(row) >= 2 {
			retentionData["averageViewDuration"] = row[0].(float64)
			retentionData["averageViewPercentage"] = row[1].(float64)
		}
	}
	
	result["retention"] = retentionData
	
	// 4. 트래픽 소스
	trafficData := []gin.H{}
	trafficReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("views").
		Dimensions("insightTrafficSourceType").
		Filters(fmt.Sprintf("video==%s", videoID)).
		StartDate(startDate).
		EndDate(endDate).
		Sort("-views").
		Do()
	
	if err == nil && trafficReport.Rows != nil {
		for _, row := range trafficReport.Rows {
			if len(row) >= 2 {
				trafficData = append(trafficData, gin.H{
					"source": fmt.Sprintf("%v", row[0]),
					"views": row[1].(float64),
				})
			}
		}
	}
	
	result["trafficSources"] = trafficData
	
	// 5. 기기별 통계
	deviceData := []gin.H{}
	deviceReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("views").
		Dimensions("deviceType").
		Filters(fmt.Sprintf("video==%s", videoID)).
		StartDate(startDate).
		EndDate(endDate).
		Sort("-views").
		Do()
	
	if err == nil && deviceReport.Rows != nil {
		for _, row := range deviceReport.Rows {
			if len(row) >= 2 {
				deviceData = append(deviceData, gin.H{
					"device": fmt.Sprintf("%v", row[0]),
					"views": row[1].(float64),
				})
			}
		}
	}
	
	result["devices"] = deviceData
	
	// Analytics 사용 가능 여부 표시
	result["analytics"] = gin.H{
		"available": true,
		"lastUpdated": time.Now().Format(time.RFC3339),
	}
	
	c.JSON(200, result)
}
