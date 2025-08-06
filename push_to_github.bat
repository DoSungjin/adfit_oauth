@echo off
echo ===================================
echo Pushing AdFit OAuth Server to GitHub
echo ===================================
echo.

cd /d D:\Budit\posted_app\adfit-oauth-server

echo Initializing Git repository...
git init

echo.
echo Adding remote origin...
git remote remove origin 2>nul
git remote add origin https://github.com/DoSungjin/adfit_oauth.git

echo.
echo Adding all files...
git add .

echo.
echo Creating commit...
git commit -m "Initial commit: AdFit OAuth Server for TikTok"

echo.
echo Setting main branch...
git branch -M main

echo.
echo Pushing to GitHub...
git push -u origin main --force

echo.
echo Creating and pushing tag...
git tag -a v1.0.0 -m "Version 1.0.0 - Initial Release" 2>nul
git push origin v1.0.0 2>nul

echo.
echo ===================================
echo AdFit OAuth Server pushed successfully!
echo Repository: https://github.com/DoSungjin/adfit_oauth
echo ===================================
pause
