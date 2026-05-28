@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ROOT_DIR=%~dp0"
cd /d "%ROOT_DIR%"

if "%~1"=="" (
    for /f "usebackq delims=" %%I in (`powershell -NoProfile -Command "Get-Date -Format '1.yy.MMdd build HHmm'"`) do set "VERSION=%%I"
) else (
    set "VERSION=%~1"
)

for /f "usebackq delims=" %%I in (`powershell -NoProfile -Command "$env:VERSION -replace '[ /:]', '_'"`) do set "SAFE_VERSION=%%I"
set "PACKAGE_NAME=sample-plugin_%SAFE_VERSION%"
set "DIST_DIR=%ROOT_DIR%dist"
set "STAGE_DIR=%ROOT_DIR%build\%PACKAGE_NAME%"
set "ZIP_PATH=%DIST_DIR%\%PACKAGE_NAME%.zip"

echo build version: %VERSION%

if exist "%ROOT_DIR%build" rmdir /s /q "%ROOT_DIR%build"
if exist "%DIST_DIR%" rmdir /s /q "%DIST_DIR%"
mkdir "%STAGE_DIR%\plugins\sample\bin"
mkdir "%STAGE_DIR%\website"

robocopy "%ROOT_DIR%plugins\sample" "%STAGE_DIR%\plugins\sample" /E /XD bin runtime /XF config.json skill-cards.json >nul
robocopy "%ROOT_DIR%website\sample" "%STAGE_DIR%\website\sample" /E >nul
copy /Y "%ROOT_DIR%README.md" "%STAGE_DIR%\README.md" >nul

powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$ErrorActionPreference='Stop'; $version=$env:VERSION; $stage=$env:STAGE_DIR;" ^
  "$targets=@(); $targets += [pscustomobject]@{goos=(go env GOOS).Trim(); goarch=(go env GOARCH).Trim()}; $targets += [pscustomobject]@{goos='linux'; goarch='arm64'}; $targets += [pscustomobject]@{goos='linux'; goarch='amd64'}; $targets += [pscustomobject]@{goos='windows'; goarch='amd64'};" ^
  "$seen=@{}; $binaries=[ordered]@{}; foreach ($target in $targets) { $key=$target.goos + '/' + $target.goarch; if ($seen.ContainsKey($key)) { continue }; $seen[$key]=$true; $bin='sample-service_' + $target.goos + '_' + $target.goarch; if ($target.goos -eq 'windows') { $bin += '.exe' }; Write-Host ('building target: ' + $key + ' -> ' + $bin); $env:CGO_ENABLED='0'; $env:GOOS=$target.goos; $env:GOARCH=$target.goarch; & go build -trimpath -ldflags ('-s -w -X ''agentic-plugin/sample/src/sampleplugin.SamplePluginVersion=' + $version + '''') -o (Join-Path $stage ('plugins/sample/bin/' + $bin)) ./src/sampleplugin/service; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; $binaries[$key]='./plugins/sample/bin/' + $bin };" ^
  "$pluginFile=Join-Path $stage 'plugins/sample/plugin.json'; $plugin=Get-Content $pluginFile -Raw | ConvertFrom-Json; $plugin.version=$version; if (-not $plugin.entry) { $plugin | Add-Member -NotePropertyName entry -NotePropertyValue './plugins/sample/bin/sample-service' }; if ($plugin.PSObject.Properties.Name -contains 'platform_entries') { $plugin.platform_entries=[pscustomobject]@{} } else { $plugin | Add-Member -NotePropertyName platform_entries -NotePropertyValue ([pscustomobject]@{}) }; foreach ($key in $binaries.Keys) { $plugin.platform_entries | Add-Member -NotePropertyName $key -NotePropertyValue $binaries[$key] -Force }; $plugin | ConvertTo-Json -Depth 20 | Set-Content -Encoding UTF8 $pluginFile;" ^
  "$configFile=Join-Path $stage 'plugins/sample/config.default.json'; $config=Get-Content $configFile -Raw | ConvertFrom-Json; $config.version=$version; $config | ConvertTo-Json -Depth 20 | Set-Content -Encoding UTF8 $configFile;" ^
  "$info=[ordered]@{plugin_id='sample'; version=$version; target='multi'; targets=@($binaries.Keys); binaries=$binaries; platform_entries=$plugin.platform_entries; created_at=(Get-Date).ToUniversalTime().ToString('o')}; $info | ConvertTo-Json -Depth 20 | Set-Content -Encoding UTF8 (Join-Path $stage 'build-info.json');"
if errorlevel 1 exit /b 1

powershell -NoProfile -ExecutionPolicy Bypass -Command "Compress-Archive -Path '%STAGE_DIR%\*' -DestinationPath '%ZIP_PATH%' -Force"
if errorlevel 1 exit /b 1

echo package: %ZIP_PATH%
endlocal
exit /b 0
