@echo off
echo ========================================
echo AdFit OAuth Server Deploy (Source Build)
echo ========================================
echo.

echo [1/2] Installing YouTube Analytics package...
cd /d D:\Budit\posted_app\adfit-oauth-server
go get google.golang.org/api/youtubeanalytics/v2

echo.
echo [2/2] Deploying to Cloud Run...
gcloud run deploy adfit-oauth-server --source . --region asia-northeast3 --allow-unauthenticated --project posted-app-c4ff5 --env-vars-file env.yaml

echo.
echo ========================================
echo ✅ Deploy Complete!
echo 📍 Service URL: https://adfit-oauth-server-520676604613.asia-northeast3.run.app
echo ========================================
pause
