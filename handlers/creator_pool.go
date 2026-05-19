package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"adfit-oauth/models"
)

type CreatorPoolHandler struct {
	DB *gorm.DB
}

// ytImageClient - YouTube API 이미지 요청 전용 클라이언트 (timeout 설정)
var ytImageClient = &http.Client{Timeout: 10 * time.Second}

func NewCreatorPoolHandler(db *gorm.DB) *CreatorPoolHandler {
	return &CreatorPoolHandler{DB: db}
}

// Creator - creators 테이블 매핑
type Creator struct {
	ID              int    `gorm:"column:id" json:"id"`
	Platform        string `gorm:"column:platform" json:"platform"`
	Handle          string `gorm:"column:handle" json:"handle"`
	Name            string `gorm:"column:name" json:"name"`
	ProfileImage    string `gorm:"column:profile_image" json:"profileImage"`
	Category        string `gorm:"column:category" json:"category"`
	Followers       int64  `gorm:"column:followers" json:"followers"`
	EngagementRate  string `gorm:"column:engagement_rate" json:"engagementRate"`
	Language        string `gorm:"column:language" json:"language"`
	AvgViews        int64  `gorm:"column:avg_views" json:"avgViews"`
	AvgLikes        int64  `gorm:"column:avg_likes" json:"avgLikes"`
	AvgComments     int64  `gorm:"column:avg_comments" json:"avgComments"`
	AudienceGender  string `gorm:"column:audience_gender" json:"audienceGender"`
	AudienceAge     string `gorm:"column:audience_age" json:"audienceAge"`
	EstimatedAdCost string `gorm:"column:estimated_ad_cost" json:"estimatedAdCost"`
	LastUpload      string `gorm:"column:last_upload" json:"lastUpload"`
	PlatformURL     string `gorm:"column:platform_url" json:"platformUrl"`
	FeaturingURL    string `gorm:"column:featuring_url" json:"featuringUrl"`
	Email           string `gorm:"column:email" json:"-"` // 브랜드 응답에서 제외 (개인정보 보호)
}

// GetCreators - 크리에이터 목록 (페이지네이션 + 필터 + 검색)
// GET /api/creators?platform=youtube&language=한국어&search=김&category=패션&sortBy=followers&sortDir=desc&page=1&limit=50
func (h *CreatorPoolHandler) GetCreators(c *gin.Context) {
	// 쿼리 파라미터
	platform := c.DefaultQuery("platform", "all")
	language := c.DefaultQuery("language", "all")
	search := c.DefaultQuery("search", "")
	category := c.DefaultQuery("category", "all")
	secondaryCode := c.Query("secondaryCode")
	primaryCode := c.Query("primaryCode")
	followerTier := c.Query("followerTier")
	minAvgViewsStr := c.Query("minAvgViews")
	maxAvgViewsStr := c.Query("maxAvgViews")
	sortBy := c.DefaultQuery("sortBy", "followers")
	sortDir := c.DefaultQuery("sortDir", "desc")
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "50")

	page, _ := strconv.Atoi(pageStr)
	limit, _ := strconv.Atoi(limitStr)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// 쿼리 빌드
	query := h.DB.Table("creators")

	// 플랫폼 필터
	if platform != "all" && platform != "" {
		query = query.Where("platform = ?", platform)
	}

	// 언어 필터
	if language != "all" && language != "" {
		query = query.Where("language = ?", language)
	}

	// 카테고리 필터: secondaryCode/primaryCode 우선(매핑 조인, 결정 B/E), 없으면 category= 하위호환
	var cats []string
	mappingRequested := secondaryCode != "" || primaryCode != ""
	if mappingRequested {
		cats = h.resolveMappedCategories(secondaryCode, primaryCode, platform)
	} else if category != "all" && category != "" {
		cats = strings.Split(category, ",")
	}
	uniq := make([]string, 0, len(cats))
	seen := map[string]bool{}
	for _, ct := range cats {
		ct = strings.TrimSpace(ct)
		if ct == "" || seen[ct] {
			continue
		}
		seen[ct] = true
		uniq = append(uniq, ct)
	}
	if len(uniq) > 0 {
		orConditions := make([]string, len(uniq))
		args := make([]interface{}, len(uniq))
		for i, ct := range uniq {
			orConditions[i] = "category ILIKE ?"
			args[i] = "%" + ct + "%"
		}
		query = query.Where(strings.Join(orConditions, " OR "), args...)
	} else if mappingRequested {
		// 매핑이 요청됐으나 결과 0건 → 빈 결과 (필터 무시 방지)
		query = query.Where("1 = 0")
	}

	// 검색 (이름 또는 핸들)
	if search != "" {
		query = query.Where(
			"name ILIKE ? OR handle ILIKE ?",
			"%"+search+"%", "%"+search+"%",
		)
	}

	// 평균 조회수 범위 필터
	if minAvgViewsStr != "" {
		if v, err := strconv.ParseInt(minAvgViewsStr, 10, 64); err == nil {
			query = query.Where("avg_views >= ?", v)
		}
	}
	if maxAvgViewsStr != "" {
		if v, err := strconv.ParseInt(maxAvgViewsStr, 10, 64); err == nil {
			query = query.Where("avg_views <= ?", v)
		}
	}

	// 팔로워 등급 필터 (결정 F): follower_tier 조회 후 범위 적용
	if followerTier != "" {
		var ft models.FollowerTier
		tq := h.DB.Where("code = ?", followerTier)
		if platform != "" && platform != "all" {
			tq = tq.Where("platform = ?", platform)
		}
		if err := tq.Order("platform").First(&ft).Error; err == nil {
			query = query.Where("followers >= ?", ft.MinFollowers)
			if ft.MaxFollowers != nil {
				query = query.Where("followers < ?", *ft.MaxFollowers)
			}
		}
	}

	// 정렬
	allowedSort := map[string]string{
		"followers":      "followers",
		"avgViews":       "avg_views",
		"avgLikes":       "avg_likes",
		"engagement":     "engagement_rate",
		"name":           "name",
		"language":       "language",
		"category":       "category",
		"lastUpload":     "last_upload",
		"audienceAge":    "audience_age",
		"audienceGender": "audience_gender",
	}
	col, ok := allowedSort[sortBy]
	if !ok {
		col = "followers"
	}
	dir := "DESC"
	if strings.ToLower(sortDir) == "asc" {
		dir = "ASC"
	}
	orderClause := fmt.Sprintf("%s %s NULLS LAST", col, dir)

	// 전체 건수 (필터 동일, Order/Limit/Offset 제외) — 페이지 수 계산용
	// GORM: Count 가 statement 를 변형하므로 세션 분리 후 호출 (이후 Find 영향 방지)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	totalPages := int((total + int64(limit) - 1) / int64(limit))
	if totalPages < 1 {
		totalPages = 1
	}

	// 데이터 조회 (limit+1개 가져와서 다음 페이지 여부 판단)
	var creators []Creator
	result := query.
		Order(orderClause).
		Limit(limit + 1).
		Offset(offset).
		Find(&creators)

	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}

	hasMore := len(creators) > limit
	if hasMore {
		creators = creators[:limit] // 여분 1개 제거
	}

	// featuring.co URL 클린업 + YouTube channel_id 기반 API 키 URL 구성
	h.cleanProfileImages(creators)

	c.JSON(http.StatusOK, gin.H{
		"data": creators,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"hasMore":    hasMore,
			"total":      total,
			"totalPages": totalPages,
		},
	})

	// background: YouTube image 없는 항목 API로 채우고 DB 저장
	go h.fillMissingYouTubeImages(creators)
}

