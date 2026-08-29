@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"
set "APP=lumeidc.exe"

rem 支持参数: start / stop / restart；不带参数进入菜单
if /i "%~1"=="start" goto start
if /i "%~1"=="stop" goto stop
if /i "%~1"=="restart" goto restart

:menu
echo.
echo ============ lumeidc 服务管理 ============
echo   1. 启动
echo   2. 停止
echo   3. 重启
echo   0. 退出
echo =========================================
set /p opt=请输入序号: 
if "%opt%"=="1" goto start
if "%opt%"=="2" goto stop
if "%opt%"=="3" goto restart
if "%opt%"=="0" exit /b 0
echo 输入无效，请重新输入
goto menu

:start
tasklist /fi "imagename eq %APP%" 2>nul | find /i "%APP%" >nul && (
  echo %APP% 已在运行
  exit /b 0
)
start "" /b "%APP%" 1>server.out.log 2>server.err.log
echo %APP% 已启动
exit /b 0

:stop
taskkill /im %APP% /f >nul 2>&1
echo %APP% 已停止
exit /b 0

:restart
call :stop
call :start
exit /b 0
