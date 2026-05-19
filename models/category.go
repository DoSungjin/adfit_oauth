package models

import (
	"time"

	"gorm.io/gorm"
)

// ──────────────────────────────────────────────────────────────
// 크리에이터 풀 카테고리 / 등급 / 채널 (Firestore → Cloud SQL 이전)
// 결정: A(채널 SQL) · C(category_creator+배치) · E(플랫폼별 매핑) · F(플랫폼별 등급)
// ──────────────────────────────────────────────────────────────

// CategoryPrimary - 1차 분류
type CategoryPrimary struct {
	Code      string `gorm:"primaryKey;column:code;size:20" json:"code"`
	Name      string `gorm:"column:name;size:100;not null" json:"name"`
	SortOrder int    `gorm:"column:sort_order;not null;default:0" json:"sortOrder"`
}

func (CategoryPrimary) TableName() string { return "category_primary" }

// CategorySecondary - 2차 분류
type CategorySecondary struct {
	Code        string `gorm:"primaryKey;column:code;size:20" json:"code"` // 예: "01-07"
	PrimaryCode string `gorm:"column:primary_code;size:20;not null;index" json:"primaryCode"`
	Name        string `gorm:"column:name;size:100;not null" json:"name"`
	SortOrder   int    `gorm:"column:sort_order;not null;default:0" json:"sortOrder"`
}

func (CategorySecondary) TableName() string { return "category_secondary" }

// CategorySecondaryMapping - 2차 ↔ 원본 크리에이터 분류 매핑 (결정 E: 플랫폼별)
// platform: 'all'=전 플랫폼 공통, 'youtube'|'instagram'|'tiktok'=개별
type CategorySecondaryMapping struct {
	ID              int64  `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	SecondaryCode   string `gorm:"column:secondary_code;size:20;not null;uniqueIndex:ux_csm,priority:1" json:"secondaryCode"`
	Platform        string `gorm:"column:platform;size:20;not null;default:'all';uniqueIndex:ux_csm,priority:2" json:"platform"`
	CreatorCategory string `gorm:"column:creator_category;size:100;not null;uniqueIndex:ux_csm,priority:3" json:"creatorCategory"`
}

func (CategorySecondaryMapping) TableName() string { return "category_secondary_mapping" }

// CategoryCreator - 원본 크리에이터 분류 목록 (현 creatorCategories)
// 결정 C: 초기 시드 후 CreatorCount는 admin 버튼/저빈도 배치로 일괄 재계산 (라이브 ×)
type CategoryCreator struct {
	Name         string `gorm:"primaryKey;column:name;size:100" json:"name"`
	CreatorCount int    `gorm:"column:creator_count;not null;default:0" json:"creatorCount"`
}

func (CategoryCreator) TableName() string { return "category_creator" }

// CategoryMeta - 버전/메타 (단일 행, id=1). admin UI v배지 동작 유지용
type CategoryMeta struct {
	ID        int       `gorm:"primaryKey;autoIncrement:false;column:id" json:"id"`
	Version   int       `gorm:"column:version;not null;default:1" json:"version"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	UpdatedBy string    `gorm:"column:updated_by;size:50" json:"updatedBy"`
}

func (CategoryMeta) TableName() string { return "category_meta" }

