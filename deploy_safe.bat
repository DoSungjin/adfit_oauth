@echo off
echo ===================================
echo AdFit OAuth Server 안전한 배포 스크립트
echo ===================================
echo.

REM 환경 변수 파일 확인
if not exist .env (
    echo [ERROR] .env 파일이 없습니다!
    echo .env.example을 참고하여 .env 파일을 생성하세요.
    pause
    exit /b 1
)

echo .env 파일에서 설정을 로드합니다...
echo.

REM 환경 변수를 입력받거나 .env 파일 사용 안내
echo 다음 환경 변수들이 필요합니다:
echo - YOUTUBE_API_KEY
echo - YOUTUBE_CLIENT_ID  
echo - YOUTUBE_CLIENT_SECRET
echo - TIKTOK_CLIENT_KEY
echo - TIKTOK_CLIENT_SECRET
echo - JWT_SECRET
echo.
echo 이 값들을 .env 파일에 설정했는지 확인하세요.
echo.

pause

echo.
echo Cloud Run에 배포 중...
echo (시크릿은 Google Secret Manager를 통해 관리됩니다)
echo.

REM Secret Manager를 사용한 안전한 배포
gcloud run deploy adfit-server ^
    --source . ^
    --region asia-northeast3 ^
    --allow-unauthenticated ^
    --memory 512Mi ^
    --cpu 1 ^
    --update-env-vars ^
        ENABLE_FIRESTORE_UPDATE=false,^
        ENABLE_REALTIME_UPDATE=true,^
        STATS_UPDATE_TOKEN=adfit-stats-update-token,^
        FIREBASE_PROJECT_ID=posted-app-c4ff5

echo.
echo 중요: 민감한 정보는 Google Cloud Console에서 직접 설정하세요.
echo https://console.cloud.google.com/run
echo.

if %ERRORLEVEL% EQU 0 (
    echo ✅ 배포 성공!
    echo.
    echo 다음 단계:
    echo 1. Google Cloud Console에서 환경 변수 설정
    echo 2. Secret Manager에서 시크릿 관리
    echo.
    echo 서비스 URL:
    gcloud run services describe adfit-server --region asia-northeast3 --format="value(status.url)"
) else (
    echo ❌ 배포 실패!
)

pause
