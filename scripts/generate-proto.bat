@echo off
setlocal enabledelayedexpansion

set PROTOC_BIN=D:\cloudx\.tools\protoc\bin\protoc.exe
set PATH=D:\cloudx\.tools\protoc\bin;D:\cloudx\.tools\go\bin;%USERPROFILE%\go\bin;%PATH%

if not exist "%PROTOC_BIN%" (
    echo Error: protoc not found at %PROTOC_BIN%
    exit /b 1
)

echo Generating Go code from proto/v1/cloudx.proto...

"%PROTOC_BIN%" --proto_path=proto/v1 --go_out=proto/v1 --go_opt=paths=source_relative --go-grpc_out=proto/v1 --go-grpc_opt=paths=source_relative proto/v1/cloudx.proto

if %ERRORLEVEL% equ 0 (
    echo Protobuf generation successful!
) else (
    echo Protobuf generation failed with exit code %ERRORLEVEL%
    exit /b %ERRORLEVEL%
)
