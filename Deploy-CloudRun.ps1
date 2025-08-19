# AdFit Cloud Run 배포 스크립트 (환경변수 자동 적용)

param(
    [string]$ServiceName = "adfit-server",
    [string]$Region = "asia-northeast3",
    [string]$EnvFile = ".env"
)

Write-Host "🚀 AdFit Cloud Run 배포 시작..." -ForegroundColor Cyan

# .env 파일 존재 확인
if (-not (Test-Path $EnvFile)) {
    Write-Host "❌ $EnvFile 파일이 없습니다" -ForegroundColor Red
    exit 1
}

# .env 파일 읽기 및 환경변수 배열 생성
$envVars = @()
$envCount = 0

Write-Host "📁 환경변수 파일 읽는 중..." -ForegroundColor Yellow

Get-Content $EnvFile | ForEach-Object {
    $line = $_.Trim()
    if ($line -and -not $line.StartsWith("#") -and $line.Contains("=")) {
        $parts = $line.Split("=", 2)
        if ($parts.Length -eq 2) {
            $name = $parts[0].Trim()
            $value = $parts[1].Trim()
            
            # 따옴표 제거
            $value = $value -replace '^["'']|["'']$', ''
            
            $envVars += "$name=$value"
            $envCount++
            Write-Host "  ✓ $name = $($value.Substring(0, [Math]::Min(20, $value.Length)))..." -ForegroundColor Green
        }
    }
}

if ($envCount -eq 0) {
    Write-Host "❌ 유효한 환경변수가 없습니다" -ForegroundColor Red
    exit 1
}

Write-Host "📊 총 $envCount 개 환경변수 준비 완료" -ForegroundColor Green

# 환경변수 문자열 생성
$envString = $envVars -join ","

# 배포 명령어 실행
Write-Host "🔨 Cloud Run 배포 중..." -ForegroundColor Cyan
Write-Host "서비스명: $ServiceName" -ForegroundColor White
Write-Host "리전: $Region" -ForegroundColor White

try {
    $deployCmd = "gcloud run deploy $ServiceName --source . --platform managed --region $Region --allow-unauthenticated --set-env-vars=`"$envString`""
    
    Write-Host "실행 명령어:" -ForegroundColor Gray
    Write-Host $deployCmd -ForegroundColor Gray
    
    # 명령어 실행
    Invoke-Expression $deployCmd
    
    if ($LASTEXITCODE -eq 0) {
        Write-Host "✅ 배포 성공!" -ForegroundColor Green
        Write-Host "🌐 서비스 URL: https://$ServiceName-[hash]-$Region.run.app" -ForegroundColor Cyan
        Write-Host "📋 관리자 API: https://$ServiceName-[hash]-$Region.run.app/api/admin/system/health" -ForegroundColor Cyan
    } else {
        Write-Host "❌ 배포 실패" -ForegroundColor Red
    }
} catch {
    Write-Host "❌ 배포 중 오류 발생: $($_.Exception.Message)" -ForegroundColor Red
}

Write-Host "`n📋 배포 후 확인사항:" -ForegroundColor Yellow
Write-Host "1. Cloud Console에서 서비스 상태 확인" -ForegroundColor White
Write-Host "2. /health 엔드포인트로 서버 작동 확인" -ForegroundColor White
Write-Host "3. 크론잡 로그 확인 (1시간 후)" -ForegroundColor White

Read-Host "`n계속하려면 Enter를 누르세요"
