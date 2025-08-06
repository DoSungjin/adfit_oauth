@echo off
REM Quick Deploy using gcloud builds submit
echo ===================================
echo Quick Deploy to Cloud Run
echo ===================================
echo.

SET PROJECT_ID=posted-app-c4ff5
SET SERVICE_NAME=adfit-oauth-server
SET REGION=asia-northeast3

echo Deploying directly using Cloud Build...
echo This may take 3-5 minutes...
echo.

gcloud run deploy %SERVICE_NAME% ^
  --source . ^
  --platform managed ^
  --region %REGION% ^
  --allow-unauthenticated ^
  --port 8080 ^
  --memory 512Mi ^
  --max-instances 10 ^
  --set-env-vars "PORT=8080,TIKTOK_CLIENT_KEY=sbaw680qp988gxobwf,TIKTOK_CLIENT_SECRET=bBYlj1jwSgj7uy9whnz8Wsdb7pmb6nt8,TIKTOK_REDIRECT_URI=https://adfit-oauth-server-520676604613.asia-northeast3.run.app/api/tiktok/callback,JWT_SECRET=my_super_secret_jwt_2025_first_trial_go_and_get_it,CLIENT_REDIRECT_URL=https://adfit.ai/auth/callback/tiktok" ^
  --project %PROJECT_ID%

if %ERRORLEVEL% EQU 0 (
    echo.
    echo ===================================
    echo Deploy Complete!
    echo Service URL: https://%SERVICE_NAME%-520676604613.%REGION%.run.app
    echo ===================================
) else (
    echo.
    echo ===================================
    echo Deploy Failed!
    echo Please check the error messages above.
    echo ===================================
)

pause
