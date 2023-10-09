@echo off
:: cd every subdirectory and run command go build
for /d %%i in (*) do (
    :: skip img dir
    if "%%i"=="img" goto :skip
    set lastdir=%%i
    cd %%i || goto error
    echo Building %%i
    go build || goto error
    cd ..    
)

exit /b 0

:error
echo Error building %lastdir%: %errorlevel%
exit /b %errorlevel%