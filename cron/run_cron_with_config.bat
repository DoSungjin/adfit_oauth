@echo off
REM AdFit Cron Jobs with Configuration
REM 설정 파일을 사용한 크론잡 스케줄러 실행

echo 🤖 AdFit 크론잡 스케줄러 (with Config) 시작...

REM 환경변수 체크
if "%YOUTUBE_API_KEY%"=="" (
    echo ⚠️ 경고: YOUTUBE_API_KEY 환경변수가 설정되지 않음
    echo    통계 업데이트가 제한될 수 있습니다
)

REM 설정 파일 존재 체크
if not exist "..\config\app_config.yaml" (
    echo ❌ 오류: ..\config\app_config.yaml 파일이 없습니다
    pause
    exit /b 1
)

echo ✅ 설정 파일 확인 완료

REM 크론잡 실행 (설정 기반)
go run main_with_config.go

pause
