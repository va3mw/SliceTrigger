#!/bin/sh
# Cross-compile SliceTrigger.exe from macOS/Linux and write VERSION.
set -e
cd "$(dirname "$0")"
go run ./tools/mkicon
go run github.com/akavel/rsrc@v0.10.2 -manifest slicetrigger.manifest -ico assets/slicetrigger.ico -arch amd64 -o rsrc_windows_amd64.syso
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui -s -w" -o SliceTrigger.exe .
version=$(sed -n 's/^const Version = "\(.*\)"/\1/p' version.go)
sum=$(shasum -a 256 SliceTrigger.exe | cut -d' ' -f1)
printf '%s\n%s\n' "$version" "$sum" > VERSION
echo "Built SliceTrigger.exe $version"
