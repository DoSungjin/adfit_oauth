package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"adfit-oauth/models"
)

// ──────────────────────────────────────────────────────────────
// 내 크리에이터 풀 - 리스트 + 멤버 관리
//   리스트 CRUD
//     GET    /api/creators/lists                              사용자 리스트 + 멤버 수 (lazy 기본 생성 + legacy 마이그레이션)
//     POST   /api/creators/lists                              리스트 생성 (5개 한도, 20자)
//     PUT    /api/creators/lists/:id                          이름 변경
//     DELETE /api/creators/lists/:id                          삭제 (기본 리스트 금지)
//   멤버 관리
//     GET    /api/creators/lists/:id/members                  리스트별 크리에이터 (creators JOIN, 최신 정보)
//     POST   /api/creators/lists/:id/members                  단건 추가 (100명 한도)
//     POST   /api/creators/lists/:id/members/bulk-add         다건 추가 (추천 결과 통합)
//     DELETE /api/creators/lists/:id/members/:creatorId       단건 제거
// ──────────────────────────────────────────────────────────────

const (
	MaxListsPerUser   = 5
	MaxListNameLen    = 20
	MaxMembersPerList = 100
	DefaultListName   = "내 크리에이터"
)

type SaveListHandler struct {
	DB *gorm.DB
}

func NewSaveListHandler(db *gorm.DB) *SaveListHandler {
	return &SaveListHandler{DB: db}
}

// SaveListView - 리스트 응답 (멤버 수 포함)
type SaveListView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	SortOrder int    `json:"sortOrder"`
	Count     int64  `json:"count"`
}

// MemberView - 멤버 응답 (creators JOIN, 빈 필드는 빈 문자열)
type MemberView struct {
	CreatorID    int    `gorm:"column:creator_id" json:"creatorId"`
	Platform     string `gorm:"column:platform" json:"platform"`
	Handle       string `gorm:"column:handle" json:"handle"`
	Name         string `gorm:"column:name" json:"name"`
	ProfileImage string `gorm:"column:profile_image" json:"profileImage"`
	Category     string `gorm:"column:category" json:"category"`
	Followers    int64  `gorm:"column:followers" json:"followers"`
	PlatformURL  string `gorm:"column:platform_url" json:"platformUrl"`
	ChannelID    string `gorm:"column:channel_id" json:"channelId"`
	Memo         string `gorm:"column:memo" json:"memo"`
	AddedAt      string `gorm:"column:added_at" json:"addedAt"`
}

// ============================================================
// 공통 헬퍼
// ============================================================

// ensureDefaultList - 기본 리스트 없으면 생성 (멱등)
// 반환: (리스트, 방금 생성됐는지, 에러)
func (h *SaveListHandler) ensureDefaultList(userID string) (models.CreatorSaveList, bool, error) {
	var existing models.CreatorSaveList
	err := h.DB.Where("user_id = ? AND is_default = ?", userID, true).First(&existing).Error
	if err == nil {
		return existing, false, nil
	}
	if err != gorm.ErrRecordNotFound {
		return models.CreatorSaveList{}, false, err
	}
	list := models.CreatorSaveList{
		UserID:    userID,
		Name:      DefaultListName,
		IsDefault: true,
		SortOrder: 0,
	}
	if err := h.DB.Create(&list).Error; err != nil {
		return models.CreatorSaveList{}, false, err
	}
	return list, true, nil
}

