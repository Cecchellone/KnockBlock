#!/usr/bin/env bash
# Regenerates the Go protobuf code in client/internal/espapi/pb from
# api.proto and api_options.proto. Never edit the generated code by hand.
#
#   proto/generate.sh           generate from the files in proto/
#   proto/generate.sh --fetch   first download both files from the ESPHome
#                               release in proto/VERSION, then generate
set -euo pipefail

BUF_VERSION=v1.75.0

cd "$(dirname "$0")"
version="$(cat VERSION)"

if [ "${1:-}" = "--fetch" ]; then
	for f in api.proto api_options.proto; do
		echo "==> fetching $f from ESPHome $version"
		curl -fsSL -o "$f" \
			"https://raw.githubusercontent.com/esphome/esphome/${version}/esphome/components/api/${f}"
	done
fi

# Use the Go toolchain pinned by the client module.
GOTOOLCHAIN="$(awk '/^toolchain /{print $2}' ../client/go.mod)"
export GOTOOLCHAIN

echo "==> generating Go code (ESPHome $version)"
rm -f ../client/internal/espapi/pb/*.pb.go
go run "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}" generate --template buf.gen.yaml .
echo "Done."
