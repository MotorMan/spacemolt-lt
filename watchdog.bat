@echo off
REM SpaceMolt LT Watchdog
REM Restarts the client if it exits with code 100 (mass disconnect restart request)
REM or code 101 (user-requested restart, e.g. to apply updates)
REM Normal shutdown (exit code 0) will not trigger restart

setlocal enabledelayedexpansion

set RESTART_DELAY=30
set MANUAL_RESTART_DELAY=5
set SCRIPT_DIR=%~dp0
set BINARY_NAME=spacemolt-lt.exe

echo ========================================
echo SpaceMolt LT Watchdog
echo ========================================
echo.
echo Configuration:
echo   - Restart delay: %RESTART_DELAY% seconds (mass disconnect)
echo   - Manual restart delay: %MANUAL_RESTART_DELAY% seconds (user-initiated)
echo   - Working directory: %SCRIPT_DIR%
echo   - Binary: %BINARY_NAME%
echo   - Git pull on start: enabled
echo.
echo Exit codes:
echo   - 0: Normal shutdown (no restart)
echo   - 100: Restart requested (mass disconnect detected)
echo   - 101: Restart requested (user-initiated, e.g. to apply updates)
echo   - Other: Unexpected exit (no restart)
echo.
echo Press Ctrl+C to stop the watchdog.
echo ========================================
echo.

:loop
    echo [%date% %time%] Starting SpaceMolt LT...
    echo.

    cd /d "%SCRIPT_DIR%"
    echo [%date% %time%] Building Go binary...
    go build -o %BINARY_NAME% ./cmd/spacemolt-lt || (
        echo [%date% %time%] Build failed!
        goto :end
    )
    echo [%date% %time%] Build complete.
    echo.
    echo [%date% %time%] Running git pull...
    git pull || echo Warning: git pull failed or not a git repository
    echo.
    echo [%date% %time%] Launching %BINARY_NAME%...
    %BINARY_NAME%
    set EXIT_CODE=%ERRORLEVEL%

    echo.
    echo [%date% %time%] spacemolt-lt exited with code %EXIT_CODE%
    
    if %EXIT_CODE% EQU 0 (
        echo === Normal shutdown - no restart ===
        echo.
        goto :end
    ) else if %EXIT_CODE% EQU 100 (
        echo.
        echo === Restart requested (mass disconnect) ===
        goto :dorestart
    ) else if %EXIT_CODE% EQU 101 (
        echo.
        echo === Restart requested (user-initiated) ===
        goto :dorestart_manual
    ) else (
        echo.
        echo === Unexpected exit code %EXIT_CODE% - no restart ===
        echo.
        goto :end
    )

    :dorestart
    echo.
    echo Waiting %RESTART_DELAY% seconds before restart...
    timeout /t %RESTART_DELAY% /nobreak
    echo.
    echo === Restarting spacemolt-lt ===
    echo.
    goto :loop

    :dorestart_manual
    echo.
    echo Waiting %MANUAL_RESTART_DELAY% seconds before restart...
    timeout /t %MANUAL_RESTART_DELAY% /nobreak
    echo.
    echo === Restarting spacemolt-lt ===
    echo.
    goto :loop

:end
    echo.
    echo Watchdog stopped.
    pause
