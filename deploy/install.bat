@echo off
setlocal enabledelayedexpansion
rem Project HANGAR -- turnkey installer for Windows (SRS Section 9.1, Gate 5).
rem
rem   1) curl -fsSLO https://raw.githubusercontent.com/hangar-project/hangar/main/docker-compose.yml
rem   2) curl -fsSL  https://raw.githubusercontent.com/hangar-project/hangar/main/deploy/install.bat -o install.bat ^&^& install.bat
rem   3) docker compose up -d
rem
rem Only ever writes .env in the current directory. Never generates a
rem fallback key inside the binary itself -- see internal/config/validate.go.

set "ENV_FILE=.env"
set "EXAMPLE_URL=https://raw.githubusercontent.com/hangar-project/hangar/main/.env.example"

if exist "%ENV_FILE%" (
  echo install.bat: %ENV_FILE% already exists -- leaving it untouched.
  echo install.bat: delete it first if you want a fresh installation.
  exit /b 0
)

echo install.bat: fetching .env.example...
curl -fsSL "%EXAMPLE_URL%" -o "%ENV_FILE%.tmp"
if errorlevel 1 (
  echo install.bat: failed to download .env.example
  exit /b 1
)

for /f "delims=" %%K in ('powershell -NoProfile -Command "[Convert]::ToBase64String((New-Object byte[] 32 ^| %%{ (New-Object Security.Cryptography.RNGCryptoServiceProvider).GetBytes($_); $_ }))"') do set "MASTER_KEY=%%K"
for /f "delims=" %%K in ('powershell -NoProfile -Command "[Convert]::ToBase64String((New-Object byte[] 32 ^| %%{ (New-Object Security.Cryptography.RNGCryptoServiceProvider).GetBytes($_); $_ }))"') do set "SESSION_SECRET=%%K"
for /f "delims=" %%K in ('powershell -NoProfile -Command "[Convert]::ToBase64String((New-Object byte[] 32 ^| %%{ (New-Object Security.Cryptography.RNGCryptoServiceProvider).GetBytes($_); $_ }))"') do set "POSTGRES_PASSWORD=%%K"

powershell -NoProfile -Command ^
  "(Get-Content '%ENV_FILE%.tmp') |" ^
  "ForEach-Object { $_ -replace '^HANGAR_MASTER_KEY=.*', 'HANGAR_MASTER_KEY=%MASTER_KEY%' } |" ^
  "ForEach-Object { $_ -replace '^HANGAR_SESSION_SECRET=.*', 'HANGAR_SESSION_SECRET=%SESSION_SECRET%' } |" ^
  "ForEach-Object { $_ -replace '^POSTGRES_PASSWORD=.*', 'POSTGRES_PASSWORD=%POSTGRES_PASSWORD%' } |" ^
  "Set-Content -Encoding utf8 '%ENV_FILE%'"

del "%ENV_FILE%.tmp"

echo.
echo install.bat: wrote %ENV_FILE% with generated HANGAR_MASTER_KEY, HANGAR_SESSION_SECRET
echo             and POSTGRES_PASSWORD.
echo.
echo Before running "docker compose up -d", register an application at
echo https://developers.eveonline.com/applications and set in %ENV_FILE%:
echo   HANGAR_SSO_CLIENT_ID=...
echo   HANGAR_SSO_CLIENT_SECRET=...
echo The callback URL must be exactly ${HANGAR_PUBLIC_URL}/auth/callback.
