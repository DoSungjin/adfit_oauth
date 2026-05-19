package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"adfit-oauth/models"
	"adfit-oauth/services"
)

// CategoryHandler - 크리에이터 풀 카테고리/등급/채널 (Cloud SQL)
type CategoryHandler struct {
	DB *gorm.DB
}

func NewCategoryHandler(db *gorm.DB) *CategoryHandler {
	return &CategoryHandler{DB: db}
}

// bumpVersion - category_meta.version +1 (현 Firestore version 동작 보존)
func (h *CategoryHandler) bumpVersion(by string) {
	if by == "" {
		by = "admin"
	}
	h.DB.Model(&models.CategoryMeta{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"version":    gorm.Expr("version + 1"),
		"updated_at": time.Now(),
		"updated_by": by,
	})
}

func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ──────────────────────────────────────────────────────────────
// 1. 트리 조회 (admin + consumer 공용)
// ──────────────────────────────────────────────────────────────

// GetCategoryTree - GET /api/categories , GET /api/admin/categories
// ?platform= : 해당 플랫폼 + 'all' 매핑을 creatorMapping으로 머지 (미지정: 전체)
func (h *CategoryHandler) GetCategoryTree(c *gin.Context) {
	platform := c.Query("platform")

	var primary []models.CategoryPrimary
	var secondary []models.CategorySecondary
	var mappings []models.CategorySecondaryMapping
	var creatorCats []models.CategoryCreator
	var meta models.CategoryMeta

	if err := h.DB.Order("sort_order, code").Find(&primary).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.DB.Order("sort_order, code").Find(&secondary)

	mq := h.DB.Model(&models.CategorySecondaryMapping{})
	if platform != "" {
		mq = mq.Where("platform = ? OR platform = ?", platform, "all")
	}
	mq.Find(&mappings)

	h.DB.Order("creator_count DESC").Find(&creatorCats)
	h.DB.Where("id = ?", 1).First(&meta)

	merged := map[string][]string{}
	byPlatform := map[string]map[string][]string{}
	for _, m := range mappings {
		merged[m.SecondaryCode] = append(merged[m.SecondaryCode], m.CreatorCategory)
		if byPlatform[m.SecondaryCode] == nil {
			byPlatform[m.SecondaryCode] = map[string][]string{}
		}
		byPlatform[m.SecondaryCode][m.Platform] = append(byPlatform[m.SecondaryCode][m.Platform], m.CreatorCategory)
	}

	primaryOut := make([]gin.H, 0, len(primary))
	for _, p := range primary {
		primaryOut = append(primaryOut, gin.H{"code": p.Code, "name": p.Name, "order": p.SortOrder})
	}
	secondaryOut := make([]gin.H, 0, len(secondary))
	for _, s := range secondary {
		secondaryOut = append(secondaryOut, gin.H{
			"code":              s.Code,
			"primaryCode":       s.PrimaryCode,
			"name":              s.Name,
			"order":             s.SortOrder,
			"creatorMapping":    nonNilSlice(merged[s.Code]),
			"mappingByPlatform": byPlatform[s.Code],
		})
	}
	ccOut := make([]gin.H, 0, len(creatorCats))
	for _, cc := range creatorCats {
		ccOut = append(ccOut, gin.H{"name": cc.Name, "count": cc.CreatorCount})
	}

	c.JSON(http.StatusOK, gin.H{
		"version":             meta.Version,
		"primaryCategories":   primaryOut,
		"secondaryCategories": secondaryOut,
		"creatorCategories":   ccOut,
	})
}

// ──────────────────────────────────────────────────────────────
// 2. 1차 분류 CRUD
// ──────────────────────────────────────────────────────────────

