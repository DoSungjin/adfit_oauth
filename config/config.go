package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

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
	Instance string `yaml:"instance"` // Cloud SQL instance connection name
}

type FirebaseConfig struct {
	ProjectID       string `yaml:"project_id"`
	DatabaseURL     string `yaml:"database_url"`
	CredentialsPath string `yaml:"credentials_path"`
	TestDatabaseID  string `yaml:"test_database_id"` // adtown-test
}

type OAuthConfig struct {
	TikTok    OAuthProvider `yaml:"tiktok"`
	YouTube   OAuthProvider `yaml:"youtube"`
	Instagram OAuthProvider `yaml:"instagram"`
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
	UpdateToken    string   `yaml:"update_token"`
	YouTubeAPIKey  string   `yaml:"youtube_api_key"`
	YouTubeAPIKeys []string `yaml:"youtube_api_keys"` // 여러 개 API Key 로테이션용
	BatchSize      int      `yaml:"batch_size"`
}

type CronConfig struct {
	Enabled   bool              `yaml:"enabled"`
	Schedules map[string]string `yaml:"schedules"`
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
	InstagramEnabled bool `yaml:"instagram_enabled"`
	StatsEnabled     bool `yaml:"stats_enabled"`
	CronEnabled      bool `yaml:"cron_enabled"`
	AnalyticsEnabled bool `yaml:"analytics_enabled"`
}

var (
	Config              *AppConfig
	TikTokOAuth2Config  *oauth2.Config
	YouTubeOAuth2Config *oauth2.Config
)

