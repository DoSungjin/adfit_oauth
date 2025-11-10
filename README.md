# AdFit OAuth Server

TikTok OAuth 2.0 인증을 처리하는 Go 서버입니다.

## 🚀 기능

- TikTok OAuth 2.0 v2 인증
- JWT 토큰 발급 및 관리
- 사용자 정보 조회
- 비디오 목록 조회
- 토큰 갱신 및 로그아웃

## 📋 사전 요구사항

- Go 1.21 이상
- Docker (선택사항)
- Google Cloud SDK (Cloud Run 배포시)
- TikTok Developer 계정

## 🔧 설치

### 1. 저장소 클론

```bash
git clone https://github.com/DoSungjin/adfit_oauth.git
cd adfit_oauth
```

### 2. 환경 변수 설정

```bash
cp .env.example .env
# .env 파일을 열어서 실제 값으로 수정
```

### 3. 의존성 설치

```bash
go mod download
```

## 💻 로컬 실행

```bash
# Windows
.\run_local.bat

# Mac/Linux
go run main.go
```

서버가 http://localhost:8080 에서 실행됩니다.

## 🚀 배포

### Google Cloud Run

```bash
# Windows
.\deploy_simple.bat

# Mac/Linux
gcloud run deploy adfit-oauth-server \
  --source . \
  --region asia-northeast3 \
  --allow-unauthenticated \
  --project YOUR_PROJECT_ID
```

### Docker

```bash
# 빌드
docker build -t adfit-oauth-server .

# 실행
docker run -p 8080:8080 --env-file .env adfit-oauth-server
```

## 📡 API 엔드포인트

### 공개 엔드포인트

- `GET /health` - 헬스 체크
- `GET /api/tiktok/auth` - TikTok OAuth 시작
- `GET /api/tiktok/callback` - OAuth 콜백 처리
- `POST /api/tiktok/token` - 토큰 교환

### 인증 필요 엔드포인트

- `GET /api/tiktok/user` - 사용자 정보 조회
- `GET /api/tiktok/videos` - 비디오 목록 조회
- `POST /api/tiktok/refresh` - 토큰 갱신
- `POST /api/tiktok/logout` - 로그아웃

## 🔑 환경 변수

| 변수명 | 설명 | 예시 |
|--------|------|------|
| TIKTOK_CLIENT_KEY | TikTok 앱 Client Key | sbaw680qp988gxobwf |
| TIKTOK_CLIENT_SECRET | TikTok 앱 Client Secret | your_secret_here |
| TIKTOK_REDIRECT_URI | OAuth 콜백 URI | https://your-server.run.app/api/tiktok/callback |
| JWT_SECRET | JWT 서명 키 | your_jwt_secret |
| CLIENT_REDIRECT_URL | 클라이언트 앱 콜백 URL | https://your-app.com/auth/callback/tiktok |
| PORT | 서버 포트 | 8080 |

## 📝 TikTok 앱 설정

1. [TikTok Developer Portal](https://developers.tiktok.com) 접속
2. 앱 생성 또는 선택
3. OAuth 설정에서 Redirect URI 추가:
   - `https://your-server-url/api/tiktok/callback`
4. 필요한 Scope 설정:
   - `user.info.basic` (기본)
   - `video.list` (비디오 목록)

## 🧪 테스트

```bash
# 헬스 체크
curl http://localhost:8080/health

# OAuth 플로우 시작 (브라우저에서 열기)
http://localhost:8080/api/tiktok/auth
```

## 📊 로그 확인

### Cloud Run
```bash
gcloud run logs tail adfit-oauth-server --region asia-northeast3
```

### Docker
```bash
docker logs -f container_name
```

## 🤝 기여

Pull Request는 언제나 환영합니다!

## 📄 라이센스

MIT License

## 📞 문의

이슈가 있으면 GitHub Issues에 등록해주세요.