type primaryReq struct {
	OldCode string `json:"oldCode"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Order   int    `json:"order"`
}

func (h *CategoryHandler) CreatePrimary(c *gin.Context) {
	var req primaryReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code, name required"})
		return
	}
	row := models.CategoryPrimary{Code: req.Code, Name: req.Name, SortOrder: req.Order}
	if err := h.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "code"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "sort_order"}),
	}).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.bumpVersion("")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *CategoryHandler) UpdatePrimary(c *gin.Context) {
	var req primaryReq
	if err := c.ShouldBindJSON(&req); err != nil || req.OldCode == "" || req.Code == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "oldCode, code, name required"})
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if req.OldCode != req.Code {
			// 코드 변경: 1차/하위 2차 primary_code 동기화
			if err := tx.Model(&models.CategoryPrimary{}).Where("code = ?", req.OldCode).
				Update("code", req.Code).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.CategorySecondary{}).Where("primary_code = ?", req.OldCode).
				Update("primary_code", req.Code).Error; err != nil {
				return err
			}
		}
		return tx.Model(&models.CategoryPrimary{}).Where("code = ?", req.Code).
			Updates(map[string]interface{}{"name": req.Name, "sort_order": req.Order}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.bumpVersion("")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeletePrimary - DELETE /api/admin/categories/primary?code=  (하위 2차 + 매핑 CASCADE)
func (h *CategoryHandler) DeletePrimary(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code required"})
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		var secs []models.CategorySecondary
		tx.Where("primary_code = ?", code).Find(&secs)
		for _, s := range secs {
			if err := tx.Where("secondary_code = ?", s.Code).
				Delete(&models.CategorySecondaryMapping{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("primary_code = ?", code).Delete(&models.CategorySecondary{}).Error; err != nil {
			return err
		}
		return tx.Where("code = ?", code).Delete(&models.CategoryPrimary{}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.bumpVersion("")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ──────────────────────────────────────────────────────────────
// 3. 2차 분류 CRUD + 매핑
// ──────────────────────────────────────────────────────────────

type secondaryReq struct {
	OldCode     string `json:"oldCode"`
	Code        string `json:"code"`
	PrimaryCode string `json:"primaryCode"`
	Name        string `json:"name"`
	Order       int    `json:"order"`
}

func (h *CategoryHandler) CreateSecondary(c *gin.Context) {
	var req secondaryReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" || req.PrimaryCode == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code, primaryCode, name required"})
		return
	}
	row := models.CategorySecondary{Code: req.Code, PrimaryCode: req.PrimaryCode, Name: req.Name, SortOrder: req.Order}
	if err := h.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "code"}},
		DoUpdates: clause.AssignmentColumns([]string{"primary_code", "name", "sort_order"}),
	}).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.bumpVersion("")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *CategoryHandler) UpdateSecondary(c *gin.Context) {
	var req secondaryReq
	if err := c.ShouldBindJSON(&req); err != nil || req.OldCode == "" || req.Code == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "oldCode, code, name required"})
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if req.OldCode != req.Code {
			if err := tx.Model(&models.CategorySecondary{}).Where("code = ?", req.OldCode).
				Update("code", req.Code).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.CategorySecondaryMapping{}).Where("secondary_code = ?", req.OldCode).
				Update("secondary_code", req.Code).Error; err != nil {
				return err
			}
		}
		upd := map[string]interface{}{"name": req.Name, "sort_order": req.Order}
		if req.PrimaryCode != "" {
			upd["primary_code"] = req.PrimaryCode
		}
		return tx.Model(&models.CategorySecondary{}).Where("code = ?", req.Code).Updates(upd).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.bumpVersion("")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *CategoryHandler) DeleteSecondary(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code required"})
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("secondary_code = ?", code).
			Delete(&models.CategorySecondaryMapping{}).Error; err != nil {
			return err
		}
		return tx.Where("code = ?", code).Delete(&models.CategorySecondary{}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.bumpVersion("")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SetSecondaryMapping - PUT /api/admin/categories/secondary/:code/mapping
// body: { platform: "all"|"youtube"|..., creatorCategories: [...] }  (결정 E)
// 해당 (secondary, platform) 매핑 전체 교체
func (h *CategoryHandler) SetSecondaryMapping(c *gin.Context) {
	code := c.Param("code")
	var req struct {
		Platform          string   `json:"platform"`
		CreatorCategories []string `json:"creatorCategories"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code, body required"})
		return
	}
	if req.Platform == "" {
		req.Platform = "all"
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("secondary_code = ? AND platform = ?", code, req.Platform).
			Delete(&models.CategorySecondaryMapping{}).Error; err != nil {
			return err
		}
		rows := make([]models.CategorySecondaryMapping, 0, len(req.CreatorCategories))
		seen := map[string]bool{}
		for _, cat := range req.CreatorCategories {
			cat = strings.TrimSpace(cat)
			if cat == "" || seen[cat] {
				continue
			}
			seen[cat] = true
			rows = append(rows, models.CategorySecondaryMapping{
				SecondaryCode:   code,
				Platform:        req.Platform,
				CreatorCategory: cat,
			})
		}
		if len(rows) > 0 {
			return tx.Create(&rows).Error
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.bumpVersion("")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ──────────────────────────────────────────────────────────────
// 4. 인플루언서 등급 (follower_tier) CRUD  — 결정 F
// ──────────────────────────────────────────────────────────────

// ListFollowerTiers - GET /api/follower-tiers?platform= , /api/admin/follower-tiers?platform=
func (h *CategoryHandler) ListFollowerTiers(c *gin.Context) {
	platform := c.Query("platform")
	q := h.DB.Model(&models.FollowerTier{})
	if platform != "" {
		q = q.Where("platform = ?", platform)
	}
	var tiers []models.FollowerTier
	q.Order("platform, sort_order").Find(&tiers)
	c.JSON(http.StatusOK, gin.H{"data": tiers})
}

type tierReq struct {
	ID           int64  `json:"id"`
	Platform     string `json:"platform"`
	Code         string `json:"code"`
	Label        string `json:"label"`
	MinFollowers int64  `json:"minFollowers"`
	MaxFollowers *int64 `json:"maxFollowers"`
	Order        int    `json:"order"`
}

func (h *CategoryHandler) CreateFollowerTier(c *gin.Context) {
	var req tierReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Platform == "" || req.Code == "" || req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "platform, code, label required"})
		return
	}
	row := models.FollowerTier{
		Platform: req.Platform, Code: req.Code, Label: req.Label,
		MinFollowers: req.MinFollowers, MaxFollowers: req.MaxFollowers, SortOrder: req.Order,
	}
	if err := h.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "platform"}, {Name: "code"}},
		DoUpdates: clause.AssignmentColumns([]string{"label", "min_followers", "max_followers", "sort_order"}),
	}).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": row.ID})
}

