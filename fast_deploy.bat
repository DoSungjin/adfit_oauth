@echo off
echo [1/4] Installing packages...
cd /d D:\Budit\posted_app\adfit-oauth-server
go get google.golang.org/api/youtubeanalytics/v2 2>nul

echo [2/4] Building Docker image...
docker build -t gcr.io/posted-app-c4ff5/adfit-oauth-server . >nul 2>&1

echo [3/4] Pushing to GCR...
docker push gcr.io/posted-app-c4ff5/adfit-oauth-server >nul 2>&1

echo [4/4] Deploying to Cloud Run...
gcloud run deploy adfit-oauth-server --image gcr.io/posted-app-c4ff5/adfit-oauth-server --platform managed --region asia-northeast3 --allow-unauthenticated --port 8080 --memory 512Mi --max-instances 10 --set-env-vars "PORT=8080" --project posted-app-c4ff5 --quiet

echo.
echo ✅ Deploy Complete!
echo 📍 URL: https://adfit-oauth-server-520676604613.asia-northeast3.run.app
echo.
pause
