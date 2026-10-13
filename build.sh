#!/bin/sh
# Cross-compile SliceTrigger.exe from macOS/Linux.
set -e
cd "$(dirname "$0")"
go run github.com/akavel/rsrc@v0.10.2 -manifest slicetrigger.manifest -arch amd64 -o rsrc_windows_amd64.syso
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui -s -w" -o SliceTrigger.exe .
echo "Built SliceTrigger.exe"