func (h *CategoryHandler) UpdateFollowerTier(c *gin.Context) {
	var req tierReq
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	upd := map[string]interface{}{
		"label":         req.Label,
		"min_followers": req.MinFollowers,
		"max_followers": req.MaxFollowers,
		"sort_order":    req.Order,
	}
	if req.Platform != "" {
		upd["platform"] = req.Platform
	}
	if req.Code != "" {
		upd["code"] = req.Code
	}
	if err := h.DB.Model(&models.FollowerTier{}).Where("id = ?", req.ID).Updates(upd).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *CategoryHandler) DeleteFollowerTier(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	if err := h.DB.Where("id = ?", id).Delete(&models.FollowerTier{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ──────────────────────────────────────────────────────────────
// 5. 채널 (channel_master) CRUD  — 결정 A
// ──────────────────────────────────────────────────────────────

// ListChannels - GET /api/admin/channels?secondaryCode=
func (h *CategoryHandler) ListChannels(c *gin.Context) {
	q := h.DB.Model(&models.ChannelMaster{})
	if sc := c.Query("secondaryCode"); sc != "" {
		q = q.Where("secondary_code = ?", sc)
	}
	var chs []models.ChannelMaster
	q.Order("created_at").Find(&chs)
	c.JSON(http.StatusOK, gin.H{"data": chs})
}

type channelReq struct {
	ID            string `json:"id"`
	SecondaryCode string `json:"secondaryCode"`
	Platform      string `json:"platform"`
	AccountID     string `json:"accountId"`
	PasswordEnc   string `json:"passwordEnc"`
	URL           string `json:"url"`
	Description   string `json:"description"`
}

func (h *CategoryHandler) CreateChannel(c *gin.Context) {
	var req channelReq
	if err := c.ShouldBindJSON(&req); err != nil || req.SecondaryCode == "" || req.AccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "secondaryCode, accountId required"})
		return
	}
	id := req.ID
	if id == "" {
		id = strconv.FormatInt(time.Now().UnixMilli(), 10)
	}
	if req.Platform == "" {
		req.Platform = "youtube"
	}
	row := models.ChannelMaster{
		ID: id, SecondaryCode: req.SecondaryCode, Platform: req.Platform,
		AccountID: req.AccountID, PasswordEnc: req.PasswordEnc, URL: req.URL, Description: req.Description,
	}
	if err := h.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"secondary_code", "platform", "account_id", "password_enc", "url", "description", "updated_at",
		}),
	}).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
}

