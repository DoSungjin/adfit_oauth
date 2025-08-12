@echo off
echo ==========================================
echo   AdFit OAuth Server - Cloud Run Deploy
echo ==========================================
echo.

REM 프로젝트 설정
set PROJECT_ID=posted-app-c4ff5
set SERVICE_NAME=adfit-oauth-server
set REGION=asia-northeast3

echo [1/4] Setting up gcloud project...
gcloud config set project %PROJECT_ID%

echo.
echo [2/4] Building and deploying to Cloud Run...
echo This may take a few minutes...

REM Cloud Run 배포 (소스에서 직접 빌드)
gcloud run deploy %SERVICE_NAME% ^
  --source . ^
  --region %REGION% ^
  --platform managed ^
  --allow-unauthenticated ^
  --project %PROJECT_ID% ^
  --memory 512Mi ^
  --cpu 1 ^
  --timeout 60 ^
  --max-instances 100 ^
  --set-env-vars "TIKTOK_CLIENT_KEY=sbaw680qp988gxobwf" ^
  --set-env-vars "TIKTOK_CLIENT_SECRET=bBYlj1jwSgj7uy9whnz8Wsdb7pmb6nt8" ^
  --set-env-vars "YOUTUBE_CLIENT_SECRET=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" ^
  --set-env-vars "JWT_SECRET=my_super_secret_jwt_2025_first_trial_go_and_get_it" ^
  --set-env-vars "ENV=production" ^
  --set-env-vars "PORT=8080"

echo.
echo [3/4] Getting service URL...
for /f "tokens=2" %%a in ('gcloud run services describe %SERVICE_NAME% --region %REGION% --format "value(status.url)"') do set SERVICE_URL=%%a

echo.
echo [4/4] Deployment Complete!
echo ==========================================
echo Service URL: %SERVICE_URL%
echo ==========================================
echo.
echo Testing endpoints:
echo - Health Check: %SERVICE_URL%/health
echo - YouTube Auth: %SERVICE_URL%/api/youtube/auth
echo - TikTok Auth: %SERVICE_URL%/api/tiktok/auth
echo.
pause
