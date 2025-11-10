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
	
	
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	
	
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
	
	
	demographicsData := gin.H{
		"gender": gin.H{},
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
					"country": fmt.Sprintf("%v", row[0]),
					"views": row[1].(float64),
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
					"views": row[1].(float64),
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
					"views": row[1].(float64),
				})
			}
		}
	}
	
	result["devices"] = deviceData
	
	
	result["analytics"] = gin.H{
		"available": true,
		"lastUpdated": time.Now().Format(time.RFC3339),
	}
	
	c.JSON(200, result)
}
