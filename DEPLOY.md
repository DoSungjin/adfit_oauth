# 🚀 AdFit Server 배포 가이드

## 📋 초기 설정 (최초 1회)

### 1️⃣ 배포 스크립트 생성

```powershell
# Windows에서
Copy-Item deploy.ps1.example deploy.ps1
Copy-Item update_env.ps1.example update_env.ps1

# Linux/Mac에서
cp deploy.sh.example deploy.sh
chmod +x deploy.sh
```

### 2️⃣ 환경 변수 설정

`deploy.ps1` (또는 `deploy.sh`) 파일을 열어서 다음 변수들을 **실제 값**으로 변경:

```powershell
$YOUTUBE_CLIENT_ID = "YOUR_YOUTUBE_CLIENT_ID"          # ← 실제 값으로 변경
$YOUTUBE_CLIENT_SECRET = "YOUR_YOUTUBE_CLIENT_SECRET"  # ← 실제 값으로 변경
$YOUTUBE_API_KEY = "YOUR_YOUTUBE_API_KEY"              # ← 실제 값으로 변경
# ... (나머지도 동일)
```

---

## 🎯 배포 방법

### ✅ 일반 배포 (코드 변경 시)

```powershell
# Windows
.\deploy.ps1

# Linux/Mac
./deploy.sh
```

**소요 시간:** 1-2분

---

### ⚡ 환경 변수만 변경 시 (빠른 업데이트)

```powershell
# Windows
.\update_env.ps1

# Linux/Mac
# (update_env.sh 파일을 만들어 사용)
```

**소요 시간:** 10초

---

## 🔒 보안 주의사항

### ❌ Git에 커밋하지 말 것

다음 파일들은 **절대** Git에 푸시하지 마세요:

```
deploy.ps1          ← 실제 환경 변수 포함
deploy.sh           ← 실제 환경 변수 포함
update_env.ps1      ← 실제 환경 변수 포함
cloudbuild.yaml     ← 실제 환경 변수 포함
.env                ← 실제 환경 변수 포함
```

### ✅ Git에 커밋해도 되는 파일

```
deploy.ps1.example       ← 템플릿 (placeholder 값)
deploy.sh.example        ← 템플릿 (placeholder 값)
update_env.ps1.example   ← 템플릿 (placeholder 값)
cloudbuild.yaml.example  ← 템플릿 (placeholder 값)
```

---

## 📊 파일 구조

```
adfit-oauth-server/
├── deploy.ps1              ← ❌ Git 제외 (실제 값)
├── deploy.ps1.example      ← ✅ Git 포함 (템플릿)
├── deploy.sh               ← ❌ Git 제외 (실제 값)
├── deploy.sh.example       ← ✅ Git 포함 (템플릿)
├── update_env.ps1          ← ❌ Git 제외 (실제 값)
├── update_env.ps1.example  ← ✅ Git 포함 (템플릿)
├── cloudbuild.yaml         ← ❌ Git 제외 (실제 값)
├── cloudbuild.yaml.example ← ✅ Git 포함 (템플릿)
└── .gitignore              ← 위 파일들 제외 설정
```

---

## 🆘 문제 해결

### 배포가 3-4분 걸릴 때

1. **DB 파일 삭제 확인:**
   ```powershell
   Remove-Item adfit.db -ErrorAction SilentlyContinue
   ```

2. **빌드 아티팩트 삭제:**
   ```powershell
   Remove-Item main.exe, adfit-server -ErrorAction SilentlyContinue
   ```

3. **Docker 캐시 정리:**
   ```bash
   docker system prune -a
   ```

### 환경 변수 오류

`deploy.ps1` 파일에서 모든 `YOUR_XXX` 값이 실제 값으로 변경되었는지 확인하세요.

---

## 📝 배포 체크리스트

배포 전에 확인:

- [ ] `deploy.ps1` 파일에 실제 환경 변수 설정됨
- [ ] `adfit.db` 파일 삭제됨
- [ ] `main.exe`, `adfit-server` 파일 삭제됨
- [ ] Git에 민감 정보가 커밋되지 않았는지 확인
- [ ] `.gitignore`에 배포 파일들이 포함되어 있는지 확인

---

## 🎉 배포 완료 확인

배포 후 다음 URL에서 서버가 정상 작동하는지 확인:

```
https://adfit-server-520676604613.asia-northeast3.run.app/health
```

정상 응답: `{"status": "ok"}` 또는 유사한 응답
