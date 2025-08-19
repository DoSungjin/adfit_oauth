package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"golang.org/x/oauth2"
)

// AppConfig 전체 애플리케이션 설정
type AppConfig struct {
	App          AppSettings          `yaml:"app"`
	Database     DatabaseConfig       `yaml:"database"`
	Firebase     FirebaseConfig       `yaml:"firebase"`
	OAuth        OAuthConfig          `yaml:"oauth"`
	CORS         CORSConfig           `yaml:"cors"`
	Stats        StatsConfig          `yaml:"stats"`
	Cron         CronConfig           `yaml:"cron"`
	Logging      LoggingConfig        `yaml:"logging"`
	Security     SecurityConfig       `yaml:"security"`
	Features     FeatureFlags         `yaml:"features"`
	Environments map[string]AppConfig `yaml:"environments"`
}

type AppSettings struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Environment string `yaml:"environment"`
	Port        string `yaml:"port"`
	Debug       bool   `yaml:"debug"`
}

type DatabaseConfig struct {
	Type     string `yaml:"type"`
	Path     string `yaml:"path"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
}

type FirebaseConfig struct {
	ProjectID       string `yaml:"project_id"`
	CredentialsPath string `yaml:"credentials_path"`
}

type OAuthConfig struct {
	TikTok  OAuthProvider `yaml:"tiktok"`
	YouTube OAuthProvider `yaml:"youtube"`
}

type OAuthProvider struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURI  string   `yaml:"redirect_uri"`
	Scopes       []string `yaml:"scopes"`
	AuthURL      string   `yaml:"auth_url"`
	TokenURL     string   `yaml:"token_url"`
	APIKey       string   `yaml:"api_key"`
}

type CORSConfig struct {
	AllowedOrigins   []string `yaml:"allowed_origins"`
	AllowedMethods   []string `yaml:"allowed_methods"`
	AllowedHeaders   []string `yaml:"allowed_headers"`
	ExposeHeaders    []string `yaml:"expose_headers"`
	AllowCredentials bool     `yaml:"allow_credentials"`
}

type StatsConfig struct {
	UpdateToken   string `yaml:"update_token"`
	YouTubeAPIKey string `yaml:"youtube_api_key"`
	BatchSize     int    `yaml:"batch_size"`
}

type CronConfig struct {
	Enabled   bool               `yaml:"enabled"`
	Schedules map[string]string  `yaml:"schedules"`
}

type LoggingConfig struct {
	Level    string `yaml:"level"`
	Format   string `yaml:"format"`
	Output   string `yaml:"output"`
	FilePath string `yaml:"file_path"`
}

type SecurityConfig struct {
	JWTSecret string `yaml:"jwt_secret"`
	TokenTTL  string `yaml:"token_ttl"`
	RateLimit struct {
		RequestsPerMinute int `yaml:"requests_per_minute"`
		Burst             int `yaml:"burst"`
	} `yaml:"rate_limit"`
}

type FeatureFlags struct {
	TikTokEnabled    bool `yaml:"tiktok_enabled"`
	YouTubeEnabled   bool `yaml:"youtube_enabled"`
	StatsEnabled     bool `yaml:"stats_enabled"`
	CronEnabled      bool `yaml:"cron_enabled"`
	AnalyticsEnabled bool `yaml:"analytics_enabled"`
}

// 전역 설정 변수
var (
	Config           *AppConfig
	TikTokOAuth2Config *oauth2.Config
	YouTubeOAuth2Config *oauth2.Config
)

// LoadConfig YAML 설정 파일 로드
func LoadConfig(configPath string) error {
	if configPath == "" {
		configPath = "config/app_config.yaml"
	}

	// 절대 경로 변환
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("경로 변환 실패: %v", err)
	}

	// 파일 읽기
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("설정 파일 읽기 실패 (%s): %v", absPath, err)
	}

	// YAML 파싱
	Config = &AppConfig{}
	if err := yaml.Unmarshal(data, Config); err != nil {
		return fmt.Errorf("YAML 파싱 실패: %v", err)
	}

	// 환경별 설정 오버라이드
	applyEnvironmentOverrides()

	// 환경변수 적용
	applyEnvironmentVariables()

	// OAuth 설정 초기화
	initializeOAuthConfigs()

	log.Printf("✅ 설정 로드 완료: %s (환경: %s)", absPath, Config.App.Environment)
	return nil
}

// 환경별 설정 오버라이드
func applyEnvironmentOverrides() {
	env := Config.App.Environment
	if envConfig, exists := Config.Environments[env]; exists {
		// 간단한 필드들 오버라이드
		if envConfig.App.Debug != Config.App.Debug {
			Config.App.Debug = envConfig.App.Debug
		}
		if envConfig.App.Port != "" {
			Config.App.Port = envConfig.App.Port
		}
		if envConfig.Logging.Level != "" {
			Config.Logging.Level = envConfig.Logging.Level
		}
		if envConfig.Logging.Format != "" {
			Config.Logging.Format = envConfig.Logging.Format
		}
		if len(envConfig.CORS.AllowedOrigins) > 0 {
			Config.CORS.AllowedOrigins = envConfig.CORS.AllowedOrigins
		}
		// Feature flags 오버라이드
		if envConfig.Features.AnalyticsEnabled != Config.Features.AnalyticsEnabled {
			Config.Features.AnalyticsEnabled = envConfig.Features.AnalyticsEnabled
		}
	}
}

// 환경변수 적용
func applyEnvironmentVariables() {
	// 앱 설정
	if port := os.Getenv("PORT"); port != "" {
		Config.App.Port = port
	}
	if env := os.Getenv("ENVIRONMENT"); env != "" {
		Config.App.Environment = env
	}

	// TikTok OAuth 설정
	if clientKey := os.Getenv("TIKTOK_CLIENT_KEY"); clientKey != "" {
		Config.OAuth.TikTok.ClientID = clientKey
	}
	if clientSecret := os.Getenv("TIKTOK_CLIENT_SECRET"); clientSecret != "" {
		Config.OAuth.TikTok.ClientSecret = clientSecret
	}
	if redirectURI := os.Getenv("TIKTOK_REDIRECT_URI"); redirectURI != "" {
		Config.OAuth.TikTok.RedirectURI = redirectURI
	}
	
	// YouTube OAuth 설정
	if clientID := os.Getenv("YOUTUBE_CLIENT_ID"); clientID != "" {
		Config.OAuth.YouTube.ClientID = clientID
	}
	if clientSecret := os.Getenv("YOUTUBE_CLIENT_SECRET"); clientSecret != "" {
		Config.OAuth.YouTube.ClientSecret = clientSecret
	}
	
	// YouTube Data API Key (Browser Key)
	if apiKey := os.Getenv("YOUTUBE_API_KEY"); apiKey != "" {
		Config.OAuth.YouTube.APIKey = apiKey
		Config.Stats.YouTubeAPIKey = apiKey  // Stats에도 설정
	}

	// Firebase 설정
	if projectID := os.Getenv("FIREBASE_PROJECT_ID"); projectID != "" {
		Config.Firebase.ProjectID = projectID
	}
	if credPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credPath != "" {
		Config.Firebase.CredentialsPath = credPath
	}

	// Stats 설정
	if token := os.Getenv("STATS_UPDATE_TOKEN"); token != "" {
		Config.Stats.UpdateToken = token
	}

	// Security 설정
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		Config.Security.JWTSecret = secret
	}
}

// OAuth 설정 초기화
func initializeOAuthConfigs() {
	// TikTok OAuth2 설정
	TikTokOAuth2Config = &oauth2.Config{
		ClientID:     Config.OAuth.TikTok.ClientID,
		ClientSecret: Config.OAuth.TikTok.ClientSecret,
		RedirectURL:  Config.OAuth.TikTok.RedirectURI,
		Scopes:       Config.OAuth.TikTok.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  Config.OAuth.TikTok.AuthURL,
			TokenURL: Config.OAuth.TikTok.TokenURL,
		},
	}

	// YouTube OAuth2 설정
	YouTubeOAuth2Config = &oauth2.Config{
		ClientID:     Config.OAuth.YouTube.ClientID,
		ClientSecret: Config.OAuth.YouTube.ClientSecret,
		RedirectURL:  Config.OAuth.YouTube.RedirectURI,
		Scopes:       Config.OAuth.YouTube.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
	}
}

// InitOAuth2 기존 호환성을 위한 함수
func InitOAuth2() {
	if Config == nil {
		log.Println("⚠️ 설정이 로드되지 않았습니다. LoadConfig()를 먼저 호출하세요.")
		return
	}

	log.Println("🔧 OAuth2 설정 완료:")
	log.Printf("  TikTok Client Key: %s", maskString(Config.OAuth.TikTok.ClientID))
	log.Printf("  TikTok Redirect URI: %s", Config.OAuth.TikTok.RedirectURI)
	log.Printf("  YouTube Client ID: %s", maskString(Config.OAuth.YouTube.ClientID))
	log.Printf("  YouTube API Key: %s", maskString(Config.OAuth.YouTube.APIKey))
}

// GetCronSchedule 크론 스케줄 가져오기
func GetCronSchedule(name string) (string, bool) {
	if Config == nil || !Config.Cron.Enabled {
		return "", false
	}
	schedule, exists := Config.Cron.Schedules[name]
	return schedule, exists
}

// IsFeatureEnabled 기능 플래그 확인
func IsFeatureEnabled(feature string) bool {
	if Config == nil {
		return false
	}
	
	switch strings.ToLower(feature) {
	case "tiktok":
		return Config.Features.TikTokEnabled
	case "youtube":
		return Config.Features.YouTubeEnabled
	case "stats":
		return Config.Features.StatsEnabled
	case "cron":
		return Config.Features.CronEnabled
	case "analytics":
		return Config.Features.AnalyticsEnabled
	default:
		return false
	}
}

// GetLogLevel 로그 레벨 가져오기
func GetLogLevel() string {
	if Config == nil {
		return "info"
	}
	return Config.Logging.Level
}

// GetPort 포트 번호 가져오기
func GetPort() string {
	if Config == nil {
		return "8080"
	}
	return Config.App.Port
}

// IsDebugMode 디버그 모드 확인
func IsDebugMode() bool {
	if Config == nil {
		return false
	}
	return Config.App.Debug
}

// 문자열 마스킹 (보안)
func maskString(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-4)
}

// GetDatabasePath SQLite 데이터베이스 경로
func GetDatabasePath() string {
	if Config == nil {
		return "adfit.db"
	}
	return Config.Database.Path
}

// GetStatsUpdateToken 통계 업데이트 토큰
func GetStatsUpdateToken() string {
	if Config == nil {
		return "adfit-stats-update-token"
	}
	return Config.Stats.UpdateToken
}

// GetYouTubeAPIKey YouTube API 키
func GetYouTubeAPIKey() string {
	if Config == nil {
		return ""
	}
	return Config.Stats.YouTubeAPIKey
}

// GetStatsBatchSize 통계 배치 크기
func GetStatsBatchSize() int {
	if Config == nil {
		return 50
	}
	return Config.Stats.BatchSize
}
