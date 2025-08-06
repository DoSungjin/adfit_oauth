package config

import (
    "fmt"
    "os"
    "golang.org/x/oauth2"
)

var TikTokOAuth2Config *oauth2.Config

func InitOAuth2() {
    TikTokOAuth2Config = &oauth2.Config{
        ClientID:     os.Getenv("TIKTOK_CLIENT_KEY"),
        ClientSecret: os.Getenv("TIKTOK_CLIENT_SECRET"),
        Endpoint: oauth2.Endpoint{
            AuthURL:  "https://www.tiktok.com/v2/auth/authorize",
            TokenURL: "https://open.tiktokapis.com/v2/oauth/token/",
        },
        RedirectURL: os.Getenv("TIKTOK_REDIRECT_URI"),
        Scopes:      []string{"user.info.basic", "video.list"},
    }
    
    // 디버깅을 위한 환경변수 확인
    fmt.Println("🔧 OAuth2 Config Initialized:")
    fmt.Printf("  Client Key: %s\n", os.Getenv("TIKTOK_CLIENT_KEY"))
    fmt.Printf("  Redirect URI: %s\n", os.Getenv("TIKTOK_REDIRECT_URI"))
}
