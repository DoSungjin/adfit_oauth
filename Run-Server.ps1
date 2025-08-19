# AdFit OAuth Server with Configuration (PowerShell)
# PowerShell 전용 서버 실행 스크립트

param(
    [string]$Environment = "development",
    [string]$ConfigPath = "config\app_config.yaml"
)

Write-Host "🚀 AdFit OAuth Server (PowerShell) 시작..." -ForegroundColor Cyan
Write-Host "환경: $Environment" -ForegroundColor Yellow

# 설정 파일 존재 확인
if (-not (Test-Path $ConfigPath)) {
    Write-Host "❌ 오류: $ConfigPath 파일이 없습니다" -ForegroundColor Red
    Read-Host "계속하려면 Enter를 누르세요"
    exit 1
}
Write-Host "✅ 설정 파일 확인 완료: $ConfigPath" -ForegroundColor Green

# .env 파일 로드 (PowerShell 방식)
if (Test-Path ".env") {
    Write-Host "📁 .env 파일 로드 중..." -ForegroundColor Yellow
    Get-Content ".env" | ForEach-Object {
        if ($_ -match "^([^#].*)=(.*)$") {
            $name = $matches[1].Trim()
            $value = $matches[2].Trim()
            [System.Environment]::SetEnvironmentVariable($name, $value, "Process")
            Write-Host "  $name = $($value.Substring(0, [Math]::Min(10, $value.Length)))..." -ForegroundColor Gray
        }
    }
    Write-Host "✅ 환경변수 로드 완료" -ForegroundColor Green
} else {
    Write-Host "⚠️ .env 파일이 없습니다. 기본 환경변수를 사용합니다." -ForegroundColor Yellow
}

# 필수 환경변수 확인
$requiredVars = @("YOUTUBE_API_KEY", "TIKTOK_CLIENT_SECRET")
$missingVars = @()

foreach ($var in $requiredVars) {
    $value = [System.Environment]::GetEnvironmentVariable($var)
    if ([string]::IsNullOrEmpty($value)) {
        $missingVars += $var
        Write-Host "⚠️ 경고: $var 환경변수가 설정되지 않음" -ForegroundColor Yellow
    } else {
        Write-Host "✅ $var 설정됨" -ForegroundColor Green
    }
}

if ($missingVars.Count -gt 0) {
    Write-Host "⚠️ 누락된 환경변수: $($missingVars -join ', ')" -ForegroundColor Yellow
    Write-Host "일부 기능이 제한될 수 있습니다." -ForegroundColor Yellow
}

# 환경변수 설정
$env:ENVIRONMENT = $Environment

try {
    Write-Host "🔧 Go 서버 시작 중..." -ForegroundColor Cyan
    
    # Go 모듈 확인
    if (-not (Test-Path "go.mod")) {
        Write-Host "❌ go.mod 파일이 없습니다. Go 프로젝트 루트에서 실행하세요." -ForegroundColor Red
        exit 1
    }
    
    # Go 서버 실행
    Write-Host "▶️ go run main_with_config.go" -ForegroundColor White
    & go run main_with_config.go
    
} catch {
    Write-Host "❌ 서버 실행 오류: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "스택 트레이스: $($_.ScriptStackTrace)" -ForegroundColor Gray
} finally {
    Write-Host "`n🛑 서버가 종료되었습니다." -ForegroundColor Yellow
    Read-Host "계속하려면 Enter를 누르세요"
}
