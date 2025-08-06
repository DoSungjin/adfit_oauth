@echo off
echo ===================================
echo Checking AdFit OAuth Server Status
echo ===================================
echo.

SET SERVICE_NAME=adfit-oauth-server
SET REGION=asia-northeast3

echo Fetching service info...
gcloud run services describe %SERVICE_NAME% --region %REGION% --format "value(status.url)"

echo.
echo Testing health endpoint...
curl https://adfit-oauth-server-520676604613.asia-northeast3.run.app/health

echo.
echo.
echo Recent logs (last 10 entries):
echo ===================================
gcloud run logs read --service %SERVICE_NAME% --region %REGION% --limit 10

echo.
pause
