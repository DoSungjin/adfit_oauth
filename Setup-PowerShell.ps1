# PowerShell 실행 정책 확인 및 설정 스크립트

Write-Host "🔧 PowerShell 실행 환경 설정" -ForegroundColor Cyan

# 현재 실행 정책 확인
$currentPolicy = Get-ExecutionPolicy
Write-Host "현재 실행 정책: $currentPolicy" -ForegroundColor Yellow

if ($currentPolicy -eq "Restricted") {
    Write-Host "❌ PowerShell 스크립트 실행이 제한되어 있습니다." -ForegroundColor Red
    Write-Host "다음 중 하나를 선택하세요:" -ForegroundColor Yellow
    Write-Host "1. 현재 사용자만 허용 (권장)" -ForegroundColor White
    Write-Host "2. 로컬 컴퓨터 전체 허용" -ForegroundColor White
    Write-Host "3. 임시로 우회" -ForegroundColor White
    Write-Host "4. 취소" -ForegroundColor White
    
    $choice = Read-Host "`n선택하세요 (1-4)"
    
    switch ($choice) {
        "1" {
            Write-Host "현재 사용자 정책을 RemoteSigned로 변경합니다..." -ForegroundColor Cyan
            try {
                Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser -Force
                Write-Host "✅ 설정 완료" -ForegroundColor Green
            } catch {
                Write-Host "❌ 설정 실패: $($_.Exception.Message)" -ForegroundColor Red
                Write-Host "관리자 권한으로 다시 시도하세요." -ForegroundColor Yellow
            }
        }
        "2" {
            Write-Host "⚠️ 관리자 권한이 필요합니다." -ForegroundColor Yellow
            try {
                Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope LocalMachine -Force
                Write-Host "✅ 설정 완료" -ForegroundColor Green
            } catch {
                Write-Host "❌ 설정 실패: $($_.Exception.Message)" -ForegroundColor Red
                Write-Host "관리자 권한으로 PowerShell을 실행하고 다시 시도하세요." -ForegroundColor Yellow
            }
        }
        "3" {
            Write-Host "💡 임시 우회 방법:" -ForegroundColor Cyan
            Write-Host "PowerShell에서 다음 명령을 실행하세요:" -ForegroundColor White
            Write-Host "Set-ExecutionPolicy -ExecutionPolicy Bypass -Scope Process" -ForegroundColor Gray
            Write-Host "또는 스크립트 실행시:" -ForegroundColor White
            Write-Host "powershell -ExecutionPolicy Bypass -File .\Start-AdFit.ps1" -ForegroundColor Gray
        }
        "4" {
            Write-Host "👋 취소되었습니다." -ForegroundColor Yellow
            exit 0
        }
    }
} else {
    Write-Host "✅ PowerShell 스크립트 실행이 허용되어 있습니다." -ForegroundColor Green
}

Write-Host "`n📋 AdFit PowerShell 스크립트 사용법:" -ForegroundColor Cyan
Write-Host "• 전체 시스템 시작: .\Start-AdFit.ps1" -ForegroundColor White
Write-Host "• API 서버만: .\Run-Server.ps1" -ForegroundColor White  
Write-Host "• 크론잡만: .\cron\Run-CronJobs.ps1" -ForegroundColor White

Write-Host "`n🚀 이제 AdFit 스크립트를 실행할 수 있습니다!" -ForegroundColor Green
Read-Host "계속하려면 Enter를 누르세요"
