@echo off
echo Deploying to Cloud Run...
gcloud run deploy adfit-oauth-server --source . --region asia-northeast3 --allow-unauthenticated --project posted-app-c4ff5
pause
