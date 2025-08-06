# Google Cloud Run 배포 가이드 (Windows)

## 사전 준비사항

1. **Google Cloud SDK 설치**
   - https://cloud.google.com/sdk/docs/install 에서 다운로드
   - 설치 후 PowerShell에서: `gcloud init`

2. **Docker Desktop 설치**
   - https://www.docker.com/products/docker-desktop 에서 다운로드
   - Docker Desktop 실행 확인

3. **프로젝트 설정**
   ```powershell
   gcloud config set project posted-app-c4ff5
   ```

4. **인증 설정**
   ```powershell
   gcloud auth login
   gcloud auth configure-docker
   ```

## 자동 배포 (권장)

```powershell
cd D:\Budit\posted_app\adfit-oauth-server
.\deploy.bat
```

## 수동 배포 (단계별)

### 1단계: Docker 이미지 빌드

```powershell
cd D:\Budit\posted_app\adfit-oauth-server

# Docker 이미지 빌드
docker build -t gcr.io/posted-app-c4ff5/adfit-oauth-server .
```

### 2단계: GCR에 푸시

```powershell
# Google Container Registry에 푸시
docker push gcr.io/posted-app-c4ff5/adfit-oauth-server
```

### 3단계: Cloud Run 배포

```powershell
# Cloud Run에 배포
gcloud run deploy adfit-oauth-server `
  --image gcr.io/posted-app-c4ff5/adfit-oauth-server `
  --platform managed `
  --region asia-northeast3 `
  --allow-unauthenticated `
  --port 8080 `
  --memory 512Mi `
  --max-instances 10 `
  --set-env-vars "PORT=8080" `
  --project posted-app-c4ff5
```

## 환경 변수 설정

Cloud Run에서 환경 변수 업데이트:

```powershell
gcloud run services update adfit-oauth-server `
  --update-env-vars `
    TIKTOK_CLIENT_KEY=sbaw680qp988gxobwf,`
    TIKTOK_CLIENT_SECRET=bBYlj1jwSgj7uy9whnz8Wsdb7pmb6nt8,`
    TIKTOK_REDIRECT_URI=https://adfit-oauth-server-520676604613.asia-northeast3.run.app/api/tiktok/callback,`
    JWT_SECRET=my_super_secret_jwt_2025_first_trial_go_and_get_it,`
    CLIENT_REDIRECT_URL=https://adfit.ai/auth/callback/tiktok `
  --region asia-northeast3 `
  --project posted-app-c4ff5
```

## 로그 확인

```powershell
# 실시간 로그 확인
gcloud run logs tail adfit-oauth-server --region asia-northeast3

# 최근 50개 로그 확인
gcloud run logs read --service adfit-oauth-server --limit 50 --region asia-northeast3
```

## 서비스 URL

배포 후 서비스 URL:
```
https://adfit-oauth-server-520676604613.asia-northeast3.run.app
```

## 테스트 엔드포인트

### Health Check
```powershell
curl https://adfit-oauth-server-520676604613.asia-northeast3.run.app/health
```

### TikTok OAuth 시작
```
https://adfit-oauth-server-520676604613.asia-northeast3.run.app/api/tiktok/auth
```

## 문제 해결

### 1. Docker 빌드 실패
- Docker Desktop이 실행 중인지 확인
- Dockerfile이 있는지 확인

### 2. GCR 푸시 실패
```powershell
# Docker 인증 재설정
gcloud auth configure-docker
```

### 3. Cloud Run 배포 실패
```powershell
# 프로젝트 확인
gcloud config get-value project

# API 활성화
gcloud services enable run.googleapis.com
gcloud services enable containerregistry.googleapis.com
```

### 4. 권한 오류
```powershell
# Cloud Run Admin 권한 부여
gcloud projects add-iam-policy-binding posted-app-c4ff5 `
  --member="user:YOUR_EMAIL@gmail.com" `
  --role="roles/run.admin"
```

## 로컬 테스트

배포 전 로컬에서 테스트:

```powershell
cd D:\Budit\posted_app\adfit-oauth-server
.\run_local.bat
```

브라우저에서 확인:
- http://localhost:8080/health
- http://localhost:8080/api/tiktok/auth

## 데이터베이스 초기화

필요시 데이터베이스 리셋:

```powershell
# adfit.db 파일 삭제
Remove-Item adfit.db -Force

# 서버 재시작 (자동으로 새 DB 생성)
.\run_local.bat
```