// FollowerTier - 인플루언서 등급 (결정 F: 플랫폼별, admin 설정, 추가 가능)
// 구간: MinFollowers <= followers < MaxFollowers (MaxFollowers nil = 상한 없음)
type FollowerTier struct {
	ID           int64  `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Platform     string `gorm:"column:platform;size:20;not null;uniqueIndex:ux_ft,priority:1" json:"platform"`
	Code         string `gorm:"column:code;size:30;not null;uniqueIndex:ux_ft,priority:2" json:"code"`
	Label        string `gorm:"column:label;size:50;not null" json:"label"`
	MinFollowers int64  `gorm:"column:min_followers;not null;default:0" json:"minFollowers"`
	MaxFollowers *int64 `gorm:"column:max_followers" json:"maxFollowers"` // nil = 무제한
	SortOrder    int    `gorm:"column:sort_order;not null;default:0" json:"sortOrder"`
}

func (FollowerTier) TableName() string { return "follower_tier" }

// ChannelMaster - 채널 (결정 A: Cloud SQL 정식 편입)
type ChannelMaster struct {
	ID            string     `gorm:"primaryKey;column:id;size:40" json:"id"`
	SecondaryCode string     `gorm:"column:secondary_code;size:20;not null;index" json:"secondaryCode"`
	Platform      string     `gorm:"column:platform;size:20;not null" json:"platform"`
	AccountID     string     `gorm:"column:account_id;size:200;not null" json:"accountId"`
	PasswordEnc   string     `gorm:"column:password_enc" json:"passwordEnc"` // base64(현행). 보안 개선 별도 이슈
	URL           string     `gorm:"column:url" json:"url"`
	Description   string     `gorm:"column:description" json:"description"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt     *time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

func (ChannelMaster) TableName() string { return "channel_master" }

// ──────────────────────────────────────────────────────────────
// 기본 시드 (멱등) — Phase 1: AutoMigrate 직후 호출
// ──────────────────────────────────────────────────────────────

func i64(v int64) *int64 { return &v }

// 기본 등급 정의 (§3-1)
//   YouTube · TikTok : nano/micro/macro/mega (4단계)
//   Instagram        : nano/micro-l/micro-h/macro-l/macro-h/mega (6단계)
func defaultFollowerTiers() []FollowerTier {
	base4 := func(platform string) []FollowerTier {
		return []FollowerTier{
			{Platform: platform, Code: "nano", Label: "1만 미만", MinFollowers: 0, MaxFollowers: i64(10000), SortOrder: 1},
			{Platform: platform, Code: "micro", Label: "1만~10만", MinFollowers: 10000, MaxFollowers: i64(100000), SortOrder: 2},
			{Platform: platform, Code: "macro", Label: "10만~100만", MinFollowers: 100000, MaxFollowers: i64(1000000), SortOrder: 3},
			{Platform: platform, Code: "mega", Label: "100만 이상", MinFollowers: 1000000, MaxFollowers: nil, SortOrder: 4},
		}
	}

	tiers := []FollowerTier{}
	tiers = append(tiers, base4("youtube")...)
	tiers = append(tiers, base4("tiktok")...)
	tiers = append(tiers, []FollowerTier{
		{Platform: "instagram", Code: "nano", Label: "3만 미만", MinFollowers: 0, MaxFollowers: i64(30000), SortOrder: 1},
		{Platform: "instagram", Code: "micro-l", Label: "3만~5만", MinFollowers: 30000, MaxFollowers: i64(50000), SortOrder: 2},
		{Platform: "instagram", Code: "micro-h", Label: "5만~10만", MinFollowers: 50000, MaxFollowers: i64(100000), SortOrder: 3},
		{Platform: "instagram", Code: "macro-l", Label: "10만~30만", MinFollowers: 100000, MaxFollowers: i64(300000), SortOrder: 4},
		{Platform: "instagram", Code: "macro-h", Label: "30만~100만", MinFollowers: 300000, MaxFollowers: i64(1000000), SortOrder: 5},
		{Platform: "instagram", Code: "mega", Label: "100만 이상", MinFollowers: 1000000, MaxFollowers: nil, SortOrder: 6},
	}...)
	return tiers
}

// SeedCategoryDefaults - 멱등 시드.
// follower_tier: (platform, code) 없을 때만 삽입 / category_meta: id=1 없을 때만 생성.
func SeedCategoryDefaults(db *gorm.DB) error {
	for _, t := range defaultFollowerTiers() {
		tier := t // 루프 변수 캡처 회피
		if err := db.Where(&FollowerTier{Platform: tier.Platform, Code: tier.Code}).
			Attrs(&tier).
			FirstOrCreate(&FollowerTier{}).Error; err != nil {
			return err
		}
	}

	meta := CategoryMeta{ID: 1}
	if err := db.Where(&CategoryMeta{ID: 1}).
		Attrs(&CategoryMeta{ID: 1, Version: 1, UpdatedBy: "system"}).
		FirstOrCreate(&meta).Error; err != nil {
		return err
	}

	return nil
}
