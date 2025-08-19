# AdFit Admin API 테스트 스크립트 (PowerShell)

param(
    [string]$BaseUrl = "http://localhost:8080/api",
    [string]$AdminToken = "adfit-admin-secret"
)

Write-Host "🧪 AdFit Admin API 테스트 시작" -ForegroundColor Cyan
Write-Host "서버: $BaseUrl" -ForegroundColor Yellow

# 공통 헤더
$headers = @{
    "Authorization" = "Bearer adfit-stats-update-token"
    "X-Admin-Token" = $AdminToken
    "Content-Type" = "application/json"
}

function Test-ApiEndpoint {
    param(
        [string]$Method,
        [string]$Endpoint,
        [hashtable]$Body = $null,
        [string]$Description
    )
    
    Write-Host "`n📋 테스트: $Description" -ForegroundColor White
    Write-Host "   $Method $Endpoint" -ForegroundColor Gray
    
    try {
        $uri = "$BaseUrl$Endpoint"
        
        if ($Body) {
            $response = Invoke-RestMethod -Uri $uri -Method $Method -Headers $headers -Body ($Body | ConvertTo-Json)
        } else {
            $response = Invoke-RestMethod -Uri $uri -Method $Method -Headers $headers
        }
        
        Write-Host "   ✅ 성공" -ForegroundColor Green
        if ($response.message) {
            Write-Host "   💬 $($response.message)" -ForegroundColor Cyan
        }
        if ($response.data) {
            Write-Host "   📊 데이터 개수: $($response.data.PSObject.Properties.Count)" -ForegroundColor Blue
        }
        return $response
    } catch {
        Write-Host "   ❌ 실패: $($_.Exception.Message)" -ForegroundColor Red
        return $null
    }
}

# 1. 시스템 헬스 체크
Test-ApiEndpoint -Method "GET" -Endpoint "/admin/system/health" -Description "시스템 상태 확인"

# 2. 저장소 통계 조회
$storageStats = Test-ApiEndpoint -Method "GET" -Endpoint "/admin/storage/stats" -Description "저장소 통계 조회"

# 3. 백업 정보 조회
Test-ApiEndpoint -Method "GET" -Endpoint "/admin/storage/backup-info" -Description "백업 정보 조회"

# 4. 수동 시간별 스냅샷 실행
Test-ApiEndpoint -Method "POST" -Endpoint "/admin/trigger/hourly-snapshots" -Description "시간별 스냅샷 수동 실행"

# 5. 수동 일별 집계 실행
Test-ApiEndpoint -Method "POST" -Endpoint "/admin/trigger/daily-aggregation" -Description "일별 집계 수동 실행"

# 6. 특정 대회 시간별 스냅샷 (예시 대회 ID가 있다면)
Write-Host "`n📋 테스트: 특정 대회 시간별 스냅샷" -ForegroundColor White
Write-Host "   (실제 대회 ID가 필요합니다)" -ForegroundColor Gray

# 7. 30일 이전 데이터 정리 (실제로는 실행하지 않음)
Write-Host "`n⚠️ 데이터 정리 테스트 (실행하지 않음)" -ForegroundColor Yellow
Write-Host "   DELETE /admin/cleanup/old-snapshots?days=30" -ForegroundColor Gray
Write-Host "   DELETE /admin/cleanup/date-range?start_date=2024-01-01&end_date=2024-01-31" -ForegroundColor Gray

# 8. 통계 API 테스트 (기존 API)
Write-Host "`n📊 기존 통계 API 테스트" -ForegroundColor Magenta

# 통계 상태 확인
try {
    $statsHealth = Invoke-RestMethod -Uri "$BaseUrl/stats/health" -Method GET
    Write-Host "   ✅ 통계 서비스 상태: $($statsHealth.message)" -ForegroundColor Green
} catch {
    Write-Host "   ❌ 통계 서비스 연결 실패" -ForegroundColor Red
}

# 전체 통계 업데이트 (실제로는 실행하지 않음 - 시간이 오래 걸림)
Write-Host "   📋 전체 통계 업데이트 (POST /stats/update/all)" -ForegroundColor Gray
Write-Host "   (실제 실행 시 시간이 오래 걸릴 수 있음)" -ForegroundColor Yellow

Write-Host "`n🎉 API 테스트 완료!" -ForegroundColor Green
Write-Host "`n📋 관리자 API 사용법:" -ForegroundColor Cyan
Write-Host "1. 저장소 상태 확인: GET /api/admin/storage/stats" -ForegroundColor White
Write-Host "2. 수동 스냅샷 실행: POST /api/admin/trigger/hourly-snapshots" -ForegroundColor White
Write-Host "3. 오래된 데이터 정리: DELETE /api/admin/cleanup/old-snapshots?days=30" -ForegroundColor White
Write-Host "4. 기간별 데이터 삭제: DELETE /api/admin/cleanup/date-range?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD" -ForegroundColor White

Write-Host "`n🔐 인증 정보:" -ForegroundColor Yellow
Write-Host "Authorization: Bearer adfit-stats-update-token" -ForegroundColor Gray
Write-Host "X-Admin-Token: adfit-admin-secret" -ForegroundColor Gray

Read-Host "`n계속하려면 Enter를 누르세요"