func (h *CategoryHandler) UpdateChannel(c *gin.Context) {
	var req channelReq
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	now := time.Now()
	upd := map[string]interface{}{
		"platform":     req.Platform,
		"account_id":   req.AccountID,
		"password_enc": req.PasswordEnc,
		"url":          req.URL,
		"description":  req.Description,
		"updated_at":   &now,
	}
	if req.SecondaryCode != "" {
		upd["secondary_code"] = req.SecondaryCode
	}
	if err := h.DB.Model(&models.ChannelMaster{}).Where("id = ?", req.ID).Updates(upd).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *CategoryHandler) DeleteChannel(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	if err := h.DB.Where("id = ?", id).Delete(&models.ChannelMaster{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ──────────────────────────────────────────────────────────────
// 6. creatorCategories 일괄 재계산  — 결정 C
// ──────────────────────────────────────────────────────────────

// RecomputeCreatorCategories - POST /api/admin/categories/recompute-creator-categories
// creators.category('\n' 조합)를 토큰 분리해 (name, creator_count) 재집계 (PostgreSQL 전용)
func (h *CategoryHandler) RecomputeCreatorCategories(c *gin.Context) {
	if h.DB.Dialector.Name() != "postgres" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "PostgreSQL(Cloud SQL) 환경에서만 지원됩니다"})
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM category_creator").Error; err != nil {
			return err
		}
		return tx.Exec(`
			INSERT INTO category_creator (name, creator_count)
			SELECT btrim(tok) AS name, COUNT(DISTINCT id) AS cnt
			FROM creators, unnest(string_to_array(category, E'\n')) AS tok
			WHERE category IS NOT NULL AND btrim(tok) <> ''
			GROUP BY btrim(tok)
		`).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var n int64
	h.DB.Model(&models.CategoryCreator{}).Count(&n)
	c.JSON(http.StatusOK, gin.H{"success": true, "tokenCount": n})
}

// ──────────────────────────────────────────────────────────────
// 7. Firestore → Cloud SQL 일회성 마이그레이션 (멱등)  — 결정 D
// ──────────────────────────────────────────────────────────────

