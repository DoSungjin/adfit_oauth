package models

import (
    "time"
    "gorm.io/gorm"
)

type UserToken struct {
    gorm.Model
    UserID       string    `gorm:"index;not null"`  // uniqueIndex 제거 (platform과 함께 사용)
    Platform     string    `gorm:"index;not null;default:'tiktok'"` // 'tiktok' or 'youtube'
    AccessToken  string    `gorm:"not null"`
    RefreshToken string    
    TokenType    string    
    ExpiresAt    time.Time
    Scope        string
    OpenID       string    // TikTok user open_id
    ChannelID    string    // YouTube channel ID
    UpdatedAt    time.Time
}

type TikTokUser struct {
    OpenID      string `json:"open_id"`
    UnionID     string `json:"union_id"`
    DisplayName string `json:"display_name"`
    AvatarURL   string `json:"avatar_url"`
}

type TikTokVideo struct {
    ID              string    `json:"id"`
    Title           string    `json:"title"`
    Description     string    `json:"description"`
    CreateTime      int64     `json:"create_time"`
    CoverImageURL   string    `json:"cover_image_url"`
    ShareURL        string    `json:"share_url"`
    ViewCount       int       `json:"view_count"`
    LikeCount       int       `json:"like_count"`
    CommentCount    int       `json:"comment_count"`
    ShareCount      int       `json:"share_count"`
}
