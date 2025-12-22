package handlers

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"google.golang.org/api/youtubeanalytics/v2"
)

// VerifyAndSaveAnalytics - 비디오 소유권 확인 및 Analytics 저장
func (h *YouTubeHandlerFirestore) VerifyAndSaveAnalytics(c *gin.Context) {
	ctx := context.Background()
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(401, gin.H{"error": "Unauthorized"})
		return
	}

	// 요청 바디 파싱
	var reqBody struct {
		VideoID       string `json:"videoId"`
		CompetitionID string `json:"competitionId"`
		SubmissionID  string `json:"submissionId"`
	}

	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	fmt.Printf("\n🔍 비디오 연동 요청:\n")
	fmt.Printf("  - userID: %s\n", userID)
	fmt.Printf("  - videoID: %s\n", reqBody.VideoID)
	fmt.Printf("  - competitionID: %s\n", reqBody.CompetitionID)
	fmt.Printf("  - submissionID: %s\n\n", reqBody.SubmissionID)

	// 1. YouTube Token 조회
	doc, err := h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Get(ctx)

	if err != nil {
		fmt.Printf("❌ YouTube 토큰 조회 실패: %v\n", err)
		c.JSON(401, gin.H{"error": "YouTube not connected"})
		return
	}

	var tokenData YouTubeToken
	if err := doc.DataTo(&tokenData); err != nil {
		fmt.Printf("❌ 토큰 파싱 실패: %v\n", err)
		c.JSON(500, gin.H{"error": "Failed to parse token"})
		return
	}

	// 2. Token 갱신 (만료시)
	token := &oauth2.Token{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		TokenType:    "Bearer",
		Expiry:       tokenData.ExpiresAt,
	}

	if token.Expiry.Before(time.Now()) && tokenData.RefreshToken != "" {
		newToken, err := h.oauth2Config.TokenSource(context.Background(), token).Token()
		if err == nil {
			token = newToken
			_, _ = h.firestore.Collection("users").Doc(userID).
				Collection("connections").Doc("youtube").Update(ctx, []firestore.Update{
				{Path: "accessToken", Value: newToken.AccessToken},
				{Path: "expiresAt", Value: newToken.Expiry},
				{Path: "updatedAt", Value: time.Now()},
			})
		}
	}

	// 3. YouTube API 클라이언트 생성
	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fmt.Printf("❌ YouTube 서비스 생성 실패: %v\n", err)
		c.JSON(500, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	// 4. 비디오 정보 조회
	videoResponse, err := youtubeService.Videos.List([]string{"snippet", "statistics"}).
		Id(reqBody.VideoID).Do()

	if err != nil || len(videoResponse.Items) == 0 {
		fmt.Printf("❌ 비디오 조회 실패: %v\n", err)
		c.JSON(404, gin.H{"error": "Video not found"})
		return
	}

	video := videoResponse.Items[0]

	// 5. 채널 정보 조회 (소유권 확인)
	channelResponse, err := youtubeService.Channels.List([]string{"id"}).Mine(true).Do()
	if err != nil || len(channelResponse.Items) == 0 {
		fmt.Printf("❌ 채널 정보 조회 실패: %v\n", err)
		c.JSON(500, gin.H{"error": "Failed to get channel info"})
		return
	}

	myChannelID := channelResponse.Items[0].Id
	videoChannelID := video.Snippet.ChannelId

	fmt.Printf("\n🔍 소유권 확인:\n")
	fmt.Printf("  - 내 채널 ID: %s\n", myChannelID)
	fmt.Printf("  - 비디오 채널 ID: %s\n\n", videoChannelID)

	if myChannelID != videoChannelID {
		fmt.Printf("❌ 소유권 불일치!\n")
		c.JSON(403, gin.H{"error": "This video does not belong to your channel"})
		return
	}

	fmt.Printf("✅ 소유권 확인 완료!\n\n")

	// 6. Analytics API로 상세 통계 수집
	analyticsService, err := youtubeanalytics.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fmt.Printf("⚠️ Analytics API 초기화 실패: %v\n", err)
		// Analytics API 없어도 기본 통계는 저장
		analyticsData := h.createBasicAnalyticsData(video)
		if err := h.saveAnalyticsToSubmission(ctx, reqBody.CompetitionID, reqBody.SubmissionID, video, analyticsData); err != nil {
			fmt.Printf("❌ Firebase 저장 실패: %v\n", err)
			c.JSON(500, gin.H{"error": "Failed to save analytics"})
			return
		}
		c.JSON(200, gin.H{
			"success":   true,
			"message":   "비디오 연동 완료 (기본 통계)",
			"analytics": analyticsData,
		})
		return
	}

	// 7. 상세 Analytics 데이터 수집
	endDate := time.Now().Format("2025-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2025-01-02")

	analyticsData := h.createBasicAnalyticsData(video)
	analyticsData["period"] = gin.H{
		"startDate": startDate,
		"endDate":   endDate,
	}

	// Demographics (성별, 연령)
	demographicsData := gin.H{
		"gender":   gin.H{},
		"ageGroup": gin.H{},
	}

	// 성별 통계
	genderReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("viewerPercentage").
		Dimensions("gender").
		Filters(fmt.Sprintf("video==%s", reqBody.VideoID)).
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

	// 연령 통계
	ageReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("viewerPercentage").
		Dimensions("ageGroup").
		Filters(fmt.Sprintf("video==%s", reqBody.VideoID)).
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

	analyticsData["demographics"] = demographicsData

	// Geography (지역)
	var geographyData []gin.H
	geoReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("views,estimatedMinutesWatched").
		Dimensions("country").
		Filters(fmt.Sprintf("video==%s", reqBody.VideoID)).
		StartDate(startDate).
		EndDate(endDate).
		Sort("-views").
		MaxResults(10).
		Do()

	if err == nil && geoReport.Rows != nil {
		for _, row := range geoReport.Rows {
			if len(row) >= 3 {
				geographyData = append(geographyData, gin.H{
					"country":        fmt.Sprintf("%v", row[0]),
					"views":          row[1].(float64),
					"minutesWatched": row[2].(float64),
				})
			}
		}
	}

	analyticsData["geography"] = geographyData

	// Devices (시청 기기)
	var deviceData []gin.H
	deviceReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("views").
		Dimensions("deviceType").
		Filters(fmt.Sprintf("video==%s", reqBody.VideoID)).
		StartDate(startDate).
		EndDate(endDate).
		Sort("-views").
		Do()

	if err == nil && deviceReport.Rows != nil {
		for _, row := range deviceReport.Rows {
			if len(row) >= 2 {
				deviceData = append(deviceData, gin.H{
					"device": fmt.Sprintf("%v", row[0]),
					"views":  row[1].(float64),
				})
			}
		}
	}

	analyticsData["devices"] = deviceData

	// Traffic Sources (유입 경로)
	var trafficData []gin.H
	trafficReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("views").
		Dimensions("insightTrafficSourceType").
		Filters(fmt.Sprintf("video==%s", reqBody.VideoID)).
		StartDate(startDate).
		EndDate(endDate).
		Sort("-views").
		Do()

	if err == nil && trafficReport.Rows != nil {
		for _, row := range trafficReport.Rows {
			if len(row) >= 2 {
				trafficData = append(trafficData, gin.H{
					"source": fmt.Sprintf("%v", row[0]),
					"views":  row[1].(float64),
				})
			}
		}
	}

	analyticsData["trafficSources"] = trafficData

	// Retention (시청 지속 시간)
	retentionData := gin.H{}
	retentionReport, err := analyticsService.Reports.Query().
		Ids("channel==MINE").
		Metrics("averageViewDuration,averageViewPercentage").
		Filters(fmt.Sprintf("video==%s", reqBody.VideoID)).
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

	analyticsData["retention"] = retentionData

	// ⭐ 핵심: isVerified = true 설정
	analyticsData["isVerified"] = true
	analyticsData["lastUpdated"] = time.Now()

	fmt.Printf("\n📊 Analytics 데이터 수집 완료\n\n")

	// 8. Firebase에 저장 (submission 메인 필드 + analytics 서브필드)
	if err := h.saveAnalyticsToSubmission(ctx, reqBody.CompetitionID, reqBody.SubmissionID, video, analyticsData); err != nil {
		fmt.Printf("❌ Firebase 저장 실패: %v\n", err)
		c.JSON(500, gin.H{"error": "Failed to save analytics"})
		return
	}

	fmt.Printf("✅ Analytics 저장 완료!\n\n")

	c.JSON(200, gin.H{
		"success":   true,
		"message":   "비디오 연동 및 상세 통계 수집 완료",
		"analytics": analyticsData,
	})
}

