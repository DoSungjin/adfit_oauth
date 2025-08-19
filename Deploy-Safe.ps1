# AdFit OAuth Server 안전한 배포 스크립트
# Google Secret Manager를 사용한 시크릿 관리

Write-Host "====================================" -ForegroundColor Cyan
Write-Host "AdFit OAuth Server 안전한 배포" -ForegroundColor Cyan
Write-Host "====================================" -ForegroundColor Cyan
Write-Host ""

# 프로젝트 ID 설정
$PROJECT_ID = "posted-app-c4ff5"

# Secret Manager에 시크릿 저장 (처음 한 번만)
function Create-Secrets {
    Write-Host "Secret Manager에 시크릿 생성 중..." -ForegroundColor Yellow
    
    # 각 시크릿을 Secret Manager에 저장
    $secrets = @{
        "youtube-api-key" = Read-Host "YouTube API Key 입력" -AsSecureString
        "youtube-client-id" = Read-Host "YouTube Client ID 입력" -AsSecureString  
        "youtube-client-secret" = Read-Host "YouTube Client Secret 입력" -AsSecureString
        "tiktok-client-key" = Read-Host "TikTok Client Key 입력" -AsSecureString
        "tiktok-client-secret" = Read-Host "TikTok Client Secret 입력" -AsSecureString
        "jwt-secret" = Read-Host "JWT Secret 입력" -AsSecureString
    }
    
    foreach ($key in $secrets.Keys) {
        $plainText = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
            [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secrets[$key])
        )
        
        # Secret 생성 또는 업데이트
        echo $plainText | gcloud secrets create $key --data-file=- --project=$PROJECT_ID 2>$null
        if ($LASTEXITCODE -ne 0) {
            # 이미 존재하면 새 버전 추가
            echo $plainText | gcloud secrets versions add $key --data-file=- --project=$PROJECT_ID
        }
        
        Write-Host "✓ $key 저장 완료" -ForegroundColor Green
    }
}

# Cloud Run 배포
function Deploy-CloudRun {
    Write-Host ""
    Write-Host "Cloud Run 배포 시작..." -ForegroundColor Yellow
    
    # Secret Manager 참조를 사용한 배포
    gcloud run deploy adfit-server `
        --source . `
        --region asia-northeast3 `
        --allow-unauthenticated `
        --memory 512Mi `
        --cpu 1 `
        --project $PROJECT_ID `
        --update-env-vars `
            "ENABLE_FIRESTORE_UPDATE=false,`
            ENABLE_REALTIME_UPDATE=true,`
            STATS_UPDATE_TOKEN=adfit-stats-update-token,`
            FIREBASE_PROJECT_ID=$PROJECT_ID" `
        --update-secrets `
            "YOUTUBE_API_KEY=youtube-api-key:latest,`
            YOUTUBE_CLIENT_ID=youtube-client-id:latest,`
            YOUTUBE_CLIENT_SECRET=youtube-client-secret:latest,`
            TIKTOK_CLIENT_KEY=tiktok-client-key:latest,`
            TIKTOK_CLIENT_SECRET=tiktok-client-secret:latest,`
            JWT_SECRET=jwt-secret:latest"
    
    if ($LASTEXITCODE -eq 0) {
        Write-Host ""
        Write-Host "✅ 배포 성공!" -ForegroundColor Green
        
        $url = gcloud run services describe adfit-server --region asia-northeast3 --format="value(status.url)" --project=$PROJECT_ID
        Write-Host "서비스 URL: $url" -ForegroundColor Cyan
        
        # Health Check
        Write-Host ""
        Write-Host "Health Check 중..." -ForegroundColor Yellow
        Start-Sleep -Seconds 3
        
        try {
            $response = Invoke-RestMethod "$url/api/stats/health"
            Write-Host "✅ 서비스 정상 작동 중" -ForegroundColor Green
            $response | ConvertTo-Json
        } catch {
            Write-Host "⚠️ Health Check 실패 - 서비스가 시작되는 중일 수 있습니다" -ForegroundColor Yellow
        }
    } else {
        Write-Host "❌ 배포 실패!" -ForegroundColor Red
    }
}

# 메인 실행
Write-Host "작업을 선택하세요:" -ForegroundColor Yellow
Write-Host "1. Secret Manager에 시크릿 저장 (처음 한 번만)"
Write-Host "2. Cloud Run 배포 (시크릿 이미 저장됨)"
Write-Host "3. 모두 실행"

$choice = Read-Host "선택 (1/2/3)"

switch ($choice) {
    "1" { Create-Secrets }
    "2" { Deploy-CloudRun }
    "3" { 
        Create-Secrets
        Deploy-CloudRun
    }
    default { Write-Host "잘못된 선택입니다" -ForegroundColor Red }
}

Write-Host ""
Write-Host "완료!" -ForegroundColor Green
