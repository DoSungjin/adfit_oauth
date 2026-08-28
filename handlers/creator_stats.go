package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"adfit-oauth/models"
)

// ──────────────────────────────────────────────────────────────
// Admin: 크리에이터 풀 분석 통계 (필터링된 슬라이스의 집계)
// GET /api/admin/creators/analytics
// 같은 필터 파라미터: platform, language, search, secondaryCode, primaryCode,
//                     followerTier, minAvgViews, maxAvgViews
// ──────────────────────────────────────────────────────────────

// GetCreatorAnalytics - 필터링된 크리에이터 슬라이스에 대한 집계 통계
// (요약 지표 + 조회수 분포 + 팔로워↔가격 산점도 + 팔로워 분포 + 언어 분포 + Top 5 + 등급별 CPV)
func (h *CreatorPoolHandler) GetCreatorAnalytics(c *gin.Context) {
	// ── 필터 파라미터 (GetCreators와 동일) ──
	platform := c.DefaultQuery("platform", "all")
	language := c.DefaultQuery("language", "all")
	search := c.DefaultQuery("search", "")
	secondaryCode := c.Query("secondaryCode")
	primaryCode := c.Query("primaryCode")
	followerTier := c.Query("followerTier")
	minAvgViewsStr := c.Query("minAvgViews")
	maxAvgViewsStr := c.Query("maxAvgViews")

	isPostgres := h.DB.Dialector.Name() == "postgres"

	// ── 공통 WHERE 절을 함수로 캡슐화 (각 집계 쿼리마다 동일 적용) ──
	applyFilters := func(q *gorm.DB) *gorm.DB {
		if platform != "all" && platform != "" {
			q = q.Where("platform = ?", platform)
		}
		if language != "all" && language != "" {
			q = q.Where("language = ?", language)
		}
		// 카테고리 매핑
		var cats []string
		mappingRequested := secondaryCode != "" || primaryCode != ""
		if mappingRequested {
			cats = h.resolveMappedCategories(secondaryCode, primaryCode, platform)
		}
		if len(cats) > 0 {
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
				op := "ILIKE"
				if !isPostgres {
					op = "LIKE"
				}
				for i, ct := range uniq {
					orConditions[i] = "category " + op + " ?"
					args[i] = "%" + ct + "%"
				}
				q = q.Where(strings.Join(orConditions, " OR "), args...)
			}
		} else if mappingRequested {
			q = q.Where("1 = 0")
		}
		if search != "" {
			op := "ILIKE"
			if !isPostgres {
				op = "LIKE"
			}
			q = q.Where("name "+op+" ? OR handle "+op+" ?", "%"+search+"%", "%"+search+"%")
		}
		if minAvgViewsStr != "" {
			if v, err := strconv.ParseInt(minAvgViewsStr, 10, 64); err == nil {
				q = q.Where("avg_views >= ?", v)
			}
		}
		if maxAvgViewsStr != "" {
			if v, err := strconv.ParseInt(maxAvgViewsStr, 10, 64); err == nil {
				q = q.Where("avg_views <= ?", v)
			}
		}
		if followerTier != "" {
			var ft models.FollowerTier
			tq := h.DB.Where("code = ?", followerTier)
			if platform != "" && platform != "all" {
				tq = tq.Where("platform = ?", platform)
			}
			if err := tq.Order("platform").First(&ft).Error; err == nil {
				q = q.Where("followers >= ?", ft.MinFollowers)
				if ft.MaxFollowers != nil {
					q = q.Where("followers < ?", *ft.MaxFollowers)
				}
			}
		}
		return q
	}

	// PostgreSQL 전용 표현식 / SQLite 폴백 — 문자열 컬럼 캐스팅
	// 정규식 강화:
	//  - engagement_rate: '%' 포함 가능
	//  - estimated_ad_cost:
	//      * 단위어 ('억', '만', '천')에 곱셈 적용 ("1.5억" → 1.5 * 1억 = 150,000,000)
	//      * 순수 숫자 ("1,000,000원" → 비숫자 제거 → 1,000,000)
	//      * 우선순위: 억 → 만 → 천 → 순수 숫자 ("100만"이 "100"으로 잘리지 않게)
	// 정규식 anchor 문자(끝-of-string) 처리를 위해 fmt.Sprintf + %q 또는 raw string 사용
	var engagementExpr, priceExpr string
	if isPostgres {
		engagementRegex := "^[0-9]+\\.?[0-9]*" + endOfString()
		engagementExpr = "CASE WHEN engagement_rate IS NULL OR engagement_rate = '' THEN NULL " +
			"WHEN replace(engagement_rate, '%', '') ~ '" + engagementRegex + "' " +
			"THEN replace(engagement_rate, '%', '')::numeric ELSE NULL END"

		priceExpr = "CASE " +
			"WHEN estimated_ad_cost IS NULL OR btrim(estimated_ad_cost) = '' THEN NULL " +
			// "억" 단위
			"WHEN estimated_ad_cost ~ '[0-9]+(\\.[0-9]+)?\\s*억' " +
			"THEN (substring(substring(estimated_ad_cost from '[0-9]+(\\.[0-9]+)?\\s*억') from '[0-9]+(\\.[0-9]+)?'))::numeric * 100000000 " +
			// "만" 단위
			"WHEN estimated_ad_cost ~ '[0-9]+(\\.[0-9]+)?\\s*만' " +
			"THEN (substring(substring(estimated_ad_cost from '[0-9]+(\\.[0-9]+)?\\s*만') from '[0-9]+(\\.[0-9]+)?'))::numeric * 10000 " +
			// "천" 단위
			"WHEN estimated_ad_cost ~ '[0-9]+(\\.[0-9]+)?\\s*천' " +
			"THEN (substring(substring(estimated_ad_cost from '[0-9]+(\\.[0-9]+)?\\s*천') from '[0-9]+(\\.[0-9]+)?'))::numeric * 1000 " +
			// 순수 숫자
			"WHEN length(regexp_replace(estimated_ad_cost, '[^0-9]', '', 'g')) > 0 " +
			"AND length(regexp_replace(estimated_ad_cost, '[^0-9]', '', 'g')) <= 18 " +
			"THEN regexp_replace(estimated_ad_cost, '[^0-9]', '', 'g')::numeric " +
			"ELSE NULL END"
	} else {
		// SQLite 로컬 dev: 캐스팅 단순화
		engagementExpr = "CAST(engagement_rate AS REAL)"
		priceExpr = "CAST(estimated_ad_cost AS INTEGER)"
	}

	// ── 1. sampleSize + summary (단일 쿼리) ──
	type summaryRow struct {
		SampleSize        int64    `gorm:"column:sample_size"`
		AvgFollowers      *float64 `gorm:"column:avg_followers"`
		AvgViews          *float64 `gorm:"column:avg_views"`
		AvgEngagementRate *float64 `gorm:"column:avg_engagement"`
		AvgPrice          *float64 `gorm:"column:avg_price"`
	}
	var s summaryRow
	selectExpr := "COUNT(*) AS sample_size, " +
		"AVG(followers) AS avg_followers, " +
		"AVG(avg_views) AS avg_views, " +
		"AVG(" + engagementExpr + ") AS avg_engagement, " +
		"AVG(" + priceExpr + ") AS avg_price"
	if err := applyFilters(h.DB.Table("creators")).Select(selectExpr).Scan(&s).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "summary 집계 실패: " + err.Error()})
		return
	}

	// ── 1b. 중앙값 (PERCENTILE_DISC) ──
	type medianRow struct {
		MedianFollowers *float64 `gorm:"column:median_followers"`
		MedianViews     *float64 `gorm:"column:median_views"`
		MedianPrice     *float64 `gorm:"column:median_price"`
	}
	var m medianRow
	if isPostgres {
		medianExpr := "PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY followers) AS median_followers, " +
			"PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY avg_views) AS median_views, " +
			"PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY (" + priceExpr + ")) AS median_price"
		if err := applyFilters(h.DB.Table("creators")).Select(medianExpr).Scan(&m).Error; err != nil {
			m = medianRow{}
		}
	}

	// 평균 CPV
	var avgCPV *float64
	if s.AvgPrice != nil && s.AvgViews != nil && *s.AvgViews > 0 {
		v := *s.AvgPrice / *s.AvgViews
		avgCPV = &v
	}

	// ── 2. 조회수 히스토그램 ──
	type bucketRow struct {
		Bucket string `gorm:"column:bucket"`
		Count  int64  `gorm:"column:count"`
		MinV   int64  `gorm:"column:min_v"`
	}
	viewsBucketExpr := `CASE
		WHEN avg_views < 1000 THEN '<1K'
		WHEN avg_views < 5000 THEN '1K-5K'
		WHEN avg_views < 10000 THEN '5K-10K'
		WHEN avg_views < 50000 THEN '10K-50K'
		WHEN avg_views < 100000 THEN '50K-100K'
		WHEN avg_views < 500000 THEN '100K-500K'
		WHEN avg_views < 1000000 THEN '500K-1M'
		ELSE '1M+'
	END`
	viewsBucketMinExpr := `CASE
		WHEN avg_views < 1000 THEN 0
		WHEN avg_views < 5000 THEN 1000
		WHEN avg_views < 10000 THEN 5000
		WHEN avg_views < 50000 THEN 10000
		WHEN avg_views < 100000 THEN 50000
		WHEN avg_views < 500000 THEN 100000
		WHEN avg_views < 1000000 THEN 500000
		ELSE 1000000
	END`
	var viewsHist []bucketRow
	if err := applyFilters(h.DB.Table("creators")).
		Select(viewsBucketExpr + " AS bucket, COUNT(*) AS count, MIN(" + viewsBucketMinExpr + ") AS min_v").
		Group(viewsBucketExpr).
		Order("min_v").
		Scan(&viewsHist).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "histogram 집계 실패: " + err.Error()})
		return
	}

	// ── 3. 팔로워 분포 ──
	followerBucketExpr := `CASE
		WHEN followers < 10000 THEN '<1만'
		WHEN followers < 50000 THEN '1-5만'
		WHEN followers < 100000 THEN '5-10만'
		WHEN followers < 500000 THEN '10-50만'
		WHEN followers < 1000000 THEN '50-100만'
		ELSE '100만+'
	END`
	followerBucketMinExpr := `CASE
		WHEN followers < 10000 THEN 0
		WHEN followers < 50000 THEN 10000
		WHEN followers < 100000 THEN 50000
		WHEN followers < 500000 THEN 100000
		WHEN followers < 1000000 THEN 500000
		ELSE 1000000
	END`
	var followerDist []bucketRow
	if err := applyFilters(h.DB.Table("creators")).
		Select(followerBucketExpr + " AS bucket, COUNT(*) AS count, MIN(" + followerBucketMinExpr + ") AS min_v").
		Group(followerBucketExpr).
		Order("min_v").
		Scan(&followerDist).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "follower distribution 실패: " + err.Error()})
		return
	}

	// ── 4. 언어 분포 (Top 10) ──
	type langRow struct {
		Language string `gorm:"column:language" json:"language"`
		Count    int64  `gorm:"column:count" json:"count"`
	}
	var languageDist []langRow
	applyFilters(h.DB.Table("creators")).
		Select("COALESCE(NULLIF(language, ''), '미지정') AS language, COUNT(*) AS count").
		Group("COALESCE(NULLIF(language, ''), '미지정')").
		Order("count DESC").
		Limit(10).
		Scan(&languageDist)

	// ── 5. 팔로워 ↔ 가격 산점도 (최대 1000 샘플) ──
	type scatterRow struct {
		Followers int64 `gorm:"column:followers" json:"followers"`
		Price     int64 `gorm:"column:price" json:"price"`
	}
	var scatter []scatterRow
	scatterQ := applyFilters(h.DB.Table("creators")).
		Select("followers, (" + priceExpr + ")::bigint AS price").
		Where("followers > 0").
		Where("(" + priceExpr + ") IS NOT NULL")
	scatterQ.Limit(1000).Scan(&scatter)

	// ── 6. Top 5 채널 ──
	type topRow struct {
		ID              int    `gorm:"column:id" json:"id"`
		Platform        string `gorm:"column:platform" json:"platform"`
		Handle          string `gorm:"column:handle" json:"handle"`
		Name            string `gorm:"column:name" json:"name"`
		ProfileImage    string `gorm:"column:profile_image" json:"profileImage"`
		Category        string `gorm:"column:category" json:"category"`
		Followers       int64  `gorm:"column:followers" json:"followers"`
		AvgViews        int64  `gorm:"column:avg_views" json:"avgViews"`
		EstimatedAdCost string `gorm:"column:estimated_ad_cost" json:"estimatedAdCost"`
		PlatformURL     string `gorm:"column:platform_url" json:"platformUrl"`
	}
	var top []topRow
	applyFilters(h.DB.Table("creators")).
		Select("id, platform, COALESCE(handle,'') AS handle, COALESCE(name,'') AS name, " +
			"COALESCE(profile_image,'') AS profile_image, COALESCE(category,'') AS category, " +
			"COALESCE(followers,0) AS followers, COALESCE(avg_views,0) AS avg_views, " +
			"COALESCE(estimated_ad_cost,'') AS estimated_ad_cost, " +
			"COALESCE(platform_url,'') AS platform_url").
		Order("avg_views DESC NULLS LAST").
		Limit(5).
		Scan(&top)

	// ── 7. 등급별 CPV (platform 지정 시) ──
	tierAnalytics := []gin.H{}
	if isPostgres && platform != "" && platform != "all" {
		var tiers []models.FollowerTier
		h.DB.Where("platform = ?", platform).Order("sort_order").Find(&tiers)

		if len(tiers) > 0 {
			var caseB strings.Builder
			caseB.WriteString("CASE ")
			for _, t := range tiers {
				if t.MaxFollowers != nil {
					caseB.WriteString(fmt.Sprintf("WHEN followers >= %d AND followers < %d THEN '%s' ",
						t.MinFollowers, *t.MaxFollowers, t.Code))
				} else {
					caseB.WriteString(fmt.Sprintf("WHEN followers >= %d THEN '%s' ",
						t.MinFollowers, t.Code))
				}
			}
			caseB.WriteString("END")
			caseExpr := caseB.String()

			type tierStatRow struct {
				TierCode string   `gorm:"column:tier_code"`
				AvgViews *float64 `gorm:"column:avg_views"`
				AvgPrice *float64 `gorm:"column:avg_price"`
				AvgCPV   *float64 `gorm:"column:avg_cpv"`
				Count    int64    `gorm:"column:count"`
			}
			var tierRows []tierStatRow

			tierSelectExpr := caseExpr + " AS tier_code, " +
				"AVG(avg_views) AS avg_views, " +
				"AVG(" + priceExpr + ") AS avg_price, " +
				"CASE WHEN AVG(avg_views) > 0 THEN AVG(" + priceExpr + ")::float / AVG(avg_views) " +
				"ELSE NULL END AS avg_cpv, " +
				"COUNT(*) AS count"

			applyFilters(h.DB.Table("creators")).
				Where("(" + priceExpr + ") IS NOT NULL AND avg_views > 0").
				Select(tierSelectExpr).
				Group(caseExpr).
				Scan(&tierRows)

			rowMap := map[string]tierStatRow{}
			for _, r := range tierRows {
				rowMap[r.TierCode] = r
			}
			for _, t := range tiers {
				r, ok := rowMap[t.Code]
				entry := gin.H{
					"code":  t.Code,
					"label": t.Label,
				}
				if ok {
					entry["avgViews"] = r.AvgViews
					entry["avgPrice"] = r.AvgPrice
					entry["avgCPV"] = r.AvgCPV
					entry["count"] = r.Count
				} else {
					entry["avgViews"] = nil
					entry["avgPrice"] = nil
					entry["avgCPV"] = nil
					entry["count"] = 0
				}
				tierAnalytics = append(tierAnalytics, entry)
			}
		}
	}

	// ── 응답 조립 ──
	histogramOut := make([]gin.H, 0, len(viewsHist))
	for _, b := range viewsHist {
		histogramOut = append(histogramOut, gin.H{"bucket": b.Bucket, "count": b.Count})
	}
	followerDistOut := make([]gin.H, 0, len(followerDist))
	for _, b := range followerDist {
		followerDistOut = append(followerDistOut, gin.H{"bucket": b.Bucket, "count": b.Count})
	}

	c.JSON(http.StatusOK, gin.H{
		"sampleSize": s.SampleSize,
		"summary": gin.H{
			"avgFollowers":          s.AvgFollowers,
			"medianFollowers":       m.MedianFollowers,
			"avgViews":              s.AvgViews,
			"medianViews":           m.MedianViews,
			"avgEngagementRate":     s.AvgEngagementRate,
			"avgEstimatedAdCost":    s.AvgPrice,
			"medianEstimatedAdCost": m.MedianPrice,
			"avgCPV":                avgCPV,
		},
		"viewsHistogram":       histogramOut,
		"followerDistribution": followerDistOut,
		"languageDistribution": languageDist,
		"followersVsPrice":     scatter,
		"topByViews":           top,
		"tierAnalytics":        tierAnalytics,
	})
}

// endOfString returns POSIX regex end-of-string anchor.
// 별도 헬퍼로 분리한 이유: '$' 문자가 string literal 안에 raw로 들어가면
// 일부 편집 도구가 \E 또는 줄바꿈으로 오해석하는 케이스를 회피하기 위함.
func endOfString() string {
	return "\x24" // ASCII 0x24 = '$'
}