func LoadConfig(configPath string) error {
	if configPath == "" {
		configPath = "config/app_config.yaml"
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("경로 변환 실패: %v", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("설정 파일 읽기 실패 (%s): %v", absPath, err)
	}

	Config = &AppConfig{}
	if err := yaml.Unmarshal(data, Config); err != nil {
		return fmt.Errorf("YAML 파싱 실패: %v", err)
	}

	applyEnvironmentOverrides()
	applyEnvironmentVariables()
	initOAuth2Configs()

	log.Printf("✅ 설정 로드 완료: %s (환경: %s)", absPath, Config.App.Environment)

	return nil
}

func applyEnvironmentOverrides() {
	env := Config.App.Environment
	if envConfig, exists := Config.Environments[env]; exists {
		if envConfig.App.Debug {
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
		if envConfig.Features.AnalyticsEnabled {
			Config.Features.AnalyticsEnabled = envConfig.Features.AnalyticsEnabled
		}
	}
}

func applyEnvironmentVariables() {

	if port := os.Getenv("PORT"); port != "" {
		Config.App.Port = port
	}
	if env := os.Getenv("ENVIRONMENT"); env != "" {
		Config.App.Environment = env
	}

	if clientKey := os.Getenv("TIKTOK_CLIENT_KEY"); clientKey != "" {
		Config.OAuth.TikTok.ClientID = clientKey
	}
	if clientSecret := os.Getenv("TIKTOK_CLIENT_SECRET"); clientSecret != "" {
		Config.OAuth.TikTok.ClientSecret = clientSecret
	}
	if redirectURI := os.Getenv("TIKTOK_REDIRECT_URI"); redirectURI != "" {
		Config.OAuth.TikTok.RedirectURI = redirectURI
	}

	if clientID := os.Getenv("YOUTUBE_CLIENT_ID"); clientID != "" {
		Config.OAuth.YouTube.ClientID = clientID
	}
	if clientSecret := os.Getenv("YOUTUBE_CLIENT_SECRET"); clientSecret != "" {
		Config.OAuth.YouTube.ClientSecret = clientSecret
	}

	// YouTube Data API Key (Browser Key)
	if apiKey := os.Getenv("YOUTUBE_API_KEY"); apiKey != "" {
		Config.OAuth.YouTube.APIKey = apiKey
		Config.Stats.YouTubeAPIKey = apiKey
	}
	// YouTube API Keys (여러 개, 쉼표로 구분)
	if apiKeys := os.Getenv("YOUTUBE_API_KEYS"); apiKeys != "" {
		Config.Stats.YouTubeAPIKeys = strings.Split(apiKeys, ",")
		for i := range Config.Stats.YouTubeAPIKeys {
			Config.Stats.YouTubeAPIKeys[i] = strings.TrimSpace(Config.Stats.YouTubeAPIKeys[i])
		}
	}

	// Database
	if dbHost := os.Getenv("DB_HOST"); dbHost != "" {
		Config.Database.Host = dbHost
	}
	if dbUser := os.Getenv("DB_USER"); dbUser != "" {
		Config.Database.User = dbUser
	}
	if dbPassword := os.Getenv("DB_PASSWORD"); dbPassword != "" {
		Config.Database.Password = dbPassword
	}
	if dbName := os.Getenv("DB_NAME"); dbName != "" {
		Config.Database.DBName = dbName
	}
	if instance := os.Getenv("CLOUD_SQL_INSTANCE"); instance != "" {
		Config.Database.Instance = instance
	}

	// Instagram
	if appID := os.Getenv("INSTAGRAM_APP_ID"); appID != "" {
		Config.OAuth.Instagram.ClientID = appID
	}
	if appSecret := os.Getenv("INSTAGRAM_APP_SECRET"); appSecret != "" {
		Config.OAuth.Instagram.ClientSecret = appSecret
	}
	if redirectURI := os.Getenv("INSTAGRAM_REDIRECT_URI"); redirectURI != "" {
		Config.OAuth.Instagram.RedirectURI = redirectURI
	}

	if projectID := os.Getenv("FIREBASE_PROJECT_ID"); projectID != "" {
		Config.Firebase.ProjectID = projectID
	}
	if databaseURL := os.Getenv("FIREBASE_DATABASE_URL"); databaseURL != "" {
		Config.Firebase.DatabaseURL = databaseURL
	}
	if credPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credPath != "" {
		Config.Firebase.CredentialsPath = credPath
	}
	if testDBID := os.Getenv("FIRESTORE_TEST_DATABASE_ID"); testDBID != "" {
		Config.Firebase.TestDatabaseID = testDBID
	}

	if token := os.Getenv("STATS_UPDATE_TOKEN"); token != "" {
		Config.Stats.UpdateToken = token
	}

	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		Config.Security.JWTSecret = secret
	}
}

func initOAuth2Configs() {

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

func InitOAuth2() {
	if Config == nil {
		log.Println("⚠️  Config is nil, cannot initialize OAuth2")
		return
	}

	initOAuth2Configs()

	log.Println("✅ OAuth2 설정 완료")
	// log.Printf("  TikTok Client Key: %s", maskString(Config.OAuth.TikTok.ClientID))
	// log.Printf("  TikTok Redirect URI: %s", Config.OAuth.TikTok.RedirectURI)
	// log.Printf("  YouTube Client ID: %s", maskString(Config.OAuth.YouTube.ClientID))
	// log.Printf("  YouTube API Key: %s", maskString(Config.OAuth.YouTube.APIKey))
}

func GetCronSchedule(name string) (string, bool) {
	if Config == nil || !Config.Cron.Enabled {
		return "", false
	}
	schedule, exists := Config.Cron.Schedules[name]
	return schedule, exists
}

func IsFeatureEnabled(feature string) bool {
	if Config == nil {
		return false
	}

	switch strings.ToLower(feature) {
	case "tiktok":
		return Config.Features.TikTokEnabled
	case "youtube":
		return Config.Features.YouTubeEnabled
	case "instagram":
		return Config.Features.InstagramEnabled
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

func GetLogLevel() string {
	if Config == nil {
		return "info"
	}
	return Config.Logging.Level
}

func GetPort() string {
	if Config == nil {
		return "8080"
	}
	return Config.App.Port
}

func IsDebugMode() bool {
	if Config == nil {
		return false
	}
	return Config.App.Debug
}

func maskString(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-4)
}

func GetDatabasePath() string {
	if Config == nil {
		return "adfit.db"
	}
	return Config.Database.Path
}

func GetStatsUpdateToken() string {
	if Config == nil {
		return "adfit-stats-update-token"
	}
	return Config.Stats.UpdateToken
}

func GetYouTubeAPIKey() string {
	if Config == nil {
		return ""
	}
	return Config.Stats.YouTubeAPIKey
}

// GetYouTubeAPIKeys returns all YouTube API keys for rotation
func GetYouTubeAPIKeys() []string {
	// 1. 환경변수에서 직접 먼저 확인 (Config 로드 실패 시에도 작동)
	if apiKeys := os.Getenv("YOUTUBE_API_KEYS"); apiKeys != "" {
		keys := strings.Split(apiKeys, ",")
		for i := range keys {
			keys[i] = strings.TrimSpace(keys[i])
		}
		if len(keys) > 0 && keys[0] != "" {
			return keys
		}
	}

	// 2. Config에서 확인
	if Config != nil {
		if len(Config.Stats.YouTubeAPIKeys) > 0 {
			return Config.Stats.YouTubeAPIKeys
		}
		if Config.Stats.YouTubeAPIKey != "" {
			return []string{Config.Stats.YouTubeAPIKey}
		}
	}

	// 3. 단일 API Key 환경변수
	if apiKey := os.Getenv("YOUTUBE_API_KEY"); apiKey != "" {
		return []string{apiKey}
	}

	return nil
}

func GetStatsBatchSize() int {
	if Config == nil {
		return 50
	}
	return Config.Stats.BatchSize
}

// GetTestDatabaseID returns the test Firestore database ID
func GetTestDatabaseID() string {
	if Config == nil {
		return "adtown-test"
	}
	if Config.Firebase.TestDatabaseID == "" {
		return "adtown-test"
	}
	return Config.Firebase.TestDatabaseID
}
