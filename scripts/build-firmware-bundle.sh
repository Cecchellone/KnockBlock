#!/usr/bin/env bash
# Builds the ESPHome bundle of firmware/knockblock.yaml for a release:
# dist/knockblock-<version>.esphomebundle.tar.gz, with every local file the
# config needs (packages, locales, external components).
#
#   scripts/build-firmware-bundle.sh [version]     default version: dev
#
# `esphome bundle` validates the config, so it runs with throwaway secrets.
# The bundle then gets placeholder secrets instead: a published bundle must
# not carry an encryption key that every user would share. Users fill in
# secrets.yaml (Wi-Fi and `openssl rand -base64 32`) before
# `esphome run knockblock-<version>.esphomebundle.tar.gz`.
#
# Environment: ESPHOME (esphome command, default "esphome").
set -euo pipefail

version="${1:-dev}"
esphome="${ESPHOME:-esphome}"
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/dist/knockblock-$version.esphomebundle.tar.gz"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Work on a copy of the tracked firmware files, so a developer's own
# secrets.yaml never ends up in the bundle.
(cd "$root" && git ls-files firmware | tar -cf - -T -) | tar -xf - -C "$work"
# The device reports the release as its project version (Home Assistant
# shows it). Development builds keep the version in board.yaml.
if [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	sed -i "s|^    version: \"[^\"]*\"\(  # project version.*\)$|    version: \"$version\"\1|" \
		"$work/firmware/common/board.yaml"
	grep -q "version: \"$version\"" "$work/firmware/common/board.yaml"
fi
sed "s|^api_encryption_key: .*|api_encryption_key: \"$(openssl rand -base64 32)\"|" \
	"$work/firmware/secrets.example.yaml" >"$work/firmware/secrets.yaml"

echo "==> bundling firmware/knockblock.yaml ($version)"
(cd "$work/firmware" && $esphome bundle -o "$work/bundle.tar.gz" knockblock.yaml)

echo "==> replacing secrets with placeholders"
mkdir "$work/unpacked"
tar -xzf "$work/bundle.tar.gz" -C "$work/unpacked"
cat >"$work/unpacked/secrets.yaml" <<'SECRETS'
# Fill in before compiling. Generate the API encryption key with
#   openssl rand -base64 32
# and enter the same key in the KnockBlock tray app and in Home Assistant.
wifi_ssid: "REPLACE_WITH_YOUR_WIFI_NAME"
wifi_password: "REPLACE_WITH_YOUR_WIFI_PASSWORD"
api_encryption_key: "REPLACE_WITH_THE_OUTPUT_OF_openssl_rand_-base64_32"
SECRETS
# Keep the original member order (manifest first).
mapfile -t members < <(tar -tzf "$work/bundle.tar.gz")
mkdir -p "$root/dist"
tar -czf "$out" --owner=0 --group=0 --numeric-owner -C "$work/unpacked" "${members[@]}"
echo "Built ${out#"$root"/}"