func fsString(m map[string]interface{}, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func fsInt(m map[string]interface{}, k string) int {
	if v, ok := m[k]; ok && v != nil {
		switch t := v.(type) {
		case int64:
			return int(t)
		case float64:
			return int(t)
		case int:
			return t
		}
	}
	return 0
}

func fsStringSlice(v interface{}) []string {
	out := []string{}
	if arr, ok := v.([]interface{}); ok {
		for _, e := range arr {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

// MigrateFromFirestore - POST /api/admin/categories/migrate-from-firestore (멱등)
// config/category_mapping + config/channel_master → Cloud SQL
func (h *CategoryHandler) MigrateFromFirestore(c *gin.Context) {
	clients := services.GetFirestoreClients()
	if clients == nil || clients.GetDefaultDB() == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Firestore 클라이언트 없음"})
		return
	}
	ctx := context.Background()
	fs := clients.GetDefaultDB()

	result := gin.H{}

	// 1. category_mapping
	cmDoc, err := fs.Collection("config").Doc("category_mapping").Get(ctx)
	if err == nil && cmDoc.Exists() {
		data := cmDoc.Data()

		txErr := h.DB.Transaction(func(tx *gorm.DB) error {
			// primary
			if arr, ok := data["primaryCategories"].([]interface{}); ok {
				for _, e := range arr {
					m, _ := e.(map[string]interface{})
					if m == nil {
						continue
					}
					row := models.CategoryPrimary{
						Code: fsString(m, "code"), Name: fsString(m, "name"), SortOrder: fsInt(m, "order"),
					}
					if row.Code == "" {
						continue
					}
					if err := tx.Clauses(clause.OnConflict{
						Columns:   []clause.Column{{Name: "code"}},
						DoUpdates: clause.AssignmentColumns([]string{"name", "sort_order"}),
					}).Create(&row).Error; err != nil {
						return err
					}
				}
			}
			// secondary + mapping(platform='all')
			if arr, ok := data["secondaryCategories"].([]interface{}); ok {
				for _, e := range arr {
					m, _ := e.(map[string]interface{})
					if m == nil {
						continue
					}
					code := fsString(m, "code")
					if code == "" {
						continue
					}
					sec := models.CategorySecondary{
						Code: code, PrimaryCode: fsString(m, "primaryCode"),
						Name: fsString(m, "name"), SortOrder: fsInt(m, "order"),
					}
					if err := tx.Clauses(clause.OnConflict{
						Columns:   []clause.Column{{Name: "code"}},
						DoUpdates: clause.AssignmentColumns([]string{"primary_code", "name", "sort_order"}),
					}).Create(&sec).Error; err != nil {
						return err
					}
					if err := tx.Where("secondary_code = ? AND platform = ?", code, "all").
						Delete(&models.CategorySecondaryMapping{}).Error; err != nil {
						return err
					}
					for _, cat := range fsStringSlice(m["creatorMapping"]) {
						if err := tx.Create(&models.CategorySecondaryMapping{
							SecondaryCode: code, Platform: "all", CreatorCategory: cat,
						}).Error; err != nil {
							return err
						}
					}
				}
			}
			// creatorCategories (초기값; 이후 recompute로 갱신)
			if arr, ok := data["creatorCategories"].([]interface{}); ok {
				for _, e := range arr {
					m, _ := e.(map[string]interface{})
					if m == nil {
						continue
					}
					name := fsString(m, "name")
					if name == "" {
						continue
					}
					row := models.CategoryCreator{Name: name, CreatorCount: fsInt(m, "count")}
					if err := tx.Clauses(clause.OnConflict{
						Columns:   []clause.Column{{Name: "name"}},
						DoUpdates: clause.AssignmentColumns([]string{"creator_count"}),
					}).Create(&row).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if txErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "category_mapping 이전 실패: " + txErr.Error()})
			return
		}
		result["category_mapping"] = "ok"
	} else {
		result["category_mapping"] = "skip(없음)"
	}

	// 2. channel_master
	chDoc, err := fs.Collection("config").Doc("channel_master").Get(ctx)
	if err == nil && chDoc.Exists() {
		if arr, ok := chDoc.Data()["channels"].([]interface{}); ok {
			cnt := 0
			for _, e := range arr {
				m, _ := e.(map[string]interface{})
				if m == nil {
					continue
				}
				id := fsString(m, "id")
				if id == "" {
					continue
				}
				row := models.ChannelMaster{
					ID:            id,
					SecondaryCode: fsString(m, "secondaryCode"),
					Platform:      fsString(m, "platform"),
					AccountID:     fsString(m, "accountId"),
					PasswordEnc:   fsString(m, "password"), // Firestore 필드명: password(base64)
					URL:           fsString(m, "url"),
					Description:   fsString(m, "description"),
				}
				if err := h.DB.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "id"}},
					DoUpdates: clause.AssignmentColumns([]string{
						"secondary_code", "platform", "account_id", "password_enc", "url", "description",
					}),
				}).Create(&row).Error; err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "channel_master 이전 실패: " + err.Error()})
					return
				}
				cnt++
			}
			result["channel_master"] = fmt.Sprintf("ok(%d)", cnt)
		}
	} else {
		result["channel_master"] = "skip(없음)"
	}

	h.bumpVersion("migration")
	c.JSON(http.StatusOK, gin.H{"success": true, "result": result})
}
