@echo off
rem Build SliceTrigger.exe on Windows. Requires Go.
rem rsrc embeds the common controls v6 manifest; walk crashes at startup without it.
go run github.com/akavel/rsrc@v0.10.2 -manifest slicetrigger.manifest -arch amd64 -o rsrc_windows_amd64.syso || exit /b 1
go build -trimpath -ldflags "-H windowsgui -s -w" -o SliceTrigger.exe . || exit /b 1
echo Built SliceTrigger.exe
