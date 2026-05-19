package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"

	"adfit-oauth/config"
)

type YouTubeTrendService struct {
	apiKeys      []string
	currentIndex int
	mu           sync.Mutex
}

// 영상 정보 구조체
type VideoInfo struct {
	VideoID      string  `json:"videoId"`
	Title        string  `json:"title"`
	ChannelID    string  `json:"channelId"`
	ChannelTitle string  `json:"channelTitle"`
	Thumbnail    string  `json:"thumbnail"`
	PublishedAt  string  `json:"publishedAt"`
	ViewCount    string  `json:"viewCount"`
	LikeCount    string  `json:"likeCount"`
	CommentCount string  `json:"commentCount"`
	Duration     string  `json:"duration"`
	CategoryID   string  `json:"categoryId"`
	ViralScore   float64 `json:"viralScore,omitempty"`
}

// 주제 분석 결과
type TopicAnalysis struct {
	Keyword      string      `json:"keyword"`
	TotalVideos  int         `json:"totalVideos"`
	AvgViewCount int64       `json:"avgViewCount"`
	AvgLikeCount int64       `json:"avgLikeCount"`
	Videos       []VideoInfo `json:"videos"`
}

func NewYouTubeTrendService() (*YouTubeTrendService, error) {
	apiKeys := config.GetYouTubeAPIKeys()
	if len(apiKeys) == 0 {
		return nil, fmt.Errorf("YouTube API Key가 설정되지 않았습니다")
	}

	log.Printf("✅ YouTubeTrendService 초기화 완료 (API Keys: %d개)", len(apiKeys))
	return &YouTubeTrendService{
		apiKeys:      apiKeys,
		currentIndex: 0,
	}, nil
}

// 현재 API Key로 YouTube 서비스 생성
func (s *YouTubeTrendService) getYouTubeService() (*youtube.Service, error) {
	s.mu.Lock()
	apiKey := s.apiKeys[s.currentIndex]
	s.mu.Unlock()

	ctx := context.Background()
	return youtube.NewService(ctx, option.WithAPIKey(apiKey))
}

// 다음 API Key로 전환
func (s *YouTubeTrendService) rotateAPIKey() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	nextIndex := (s.currentIndex + 1) % len(s.apiKeys)
	if nextIndex == 0 {
		// 모든 키를 다 돌았음
		log.Println("⚠️ 모든 YouTube API Key 소진")
		return false
	}
	s.currentIndex = nextIndex
	log.Printf("🔄 YouTube API Key 전환: %d번째 키 사용", s.currentIndex+1)
	return true
}

// API 호출 실행 (실패 시 자동 로테이션)
func (s *YouTubeTrendService) executeWithRetry(fn func(*youtube.Service) error) error {
	triedKeys := 0
	maxRetries := len(s.apiKeys)

	for triedKeys < maxRetries {
		svc, err := s.getYouTubeService()
		if err != nil {
			return fmt.Errorf("YouTube 서비스 생성 실패: %v", err)
		}

		err = fn(svc)
		if err == nil {
			return nil
		}

		// 쿼터 초과 에러인지 확인
		if isQuotaError(err) {
			log.Printf("⚠️ API Key 쿼터 초과: %v", err)
			if !s.rotateAPIKey() {
				return fmt.Errorf("모든 API Key 쿼터 초과: %v", err)
			}
			triedKeys++
			continue
		}

		// 다른 에러는 그대로 반환
		return err
	}

	return fmt.Errorf("모든 API Key 실패")
}

// 쿼터 초과 에러 확인
func isQuotaError(err error) bool {
	if apiErr, ok := err.(*googleapi.Error); ok {
		// 403: quotaExceeded, 429: rateLimitExceeded
		if apiErr.Code == 403 || apiErr.Code == 429 {
			return true
		}
	}
	return false
}

// ============ 1. 검색 ============
func (s *YouTubeTrendService) SearchVideos(query string, maxResults int64, order string) ([]VideoInfo, error) {
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 20
	}
	if order == "" {
		order = "relevance"
	}

	var videoIDs []string
	err := s.executeWithRetry(func(svc *youtube.Service) error {
		searchResp, err := svc.Search.List([]string{"snippet"}).
			Q(query).
			Type("video").
			Order(order).
			MaxResults(maxResults).
			Do()
		if err != nil {
			return err
		}

		videoIDs = make([]string, 0, len(searchResp.Items))
		for _, item := range searchResp.Items {
			videoIDs = append(videoIDs, item.Id.VideoId)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("검색 실패: %v", err)
	}

	return s.getVideoDetails(videoIDs)
}

// ============ 2. 트렌딩 ============
func (s *YouTubeTrendService) GetTrendingVideos(regionCode, categoryID string, maxResults int64) ([]VideoInfo, error) {
	if regionCode == "" {
		regionCode = "KR"
	}
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 20
	}

	var videos []VideoInfo
	err := s.executeWithRetry(func(svc *youtube.Service) error {
		call := svc.Videos.List([]string{"snippet", "statistics", "contentDetails"}).
			Chart("mostPopular").
			RegionCode(regionCode).
			MaxResults(maxResults)

		if categoryID != "" && categoryID != "0" {
			call = call.VideoCategoryId(categoryID)
		}

		resp, err := call.Do()
		if err != nil {
			return err
		}

		videos = s.parseVideoResponse(resp.Items)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("트렌딩 조회 실패: %v", err)
	}

	return videos, nil
}

// ============ 3. 주제 분석 ============
func (s *YouTubeTrendService) GetTopicAnalysis(keyword string, maxResults int64, order string) (*TopicAnalysis, error) {
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 20
	}
	if order == "" {
		order = "viewCount"
	}

	videos, err := s.SearchVideos(keyword, maxResults, order)
	if err != nil {
		return nil, err
	}

	var totalViews, totalLikes int64
	for _, v := range videos {
		totalViews += parseCount(v.ViewCount)
		totalLikes += parseCount(v.LikeCount)
	}

	avgViews := int64(0)
	avgLikes := int64(0)
	if len(videos) > 0 {
		avgViews = totalViews / int64(len(videos))
		avgLikes = totalLikes / int64(len(videos))
	}

	return &TopicAnalysis{
		Keyword:      keyword,
		TotalVideos:  len(videos),
		AvgViewCount: avgViews,
		AvgLikeCount: avgLikes,
		Videos:       videos,
	}, nil
}

