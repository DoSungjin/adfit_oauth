package models

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ──────────────────────────────────────────────────────────────
// 내 크리에이터 풀 관리 (사용자별 리스트 + 다중 소속)
// 제약:
//   · 사용자당 리스트 최대 5개
//   · 리스트당 멤버 최대 100명
//   · 리스트 이름 최대 20자
//   · 기본 리스트(IsDefault) 삭제 금지 / 이름 변경 허용
// 정규화:
//   · 멤버는 creators.id 만 참조 (이름·팔로워 등 복사 X)
//   · 조회 시 creators 와 JOIN 해서 최신 정보 사용
// ──────────────────────────────────────────────────────────────

// CreatorSaveList - 사용자별 저장 리스트
type CreatorSaveList struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	UserID    string    `gorm:"column:user_id;size:128;not null;uniqueIndex:ux_save_list_user_name,priority:1" json:"userId"`
	Name      string    `gorm:"column:name;size:20;not null;uniqueIndex:ux_save_list_user_name,priority:2" json:"name"`
	IsDefault bool      `gorm:"column:is_default;not null;default:false" json:"isDefault"`
	SortOrder int       `gorm:"column:sort_order;not null;default:0" json:"sortOrder"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

func (CreatorSaveList) TableName() string { return "creator_save_list" }

// CreatorSaveListMember - 리스트 ↔ 크리에이터 조인 (다중 소속)
// PK: (list_id, creator_id) → 같은 리스트에 같은 크리에이터 중복 방지
// 한 크리에이터가 여러 리스트에 동시 소속 가능
type CreatorSaveListMember struct {
	ListID    int64     `gorm:"primaryKey;column:list_id;not null;index:idx_member_list" json:"listId"`
	CreatorID int       `gorm:"primaryKey;column:creator_id;not null;index:idx_member_creator" json:"creatorId"`
	Memo      string    `gorm:"column:memo;type:text" json:"memo"`
	AddedAt   time.Time `gorm:"column:added_at;autoCreateTime" json:"addedAt"`
}

func (CreatorSaveListMember) TableName() string { return "creator_save_list_member" }

// ──────────────────────────────────────────────────────────────
// creators 테이블 보조 컬럼 (CSV 임포트로 생성된 테이블에 ALTER 추가)
// · channel_id    : 플랫폼 native ID (YouTube UCxxx / IG user_id / TikTok sec_uid)
// · source        : 'seed' | 'recommend' | 'migrated' | 'manual'
// · discovered_at : 최초 발견 시각 (추천으로 신규 추가된 행)
// · last_seen_at  : 가장 최근 검색에 등장한 시각
// ──────────────────────────────────────────────────────────────

// MigrateCreatorAuxColumns - creators 보조 컬럼 멱등 추가
// PostgreSQL (운영) / SQLite (로컬 폴백) 둘 다 지원
func MigrateCreatorAuxColumns(db *gorm.DB) error {
	dialect := db.Dialector.Name()

	switch dialect {
	case "postgres":
		// PG: ADD COLUMN IF NOT EXISTS 지원 (PG 9.6+)
		stmts := []string{
			`ALTER TABLE creators ADD COLUMN IF NOT EXISTS channel_id    TEXT`,
			`ALTER TABLE creators ADD COLUMN IF NOT EXISTS source        TEXT DEFAULT 'seed'`,
			`ALTER TABLE creators ADD COLUMN IF NOT EXISTS discovered_at TIMESTAMP`,
			`ALTER TABLE creators ADD COLUMN IF NOT EXISTS last_seen_at  TIMESTAMP`,
			`CREATE INDEX IF NOT EXISTS idx_creators_channel ON creators(platform, channel_id) WHERE channel_id IS NOT NULL`,
		}
		for _, sql := range stmts {
			if err := db.Exec(sql).Error; err != nil {
				return fmt.Errorf("creators aux 마이그레이션 실패 (%s): %w", sql, err)
			}
		}
	case "sqlite":
		// SQLite: IF NOT EXISTS 미지원 → 시도 후 에러 무시 (이미 존재 시 에러 발생)
		stmts := []string{
			`ALTER TABLE creators ADD COLUMN channel_id    TEXT`,
			`ALTER TABLE creators ADD COLUMN source        TEXT DEFAULT 'seed'`,
			`ALTER TABLE creators ADD COLUMN discovered_at DATETIME`,
			`ALTER TABLE creators ADD COLUMN last_seen_at  DATETIME`,
		}
		for _, sql := range stmts {
			db.Exec(sql) // 멱등을 위해 에러 무시
		}
	}
	return nil
}
