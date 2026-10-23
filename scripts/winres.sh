#!/usr/bin/env bash
# Writes the Windows resources of knockblock.exe: the app icon (drawn by
# internal/icon), version information and a manifest (DPI awareness, no
# elevation). go build links the resulting client/cmd/knockblock/rsrc_*.syso
# into Windows builds only. Generated files are git-ignored.
#
#   scripts/winres.sh [version]     default version: 0.0.0
#
# Also writes dist/knockblock.ico, which the MSI uses.
set -euo pipefail

GO_WINRES_VERSION=v0.3.3

version="${1:-0.0.0}"
version="${version#v}"
root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$root/dist"

cd "$root/client"
GOTOOLCHAIN="$(awk '/^toolchain /{print $2}' go.mod)"
export GOTOOLCHAIN

go run ./cmd/genicon -o "$root/dist/knockblock.ico"
cd cmd/knockblock
GOOS= GOARCH= go run "github.com/tc-hib/go-winres@${GO_WINRES_VERSION}" simply \
	--arch amd64 \
	--out rsrc \
	--icon "$root/dist/knockblock.ico" \
	--manifest gui \
	--product-name KnockBlock \
	--file-description "KnockBlock tray app" \
	--original-filename knockblock.exe \
	--copyright "KnockBlock contributors, GPL-3.0" \
	--product-version "$version" \
	--file-version "$version"
echo "Wrote client/cmd/knockblock/rsrc_windows_amd64.syso ($version)"
