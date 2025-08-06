# PowerShell 배포 스크립트
# AdFit OAuth Server to Google Cloud Run

$PROJECT_ID = "posted-app-c4ff5"
$SERVICE_NAME = "adfit-oauth-server"
$REGION = "asia-northeast3"
$IMAGE_NAME = "gcr.io/$PROJECT_ID/$SERVICE_NAME"

Write-Host "===================================" -ForegroundColor Cyan
Write-Host "AdFit OAuth Server Deploy Script" -ForegroundColor Cyan
Write-Host "===================================" -ForegroundColor Cyan
Write-Host ""

# 1. Docker 빌드
Write-Host "Step 1: Building Docker image..." -ForegroundColor Yellow
docker build -t $IMAGE_NAME .
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Docker build failed!" -ForegroundColor Red
    exit 1
}
Write-Host "✓ Docker build successful" -ForegroundColor Green
Write-Host ""

# 2. GCR 푸시
Write-Host "Step 2: Pushing to Google Container Registry..." -ForegroundColor Yellow
docker push $IMAGE_NAME
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Push to GCR failed!" -ForegroundColor Red
    exit 1
}
Write-Host "✓ Push successful" -ForegroundColor Green
Write-Host ""

# 3. Cloud Run 배포
Write-Host "Step 3: Deploying to Cloud Run..." -ForegroundColor Yellow
gcloud run deploy $SERVICE_NAME `
    --image $IMAGE_NAME `
    --platform managed `
    --region $REGION `
    --allow-unauthenticated `
    --port 8080 `
    --memory 512Mi `
    --max-instances 10 `
    --set-env-vars "PORT=8080" `
    --project $PROJECT_ID

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Cloud Run deployment failed!" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "===================================" -ForegroundColor Green
Write-Host "✓ Deployment Complete!" -ForegroundColor Green
Write-Host "===================================" -ForegroundColor Green
Write-Host ""
Write-Host "Service URL:" -ForegroundColor Cyan
Write-Host "https://$SERVICE_NAME-520676604613.$REGION.run.app" -ForegroundColor White
Write-Host ""
Write-Host "Test endpoints:" -ForegroundColor Cyan
Write-Host "  Health: https://$SERVICE_NAME-520676604613.$REGION.run.app/health" -ForegroundColor White
Write-Host "  Auth:   https://$SERVICE_NAME-520676604613.$REGION.run.app/api/tiktok/auth" -ForegroundColor White
Write-Host ""
Write-Host "Press any key to exit..."
$null = $Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")
