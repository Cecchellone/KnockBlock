#!/usr/bin/env bash
# Builds the Windows tray app and its MSI installer, on Linux.
#
#   scripts/build-msi.sh [version]     default version: 0.0.0
#
# The version must be numeric (major.minor.patch); a leading "v" (from a git
# tag) is dropped. Output: dist/KnockBlock-<version>.msi. Needs wixl and
# msitools (Debian/Ubuntu packages "wixl" and "msitools").
set -euo pipefail

version="${1:-0.0.0}"
version="${version#v}"
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "version must be major.minor.patch, got '$version'" >&2
	exit 1
fi
root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$root/dist"

echo "==> building knockblock.exe $version"
"$root/scripts/winres.sh" "$version"
cd "$root/client"
GOTOOLCHAIN="$(awk '/^toolchain /{print $2}' go.mod)" \
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -H=windowsgui -X main.version=$version" \
	-o "$root/dist/knockblock.exe" ./cmd/knockblock

echo "==> building KnockBlock-$version.msi"
wixl --arch x64 \
	-D Version="$version" \
	-D Exe="$root/dist/knockblock.exe" \
	-D License="$root/LICENSE" \
	-D Icon="$root/dist/knockblock.ico" \
	-o "$root/dist/KnockBlock-$version.msi" \
	"$root/installer/knockblock.wxs"
# AUTOSTART=0 disables the StartWithWindows feature; wixl cannot express it.
(cd "$root/installer" && msibuild "$root/dist/KnockBlock-$version.msi" -i condition.idt)
echo "Built dist/KnockBlock-$version.msi"
