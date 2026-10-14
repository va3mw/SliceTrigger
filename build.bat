@echo off
rem Build SliceTrigger.exe on Windows and write VERSION. Requires Go.
rem rsrc embeds the icon and the common controls v6 manifest; walk crashes at startup without the manifest.
go run ./tools/mkicon || exit /b 1
go run github.com/akavel/rsrc@v0.10.2 -manifest slicetrigger.manifest -ico assets/slicetrigger.ico -arch amd64 -o rsrc_windows_amd64.syso || exit /b 1
go build -trimpath -ldflags "-H windowsgui -s -w" -o SliceTrigger.exe . || exit /b 1
powershell -NoProfile -Command "$v = (Select-String -Path version.go -Pattern 'const Version = \"(.*)\"').Matches[0].Groups[1].Value; $h = (Get-FileHash SliceTrigger.exe -Algorithm SHA256).Hash.ToLower(); Set-Content -Path VERSION -Value @($v, $h) -Encoding ascii" || exit /b 1
echo Built SliceTrigger.exe
