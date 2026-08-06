#!/usr/bin/env bash
# Create/update a local .env with session secret and password hash.
# Usage: ./scripts/configure.sh [path-to-.env]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${1:-$ROOT/.env}"
EXAMPLE="$ROOT/.env.example"

if [[ ! -f "$EXAMPLE" ]]; then
	echo "missing $EXAMPLE" >&2
	exit 1
fi

set_kv() {
	local key="$1" val="$2" tmp
	tmp="$(mktemp)"
	while IFS= read -r line || [[ -n "$line" ]]; do
		if [[ "$line" == "$key="* ]]; then
			printf '%s=%s\n' "$key" "$val"
		else
			printf '%s\n' "$line"
		fi
	done < "$ENV_FILE" > "$tmp"
	mv "$tmp" "$ENV_FILE"
}

if [[ -f "$ENV_FILE" ]]; then
	read -r -p "$ENV_FILE already exists. Overwrite from .env.example? [y/N] " ans
	case "$ans" in
	[yY]|[yY][eE][sS]) ;;
	*)
		echo "aborted"
		exit 1
		;;
	esac
fi

cp "$EXAMPLE" "$ENV_FILE"
chmod 600 "$ENV_FILE"
echo "wrote $ENV_FILE"

if ! command -v openssl >/dev/null 2>&1; then
	echo "openssl not found" >&2
	exit 1
fi

secret="$(openssl rand -hex 32)"
set_kv JOBAPP_SESSION_SECRET "$secret"
echo "set JOBAPP_SESSION_SECRET"

read -r -s -p "Site password: " password
echo
if [[ -z "$password" ]]; then
	echo "password required" >&2
	exit 1
fi
read -r -s -p "Confirm password: " password2
echo
if [[ "$password" != "$password2" ]]; then
	echo "passwords do not match" >&2
	exit 1
fi

hash="$(go run "$ROOT/scripts/hashpassword" "$password")"
set_kv JOBAPP_PASSWORD_HASH "$hash"
echo "set JOBAPP_PASSWORD_HASH"

echo "done — fill remaining secrets in $ENV_FILE (OpenRouter, Telegram, etc.)"