// migrateLegacySaved - creator_saved → creator_save_list_member 1회 이관
// 멱등: ON CONFLICT DO NOTHING으로 중복 INSERT 방지. 매칭 실패 시 creators에 source='migrated'로 신규 추가.
func (h *SaveListHandler) migrateLegacySaved(userID string, defaultListID int64) error {
	type legacyRow struct {
		Platform     string `gorm:"column:platform"`
		Handle       string `gorm:"column:handle"`
		Name         string `gorm:"column:name"`
		ProfileImage string `gorm:"column:profile_image"`
		Category     string `gorm:"column:category"`
		Followers    int64  `gorm:"column:followers"`
		PlatformURL  string `gorm:"column:platform_url"`
		Memo         string `gorm:"column:memo"`
	}

	var rows []legacyRow
	err := h.DB.Raw(`
		SELECT
		  COALESCE(platform,'')      AS platform,
		  COALESCE(handle,'')        AS handle,
		  COALESCE(name,'')          AS name,
		  COALESCE(profile_image,'') AS profile_image,
		  COALESCE(category,'')      AS category,
		  COALESCE(followers,0)      AS followers,
		  COALESCE(platform_url,'')  AS platform_url,
		  COALESCE(memo,'')          AS memo
		FROM creator_saved
		WHERE user_id = ?
	`, userID).Scan(&rows).Error
	if err != nil {
		// 테이블이 없거나(SQLite 신규 환경) 다른 일시적 에러는 무시 — 마이그레이션은 best-effort
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	log.Printf("🔄 legacy creator_saved 마이그레이션: user=%s, count=%d", userID, len(rows))

	type idOnly struct {
		ID int `gorm:"column:id"`
	}
	now := time.Now()

	for _, row := range rows {
		if row.Platform == "" || row.Handle == "" {
			continue
		}

		// 1) creators 매칭
		var found idOnly
		err := h.DB.Table("creators").
			Select("id").
			Where("platform = ? AND handle = ?", row.Platform, row.Handle).
			Take(&found).Error

		creatorID := found.ID

		// 2) 매칭 실패 → creators에 신규 추가 (source='migrated')
		if err == gorm.ErrRecordNotFound || creatorID == 0 {
			insertRow := map[string]interface{}{
				"platform":      row.Platform,
				"handle":        row.Handle,
				"name":          row.Name,
				"profile_image": row.ProfileImage,
				"category":      row.Category,
				"followers":     row.Followers,
				"platform_url":  row.PlatformURL,
				"source":        "migrated",
				"discovered_at": now,
			}
			if err := h.DB.Table("creators").Create(insertRow).Error; err != nil {
				log.Printf("⚠️ creators INSERT 실패 (platform=%s, handle=%s): %v", row.Platform, row.Handle, err)
				continue
			}
			// 신규 생성된 id 재조회 (Create map은 ID를 반환하지 않을 수 있음)
			var newID idOnly
			if e := h.DB.Table("creators").
				Select("id").
				Where("platform = ? AND handle = ?", row.Platform, row.Handle).
				Take(&newID).Error; e != nil || newID.ID == 0 {
				continue
			}
			creatorID = newID.ID
		} else if err != nil {
			log.Printf("⚠️ creators 조회 실패 (platform=%s, handle=%s): %v", row.Platform, row.Handle, err)
			continue
		}

		// 3) 멤버 INSERT (PK 중복 무시 - 멱등)
		member := models.CreatorSaveListMember{
			ListID:    defaultListID,
			CreatorID: creatorID,
			Memo:      row.Memo,
		}
		if err := h.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&member).Error; err != nil {
			log.Printf("⚠️ 멤버 INSERT 실패 (list=%d, creator=%d): %v", defaultListID, creatorID, err)
		}
	}
	return nil
}

// isUniqueViolation - PostgreSQL/SQLite 중복 위반 감지
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "UNIQUE constraint") ||
		strings.Contains(msg, "violates unique")
}

// validateListName - 이름 검증 (trim + utf8 rune count)
func validateListName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("리스트 이름이 비어있습니다")
	}
	if utf8.RuneCountInString(name) > MaxListNameLen {
		return "", fmt.Errorf("리스트 이름은 최대 %d자까지 가능합니다", MaxListNameLen)
	}
	return name, nil
}

// requireOwnedList - 사용자가 소유한 리스트 가져오기 (실패 시 응답 처리 + nil 반환)
func (h *SaveListHandler) requireOwnedList(c *gin.Context, userID string, listID int64) *models.CreatorSaveList {
	var list models.CreatorSaveList
	err := h.DB.Where("id = ? AND user_id = ?", listID, userID).First(&list).Error
	if err == gorm.ErrRecordNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "리스트를 찾을 수 없습니다"})
		return nil
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil
	}
	return &list
}

// ============================================================
// 리스트 CRUD
// ============================================================

