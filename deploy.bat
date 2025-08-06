@echo off
REM Cloud Run Deploy Script for Windows
SET PROJECT_ID=posted-app-c4ff5
SET SERVICE_NAME=adfit-oauth-server
SET REGION=asia-northeast3
SET IMAGE_NAME=gcr.io/%PROJECT_ID%/%SERVICE_NAME%

echo ===================================
echo Starting AdFit OAuth Server Deploy...
echo ===================================

REM 1. Build Docker image
echo.
echo Building Docker image...
docker build -t %IMAGE_NAME% .
if %ERRORLEVEL% NEQ 0 (
    echo ERROR: Docker build failed!
    exit /b 1
)

REM 2. Push to Google Container Registry
echo.
echo Pushing image to GCR...
docker push %IMAGE_NAME%
if %ERRORLEVEL% NEQ 0 (
    echo ERROR: Image push failed!
    exit /b 1
)

REM 3. Deploy to Cloud Run
echo.
echo Deploying to Cloud Run...
gcloud run deploy %SERVICE_NAME% ^
  --image %IMAGE_NAME% ^
  --platform managed ^
  --region %REGION% ^
  --allow-unauthenticated ^
  --port 8080 ^
  --memory 512Mi ^
  --max-instances 10 ^
  --set-env-vars "PORT=8080" ^
  --project %PROJECT_ID%

if %ERRORLEVEL% NEQ 0 (
    echo ERROR: Cloud Run deploy failed!
    exit /b 1
)

echo.
echo ===================================
echo Deploy Complete!
echo Service URL: https://%SERVICE_NAME%-520676604613.%REGION%.run.app
echo ===================================
pause