// resolveMappedCategories - secondaryCode/primaryCode → category_secondary_mapping 조인
// (결정 E) platform 지정 시 해당 플랫폼 + 'all' 매핑만 사용
func (h *CreatorPoolHandler) resolveMappedCategories(secondaryCode, primaryCode, platform string) []string {
	q := h.DB.Table("category_secondary_mapping AS m")
	if secondaryCode != "" {
		q = q.Where("m.secondary_code = ?", secondaryCode)
	} else if primaryCode != "" {
		q = q.Joins("JOIN category_secondary s ON s.code = m.secondary_code").
			Where("s.primary_code = ?", primaryCode)
	} else {
		return nil
	}
	if platform != "" && platform != "all" {
		q = q.Where("m.platform = ? OR m.platform = ?", platform, "all")
	}
	var cats []string
	q.Distinct().Pluck("m.creator_category", &cats)
	return cats
}

// cleanProfileImages - featuring.co 등 동작하지 않는 URL은 비워서
// Flutter에서 플레이스홀더 아이콘으로 폴백되게 함
func (h *CreatorPoolHandler) cleanProfileImages(creators []Creator) {
	brokenDomains := []string{
		"featuring.co",
		"image.featuring",
	}
	for i := range creators {
		for _, domain := range brokenDomains {
			if strings.Contains(creators[i].ProfileImage, domain) {
				creators[i].ProfileImage = ""
				break
			}
		}
	}
}