// GetSaveLists - GET /api/creators/lists
// lazy 기본 리스트 생성 + legacy creator_saved 1회 마이그레이션 진입점
func (h *SaveListHandler) GetSaveLists(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	defaultList, created, err := h.ensureDefaultList(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if created {
		// best-effort: 실패해도 응답은 정상
		if mErr := h.migrateLegacySaved(userID, defaultList.ID); mErr != nil {
			log.Printf("⚠️ legacy 마이그레이션 실패: %v", mErr)
		}
	}

	var views []SaveListView
	err = h.DB.Raw(`
		SELECT l.id, l.name, l.is_default, l.sort_order,
		       COALESCE(COUNT(m.creator_id), 0) AS count
		FROM creator_save_list l
		LEFT JOIN creator_save_list_member m ON m.list_id = l.id
		WHERE l.user_id = ?
		GROUP BY l.id, l.name, l.is_default, l.sort_order
		ORDER BY l.is_default DESC, l.sort_order ASC, l.id ASC
	`, userID).Scan(&views).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"lists":             views,
		"max":               MaxListsPerUser,
		"maxMembersPerList": MaxMembersPerList,
	})
}

// CreateSaveList - POST /api/creators/lists  Body: {"name": "..."}
func (h *SaveListHandler) CreateSaveList(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	name, vErr := validateListName(req.Name)
	if vErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": vErr.Error()})
		return
	}

	// 5개 한도 (Count 세션 분리)
	var count int64
	if err := h.DB.Model(&models.CreatorSaveList{}).
		Session(&gorm.Session{}).
		Where("user_id = ?", userID).
		Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if count >= MaxListsPerUser {
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("리스트는 최대 %d개까지 생성 가능합니다", MaxListsPerUser),
			"code":  "list_limit_exceeded",
		})
		return
	}

	list := models.CreatorSaveList{
		UserID:    userID,
		Name:      name,
		IsDefault: false,
		SortOrder: int(count),
	}
	if err := h.DB.Create(&list).Error; err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "같은 이름의 리스트가 이미 있습니다",
				"code":  "duplicate_name",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"list": list})
}

// UpdateSaveList - PUT /api/creators/lists/:id  Body: {"name": "..."}
// 기본 리스트도 이름 변경 허용
func (h *SaveListHandler) UpdateSaveList(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 리스트 ID"})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	name, vErr := validateListName(req.Name)
	if vErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": vErr.Error()})
		return
	}

	result := h.DB.Model(&models.CreatorSaveList{}).
		Where("id = ? AND user_id = ?", listID, userID).
		Update("name", name)

	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "같은 이름의 리스트가 이미 있습니다",
				"code":  "duplicate_name",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "리스트를 찾을 수 없습니다"})
		return
	}

	var list models.CreatorSaveList
	h.DB.First(&list, listID)
	c.JSON(http.StatusOK, gin.H{"list": list})
}

// DeleteSaveList - DELETE /api/creators/lists/:id
// 기본 리스트 삭제 금지. 멤버는 트랜잭션 내 cascade.
func (h *SaveListHandler) DeleteSaveList(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 리스트 ID"})
		return
	}

	list := h.requireOwnedList(c, userID, listID)
	if list == nil {
		return
	}

	if list.IsDefault {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "기본 리스트는 삭제할 수 없습니다",
			"code":  "default_list_undeletable",
		})
		return
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("list_id = ?", listID).Delete(&models.CreatorSaveListMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(list).Error
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ============================================================
// 멤버 관리
// ============================================================

// GetMembers - GET /api/creators/lists/:id/members
// creators JOIN으로 최신 정보 반환. NULL은 빈 문자열/0으로 직렬화.
func (h *SaveListHandler) GetMembers(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 리스트 ID"})
		return
	}

	if h.requireOwnedList(c, userID, listID) == nil {
		return
	}

	var members []MemberView
	err = h.DB.Raw(`
		SELECT
		  m.creator_id                    AS creator_id,
		  c.platform                      AS platform,
		  COALESCE(c.handle,'')           AS handle,
		  COALESCE(c.name,'')             AS name,
		  COALESCE(c.profile_image,'')    AS profile_image,
		  COALESCE(c.category,'')         AS category,
		  COALESCE(c.followers,0)         AS followers,
		  COALESCE(c.platform_url,'')     AS platform_url,
		  COALESCE(c.channel_id,'')       AS channel_id,
		  COALESCE(m.memo,'')             AS memo,
		  COALESCE(CAST(m.added_at AS TEXT),'') AS added_at
		FROM creator_save_list_member m
		INNER JOIN creators c ON c.id = m.creator_id
		WHERE m.list_id = ?
		ORDER BY m.added_at DESC, m.creator_id DESC
	`, listID).Scan(&members).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  members,
		"total": len(members),
	})
}