// createBasicAnalyticsData - 기본 Analytics 데이터 생성 (YouTube Data API)
// 메인 필드와 중복되지 않는 analytics 전용 데이터만 저장
func (h *YouTubeHandlerFirestore) createBasicAnalyticsData(video *youtube.Video) gin.H {
	return gin.H{
		"videoId":     video.Id,
		"publishedAt": video.Snippet.PublishedAt,
		"isVerified":  true,
	}
}

// saveAnalyticsToSubmission - Analytics 데이터를 submissions 문서의 필드로 저장
// video 객체를 추가로 받아서 메인 필드도 함께 업데이트
func (h *YouTubeHandlerFirestore) saveAnalyticsToSubmission(ctx context.Context, competitionID, submissionID string, video *youtube.Video, analyticsData gin.H) error {
	// ⭐ Step 1: submission 문서 존재 여부 확인
	submissionRef := h.firestore.Collection("competitions").Doc(competitionID).
		Collection("submissions").Doc(submissionID)

	submissionDoc, err := submissionRef.Get(ctx)
	if err != nil {
		fmt.Printf("❌ Submission 문서 조회 실패: %v\n", err)
		return fmt.Errorf("submission 문서를 찾을 수 없습니다: %w", err)
	}

	if !submissionDoc.Exists() {
		fmt.Printf("❌ Submission 문서가 존재하지 않음: %s/%s\n", competitionID, submissionID)
		return fmt.Errorf("submission 문서가 존재하지 않습니다")
	}

	fmt.Printf("✅ Submission 문서 존재 확인\n")

	// ⭐ Step 2: 메인 필드 + analytics 서브필드 동시 업데이트
	// YouTube API에서 가져온 최신 통계로 submission의 메인 필드 업데이트
	// ⚠️ 중요: YouTube API는 uint64를 반환하지만 Firestore는 int64만 지원
	updateData := map[string]interface{}{
		// Analytics 서브필드 (상세 통계)
		"analytics": analyticsData,

		// Submission 메인 필드 (최신 통계로 업데이트)
		// uint64 → int64 변환
		"currentViewCount": int64(video.Statistics.ViewCount),
		"viewCount":        int64(video.Statistics.ViewCount),
		"likeCount":        int64(video.Statistics.LikeCount),
		"commentCount":     int64(video.Statistics.CommentCount),
		"videoTitle":       video.Snippet.Title,
		"thumbnailUrl":     video.Snippet.Thumbnails.Default.Url,

		// 업데이트 시간
		"updatedAt": time.Now(),
	}

	fmt.Printf("📊 업데이트할 데이터:\n")
	fmt.Printf("  - viewCount: %d\n", video.Statistics.ViewCount)
	fmt.Printf("  - likeCount: %d\n", video.Statistics.LikeCount)
	fmt.Printf("  - commentCount: %d\n", video.Statistics.CommentCount)
	fmt.Printf("  - analytics.isVerified: true\n\n")

	// ⭐ Update 사용 (기존 필드 유지하면서 업데이트)
	_, err = submissionRef.Set(ctx, updateData, firestore.MergeAll)
	if err != nil {
		fmt.Printf("❌ Firebase 업데이트 실패: %v\n", err)
		return fmt.Errorf("firebase 업데이트 실패: %w", err)
	}

	fmt.Printf("✅ Firebase 업데이트 완료\n")
	return nil
}