// fillMissingYouTubeImages - profile_image 없는 YouTube 크리에이터를
// YouTube Data API로 이미지 URL 가져와 DB 저장 (백그라운드)
func (h *CreatorPoolHandler) fillMissingYouTubeImages(creators []Creator) {
	apiKey := os.Getenv("YOUTUBE_API_KEY")
	if apiKey == "" {
		// 여러 API 키 중 첫 번째 사용
		if keys := os.Getenv("YOUTUBE_API_KEYS"); keys != "" {
			apiKey = strings.Split(keys, ",")[0]
		}
	}
	if apiKey == "" {
		return
	}

	// image 없는 YouTube 항목 필터
	var missing []Creator
	for _, c := range creators {
		if c.Platform == "youtube" && c.ProfileImage == "" {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return
	}

	// channel_id 추출
	type entry struct {
		id        int
		channelID string
	}
	var entries []entry
	for _, c := range missing {
		if !strings.Contains(c.PlatformURL, "/channel/") {
			continue
		}
		parts := strings.Split(c.PlatformURL, "/channel/")
		if len(parts) < 2 {
			continue
		}
		chid := strings.Split(parts[1], "?")[0]
		if chid != "" {
			entries = append(entries, entry{id: c.ID, channelID: chid})
		}
	}
	if len(entries) == 0 {
		return
	}

	// YouTube API 호출 (50개씩 배치)
	const batchSize = 50
	for i := 0; i < len(entries); i += batchSize {
		batch := entries[i:min(i+batchSize, len(entries))]

		ids := make([]string, len(batch))
		for j, e := range batch {
			ids[j] = e.channelID
		}

		url := fmt.Sprintf(
			"https://youtube.googleapis.com/youtube/v3/channels?part=snippet&id=%s&key=%s",
			strings.Join(ids, ","), apiKey,
		)

		resp, err := ytImageClient.Get(url)
		if err != nil || resp.StatusCode != 200 {
			log.Printf("⚠️ YouTube API 호출 실패: %v", err)
			continue
		}

		var result struct {
			Items []struct {
				ID      string `json:"id"`
				Snippet struct {
					Thumbnails struct {
						Medium  *struct{ URL string `json:"url"` } `json:"medium"`
						Default *struct{ URL string `json:"url"` } `json:"default"`
					} `json:"thumbnails"`
				} `json:"snippet"`
			} `json:"items"`
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}

		// channelID -> thumbnailURL 맵
		thumbMap := map[string]string{}
		for _, item := range result.Items {
			thumb := item.Snippet.Thumbnails.Medium
			if thumb == nil {
				thumb = item.Snippet.Thumbnails.Default
			}
			if thumb != nil && thumb.URL != "" {
				thumbMap[item.ID] = thumb.URL
			}
		}

		// DB 업데이트
		for _, e := range batch {
			if thumbURL, ok := thumbMap[e.channelID]; ok {
				h.DB.Exec(
					"UPDATE creators SET profile_image = ? WHERE id = ?",
					thumbURL, e.id,
				)
			}
		}
	}
}

// GetCreatorStats - 플랫폼별 통계
// GET /api/admin/creators/stats
func (h *CreatorPoolHandler) GetCreatorStats(c *gin.Context) {
	type PlatformStat struct {
		Platform string `gorm:"column:platform" json:"platform"`
		Count    int64  `gorm:"column:count" json:"count"`
	}

	var stats []PlatformStat
	h.DB.Table("creators").
		Select("platform, COUNT(*) as count").
		Group("platform").
		Find(&stats)

	var total int64
	h.DB.Table("creators").Count(&total)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"total":     total,
			"platforms": stats,
		},
	})
}

// SaveCreator - 즐겨찾기 추가
// POST /api/creators/save
func (h *CreatorPoolHandler) SaveCreator(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		Platform     string `json:"platform" binding:"required"`
		Handle       string `json:"handle" binding:"required"`
		Name         string `json:"name"`
		ProfileImage string `json:"profileImage"`
		Category     string `json:"category"`
		Followers    int64  `json:"followers"`
		PlatformURL  string `json:"platformUrl"`
		Memo         string `json:"memo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sql := `
		INSERT INTO creator_saved (user_id, platform, handle, name, profile_image, category, followers, platform_url, memo)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, platform, handle) DO UPDATE SET memo = EXCLUDED.memo, saved_at = NOW()
	`
	if err := h.DB.Exec(sql,
		userID, req.Platform, req.Handle, req.Name,
		req.ProfileImage, req.Category, req.Followers, req.PlatformURL, req.Memo,
	).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RemoveCreator - 즐겨찾기 제거
// DELETE /api/creators/save
func (h *CreatorPoolHandler) RemoveCreator(c *gin.Context) {
	userID := c.GetString("user_id")
	platform := c.Query("platform")
	handle := c.Query("handle")

	if userID == "" || platform == "" || handle == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing parameters"})
		return
	}

	result := h.DB.Exec(
		"DELETE FROM creator_saved WHERE user_id = ? AND platform = ? AND handle = ?",
		userID, platform, handle,
	)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetSavedCreators - 즐겨찾기 목록
// GET /api/creators/saved
func (h *CreatorPoolHandler) GetSavedCreators(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	type SavedCreator struct {
		ID           string `gorm:"column:id" json:"id"`
		Platform     string `gorm:"column:platform" json:"platform"`
		Handle       string `gorm:"column:handle" json:"handle"`
		Name         string `gorm:"column:name" json:"name"`
		ProfileImage string `gorm:"column:profile_image" json:"profileImage"`
		Category     string `gorm:"column:category" json:"category"`
		Followers    int64  `gorm:"column:followers" json:"followers"`
		PlatformURL  string `gorm:"column:platform_url" json:"platformUrl"`
		Memo         string `gorm:"column:memo" json:"memo"`
		SavedAt      string `gorm:"column:saved_at" json:"savedAt"`
	}

	var saved []SavedCreator
	h.DB.Table("creator_saved").
		Where("user_id = ?", userID).
		Order("saved_at DESC").
		Find(&saved)

	c.JSON(http.StatusOK, gin.H{"data": saved})
}