// countMembers - 리스트의 현재 멤버 수
func (h *SaveListHandler) countMembers(listID int64) (int64, error) {
	var n int64
	err := h.DB.Model(&models.CreatorSaveListMember{}).
		Session(&gorm.Session{}).
		Where("list_id = ?", listID).
		Count(&n).Error
	return n, err
}

// creatorExists - creators 테이블에 해당 id가 존재하는지
func (h *SaveListHandler) creatorExists(creatorID int) bool {
	var n int64
	h.DB.Table("creators").Where("id = ?", creatorID).Count(&n)
	return n > 0
}

// AddMember - POST /api/creators/lists/:id/members  Body: {"creatorId": 123, "memo": "..."}
func (h *SaveListHandler) AddMember(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 리스트 ID"})
		return
	}

	if h.requireOwnedList(c, userID, listID) == nil {
		return
	}

	var req struct {
		CreatorID int    `json:"creatorId" binding:"required"`
		Memo      string `json:"memo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !h.creatorExists(req.CreatorID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "크리에이터를 찾을 수 없습니다"})
		return
	}

	count, err := h.countMembers(listID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if count >= MaxMembersPerList {
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("리스트당 최대 %d명까지 추가 가능합니다", MaxMembersPerList),
			"code":  "member_limit_exceeded",
		})
		return
	}

	member := models.CreatorSaveListMember{
		ListID:    listID,
		CreatorID: req.CreatorID,
		Memo:      req.Memo,
	}
	// PK 중복은 멱등 처리 (이미 있으면 그대로 OK)
	if err := h.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"member": member})
}

// BulkAddMembers - POST /api/creators/lists/:id/members/bulk-add
// Body: {"creatorIds": [1,2,3,...], "memo": "..."}
// 추천 결과 통합용. 100명 한도 내에서 가능한 만큼 추가, 초과분은 skipped 반환.
func (h *SaveListHandler) BulkAddMembers(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 리스트 ID"})
		return
	}

	if h.requireOwnedList(c, userID, listID) == nil {
		return
	}

	var req struct {
		CreatorIDs []int  `json:"creatorIds" binding:"required"`
		Memo       string `json:"memo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 중복 제거
	seen := map[int]bool{}
	uniq := make([]int, 0, len(req.CreatorIDs))
	for _, id := range req.CreatorIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}

	count, err := h.countMembers(listID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	remaining := int(int64(MaxMembersPerList) - count)
	if remaining <= 0 {
		c.JSON(http.StatusConflict, gin.H{
			"added":        0,
			"skipped":      len(uniq),
			"limitReached": true,
			"error":        fmt.Sprintf("이 리스트는 이미 %d명에 도달했습니다", MaxMembersPerList),
		})
		return
	}

	toAdd := uniq
	overflow := 0
	if len(uniq) > remaining {
		toAdd = uniq[:remaining]
		overflow = len(uniq) - remaining
	}

	// 배치 INSERT (PK 중복 무시)
	if len(toAdd) > 0 {
		members := make([]models.CreatorSaveListMember, 0, len(toAdd))
		for _, cid := range toAdd {
			members = append(members, models.CreatorSaveListMember{
				ListID:    listID,
				CreatorID: cid,
				Memo:      req.Memo,
			})
		}
		if err := h.DB.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&members).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	// 실제 추가된 수 (중복 무시로 인해 toAdd보다 적을 수 있음) — 재조회로 확정
	newCount, _ := h.countMembers(listID)
	added := int(newCount - count)
	if added < 0 {
		added = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"added":         added,
		"skipped":       len(uniq) - added,
		"limitReached":  overflow > 0,
		"overflowCount": overflow,
		"listTotal":     newCount,
	})
}

// RemoveMember - DELETE /api/creators/lists/:id/members/:creatorId
func (h *SaveListHandler) RemoveMember(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 리스트 ID"})
		return
	}
	creatorID, err := strconv.Atoi(c.Param("creatorId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 크리에이터 ID"})
		return
	}

	if h.requireOwnedList(c, userID, listID) == nil {
		return
	}

	result := h.DB.Where("list_id = ? AND creator_id = ?", listID, creatorID).
		Delete(&models.CreatorSaveListMember{})

	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "멤버를 찾을 수 없습니다"})
		return
	}
	c.Status(http.StatusNoContent)
}
