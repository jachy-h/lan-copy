@echo off
setlocal
cd /d "%~dp0"

set COMMAND=%1
if "%COMMAND%"=="" set COMMAND=help

if "%COMMAND%"=="build" goto :build
if "%COMMAND%"=="release" goto :release
if "%COMMAND%"=="test" goto :test
if "%COMMAND%"=="run" goto :run
if "%COMMAND%"=="clean" goto :clean
if "%COMMAND%"=="help" goto :help

echo Unknown command: %COMMAND%
echo.
goto :help

:help
echo lan-copy build script
echo.
echo Usage: build [command]
echo.
echo Available commands:
echo   build     - Build release/lan-copy.exe (double-click to start and open browser)
echo   release   - Build signed Windows packages (requires a code-signing certificate)
echo   test      - Run all tests
echo   run       - Run the program directly
echo   clean     - Clean build artifacts
echo   help      - Show this help message
echo.
echo Cross-platform packages are built with "make build" (see Makefile).
goto :end

:release
powershell.exe -NoProfile -File "%~dp0scripts\release-windows.ps1"
exit /b %ERRORLEVEL%

:build
if not exist release mkdir release
go build -trimpath -ldflags="-s -w" -o "release\lan-copy.exe" .
if %ERRORLEVEL% equ 0 (
    copy /Y "%~dp0release-assets\offline-start.html" "release\使用说明.html" >nul
    echo Build successful: release\lan-copy.exe
    echo Double-click "release\lan-copy.exe" to start and open Lan Copy.
    echo WARNING: This is an unsigned development build. Use "build release" for signed packages.
) else (
    echo Build failed
    exit /b 1
)
goto :end

:test
go test ./...
if %ERRORLEVEL% equ 0 (
    echo Tests passed
) else (
    echo Tests failed
    exit /b 1
)
goto :end

:run
go run .
goto :end

:clean
if exist release (
    rmdir /s /q release
    echo Deleted release directory
)
echo Clean complete
goto :end

:end
endlocal
