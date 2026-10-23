#!/usr/bin/env bash
# Compiles a host-platform firmware, runs it in the background and runs the
# Go integration tests against it.
#
#   scripts/itest-host.sh [config]   default: host-test.yaml (in firmware/)
#
# Environment: ESPHOME (esphome command, default "esphome"),
# KNOCKBLOCK_ITEST_KEY (encryption key, for encrypted configs).
set -euo pipefail

config="${1:-host-test.yaml}"
esphome="${ESPHOME:-esphome}"
root="$(cd "$(dirname "$0")/.." && pwd)"
port=6053

cd "$root/firmware"
name="$(awk '/^esphome:/{f=1; next} f && /^  name:/{print $2; exit}' "$config")"
if [ -z "$name" ]; then
	echo "cannot find esphome: name: in $config" >&2
	exit 1
fi

echo "==> compiling $config ($name)"
$esphome compile "$config"
program=".esphome/build/$name/.pioenvs/$name/program"

echo "==> starting $program"
log="$(mktemp)"
"$program" >"$log" 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; echo "==> device log"; tail -n 40 "$log"; rm -f "$log"' EXIT

for _ in $(seq 1 100); do
	if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
		break
	fi
	sleep 0.1
done

echo "==> running integration tests"
cd "$root/client"
GOTOOLCHAIN="$(awk '/^toolchain /{print $2}' go.mod)" \
	KNOCKBLOCK_ITEST_ADDR="127.0.0.1:$port" \
	go test -tags integration -count=1 -v -run Integration ./internal/espapi/
# KNOCKBLOCK_ITEST_KEY, if set, is inherited by go test.
