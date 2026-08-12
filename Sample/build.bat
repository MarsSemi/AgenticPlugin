@echo off
setlocal
cd /d "%~dp0"
bash build.sh %*
