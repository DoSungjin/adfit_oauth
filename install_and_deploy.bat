@echo off
echo Installing YouTube Analytics API package...
cd /d D:\Budit\posted_app\adfit-oauth-server
go get google.golang.org/api/youtubeanalytics/v2
echo Package installed successfully!
echo.
echo Deploying to Google Cloud Run...
call deploy.bat
