#!/usr/bin/env bash
# Runs the same Go checks as CI: formatting, vet, staticcheck, tests and the
# Windows cross-build. Usage: scripts/check-go.sh
set -euo pipefail

STATICCHECK_VERSION=v0.8.2 # staticcheck 2026.2.2

cd "$(dirname "$0")/../client"

# Use the toolchain pinned in go.mod for every command, including
# "go run tool@version", which would otherwise pick its own Go version.
GOTOOLCHAIN="$(awk '/^toolchain /{print $2}' go.mod)"
export GOTOOLCHAIN

echo "==> gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
	echo "gofmt needed on:" >&2
	echo "$unformatted" >&2
	exit 1
fi

echo "==> go mod tidy"
go mod tidy -diff

# Build staticcheck once for the host, then run it per target GOOS.
tools_bin="$(go env GOPATH)/bin/knockblock-tools"
GOBIN="$tools_bin" go install "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}"

for goos in linux windows; do
	echo "==> go vet ($goos)"
	GOOS=$goos go vet ./...
	echo "==> staticcheck ($goos)"
	GOOS=$goos "$tools_bin/staticcheck" ./...
done

echo "==> go test"
go test -race ./...

echo "==> windows build"
mkdir -p ../dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -H=windowsgui -X main.version=${VERSION:-dev}" \
	-o ../dist/knockblock.exe ./cmd/knockblock

echo "All Go checks passed."
