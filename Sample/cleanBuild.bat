@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ROOT_DIR=%~dp0"
cd /d "%ROOT_DIR%"
set /a REMOVED=0

call :remove_dir "bin"
call :remove_dir "build"
call :remove_dir "dist"
call :remove_dir "out"
call :remove_dir "tmp"
call :remove_dir ".tmp"
call :remove_dir "coverage"
call :remove_file "sample-service"
call :remove_file "sample-service.exe"

call :remove_dir "plugins\sample\bin"
call :remove_dir "plugins\sample\runtime"
call :remove_file "plugins\sample\config.json"
call :remove_file "plugins\sample\skill\skill-cards.json"

for /r "%ROOT_DIR%" %%F in (
    .DS_Store
    *.log
    *.tmp
    *.out
    *.prof
    *.coverprofile
    *.test
) do (
    if exist "%%F" (
        del /f /q "%%F" >nul 2>nul
        echo removed %%F
        set /a REMOVED+=1
    )
)

for /d /r "%ROOT_DIR%" %%D in (
    .cache
    .parcel-cache
    .turbo
    node_modules
) do (
    if exist "%%D" (
        rmdir /s /q "%%D" >nul 2>nul
        echo removed %%D
        set /a REMOVED+=1
    )
)

if "%REMOVED%"=="0" (
    echo clean: no build artifacts found
) else (
    echo clean: removed %REMOVED% artifact(s)
)

endlocal
exit /b 0

:remove_dir
if exist "%~1\" (
    rmdir /s /q "%~1" >nul 2>nul
    echo removed %~1
    set /a REMOVED+=1
)
exit /b 0

:remove_file
if exist "%~1" (
    del /f /q "%~1" >nul 2>nul
    echo removed %~1
    set /a REMOVED+=1
)
exit /b 0
