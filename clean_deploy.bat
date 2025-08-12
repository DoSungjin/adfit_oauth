@echo off
echo ========================================
echo AdFit OAuth Server Deploy (Clean Build)
echo ========================================
echo.

echo [1/3] Cleaning and fixing go.sum...
cd /d D:\Budit\posted_app\adfit-oauth-server
del go.sum 2>nul
go mod tidy

echo.
echo [2/3] Installing YouTube Analytics package...
go get google.golang.org/api/youtubeanalytics/v2

echo.
echo [3/3] Deploying to Cloud Run...
echo (If asked for password, enter your Google Cloud password)
gcloud run deploy adfit-oauth-server --source . --region asia-northeast3 --allow-unauthenticated --project posted-app-c4ff5 --env-vars-file env.yaml

echo.
echo ========================================
echo ✅ Deploy Complete!
echo 📍 Service URL: https://adfit-oauth-server-520676604613.asia-northeast3.run.app
echo ========================================
pause