// ============ 4. 바이럴 영상 찾기 ============
func (s *YouTubeTrendService) GetViralVideos(regionCode, publishedAfter string, maxResults int64) ([]VideoInfo, error) {
	if regionCode == "" {
		regionCode = "KR"
	}
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 30
	}

	var afterTime time.Time
	switch publishedAfter {
	case "week":
		afterTime = time.Now().AddDate(0, 0, -7)
	case "month":
		afterTime = time.Now().AddDate(0, -1, 0)
	case "year":
		afterTime = time.Now().AddDate(-1, 0, 0)
	default:
		afterTime = time.Now().AddDate(0, 0, -7)
	}

	var videoIDs []string
	err := s.executeWithRetry(func(svc *youtube.Service) error {
		searchResp, err := svc.Search.List([]string{"snippet"}).
			Type("video").
			Order("viewCount").
			RegionCode(regionCode).
			PublishedAfter(afterTime.Format(time.RFC3339)).
			MaxResults(maxResults).
			Do()
		if err != nil {
			return err
		}

		videoIDs = make([]string, 0, len(searchResp.Items))
		for _, item := range searchResp.Items {
			videoIDs = append(videoIDs, item.Id.VideoId)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("바이럴 검색 실패: %v", err)
	}

	videos, err := s.getVideoDetails(videoIDs)
	if err != nil {
		return nil, err
	}

	// 바이럴 점수 계산
	for i := range videos {
		publishedAt, _ := time.Parse(time.RFC3339, videos[i].PublishedAt)
		daysOld := time.Since(publishedAt).Hours() / 24
		if daysOld < 1 {
			daysOld = 1
		}
		viewCount := parseCount(videos[i].ViewCount)
		videos[i].ViralScore = float64(viewCount) / daysOld
	}

	sort.Slice(videos, func(i, j int) bool {
		return videos[i].ViralScore > videos[j].ViralScore
	})

	return videos, nil
}

// ============ 내부 헬퍼 ============

func (s *YouTubeTrendService) getVideoDetails(videoIDs []string) ([]VideoInfo, error) {
	if len(videoIDs) == 0 {
		return []VideoInfo{}, nil
	}

	var videos []VideoInfo
	err := s.executeWithRetry(func(svc *youtube.Service) error {
		resp, err := svc.Videos.List([]string{"snippet", "statistics", "contentDetails"}).
			Id(videoIDs...).
			Do()
		if err != nil {
			return err
		}

		videos = s.parseVideoResponse(resp.Items)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("영상 상세 조회 실패: %v", err)
	}

	return videos, nil
}

func (s *YouTubeTrendService) parseVideoResponse(items []*youtube.Video) []VideoInfo {
	videos := make([]VideoInfo, 0, len(items))
	for _, item := range items {
		thumbnail := ""
		if item.Snippet.Thumbnails != nil {
			if item.Snippet.Thumbnails.Medium != nil {
				thumbnail = item.Snippet.Thumbnails.Medium.Url
			} else if item.Snippet.Thumbnails.Default != nil {
				thumbnail = item.Snippet.Thumbnails.Default.Url
			}
		}

		v := VideoInfo{
			VideoID:      item.Id,
			Title:        item.Snippet.Title,
			ChannelID:    item.Snippet.ChannelId,
			ChannelTitle: item.Snippet.ChannelTitle,
			Thumbnail:    thumbnail,
			PublishedAt:  item.Snippet.PublishedAt,
			CategoryID:   item.Snippet.CategoryId,
		}

		if item.Statistics != nil {
			v.ViewCount = fmt.Sprintf("%d", item.Statistics.ViewCount)
			v.LikeCount = fmt.Sprintf("%d", item.Statistics.LikeCount)
			v.CommentCount = fmt.Sprintf("%d", item.Statistics.CommentCount)
		}

		if item.ContentDetails != nil {
			v.Duration = item.ContentDetails.Duration
		}

		videos = append(videos, v)
	}
	return videos
}

func parseCount(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}
