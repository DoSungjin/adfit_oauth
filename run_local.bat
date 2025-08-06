@echo off
echo ===================================
echo Starting Local OAuth Server...
echo ===================================
echo.
echo Server will run on: http://localhost:8080
echo.

REM Load environment variables
if exist .env (
    echo Loading .env file...
    for /f "delims=" %%i in (.env) do set %%i
)

REM Run Go server
go run main.go

pause