func (h *YouTubeHandlerFirestore) GetVideoAnalytics(c *gin.Context) {
	videoID := c.Param("videoId")
	ctx := context.Background()

	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(401, gin.H{"error": "Unauthorized"})
		return
	}

	doc, err := h.firestore.Collection("users").Doc(userID).
		Collection("connections").Doc("youtube").Get(ctx)

	if err != nil {
		c.JSON(401, gin.H{"error": "YouTube not connected"})
		return
	}

	var tokenData YouTubeToken
	if err := doc.DataTo(&tokenData); err != nil {
		c.JSON(500, gin.H{"error": "Failed to parse token"})
		return
	}

	token := &oauth2.Token{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		TokenType:    "Bearer",
		Expiry:       tokenData.ExpiresAt,
	}

	if token.Expiry.Before(time.Now()) && tokenData.RefreshToken != "" {
		newToken, err := h.oauth2Config.TokenSource(context.Background(), token).Token()
		if err == nil {
			token = newToken

			_, _ = h.firestore.Collection("users").Doc(userID).
				Collection("connections").Doc("youtube").Update(ctx, []firestore.Update{
				{Path: "accessToken", Value: newToken.AccessToken},
				{Path: "expiresAt", Value: newToken.Expiry},
				{Path: "updatedAt", Value: time.Now()},
			})
		}
	}

	client := h.oauth2Config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))

	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to create YouTube service"})
		return
	}

	videoResponse, err := youtubeService.Videos.List([]string{"snippet", "statistics", "contentDetails"}).
		Id(videoID).Do()

	if err != nil || len(videoResponse.Items) == 0 {
		c.JSON(404, gin.H{"error": "Video not found"})
		return
	}

	video := videoResponse.Items[0]

	analyticsService, err := youtubeanalytics.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {

		c.JSON(200, gin.H{
			"videoId": videoID,
			"basic": gin.H{
				"title":        video.Snippet.Title,
				"viewCount":    video.Statistics.ViewCount,
				"likeCount":    video.Statistics.LikeCount,
				"commentCount": video.Statistics.CommentCount,
			},
			"analytics": gin.H{
				"available": false,
				"message":   "Analytics API not available",
			},
		})
		return
	}

	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")

	result := gin.H{
		"videoId": videoID,
		"basic": gin.H{
			"title":        video.Snippet.Title,
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

	demographicsData := gin.H{
		"gender":   gin.H{},
		"ageGroup": gin.H{},
	}

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

	var geographyData []gin.H
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
					"country":        fmt.Sprintf("%v", row[0]),
					"views":          row[1].(float64),
					"minutesWatched": row[2].(float64),
				})
			}
		}
	}

	result["geography"] = geographyData

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
					"views":  row[1].(float64),
				})
			}
		}
	}

	result["trafficSources"] = trafficData

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
					"views":  row[1].(float64),
				})
			}
		}
	}

	result["devices"] = deviceData

	result["analytics"] = gin.H{
		"available":   true,
		"lastUpdated": time.Now().Format(time.RFC3339),
	}

	// GetVideoAnalytics는 조회용으로만 사용 (저장 X)
	c.JSON(200, result)
}
