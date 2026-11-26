#!/usr/bin/env bash
set -euo pipefail
umask 077

directory="${1:?Directory is required}"
bundle="${2:?Archive name is required}"
bootstrap="${3:?Bootstrap flag is required}"

[[ "$directory" =~ ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ ]] || { echo 'Invalid directory' >&2; exit 1; }
[[ "$bundle" =~ ^atlas-deploy-[a-f0-9]{32}\.tar\.gz$ ]] || { echo 'Invalid archive name' >&2; exit 1; }
[[ "$bootstrap" == 0 || "$bootstrap" == 1 ]] || { echo 'Invalid bootstrap flag' >&2; exit 1; }

app_dir="$HOME/$directory"
archive="$HOME/$bundle"
trap 'rm -f -- "$archive"' EXIT

command -v docker >/dev/null || { echo 'Install Docker Engine and the Compose plugin first.' >&2; exit 1; }
if docker info >/dev/null 2>&1; then
    docker_cmd=(docker)
elif command -v sudo >/dev/null && sudo -n docker info >/dev/null 2>&1; then
    docker_cmd=(sudo -n docker)
else
    echo 'Docker is not running or SSH user cannot access it.' >&2
    exit 1
fi
"${docker_cmd[@]}" compose version >/dev/null

if [[ "$bootstrap" == 1 && ( -e "$app_dir/.env" || -e "$app_dir/google" || -e "$app_dir/secrets" ) ]]; then
    echo 'Existing configuration found. Deploy WITHOUT -Bootstrap.' >&2
    exit 1
fi

mkdir -p -- "$app_dir"
tar -xzf "$archive" -C "$app_dir" --no-same-owner
cd -- "$app_dir"

inspect_cmd=()
if [[ "$(id -u)" != 0 ]] && command -v sudo >/dev/null && sudo -n true 2>/dev/null; then
    inspect_cmd=(sudo -n)
fi

if [[ "$bootstrap" == 1 ]]; then
    mkdir -p google
    mv -- data/google-token.json google/google-token.json
    if [[ "$(id -u)" != 0 && ${#inspect_cmd[@]} == 0 ]]; then
        echo 'Initial deployment requires root or passwordless sudo to set credentials ownership (UID 10001).' >&2
        exit 1
    fi
    "${inspect_cmd[@]}" chown -R 10001:10001 google secrets
    "${inspect_cmd[@]}" chmod 700 google secrets
    "${inspect_cmd[@]}" chmod 600 google/google-token.json secrets/google-credentials.json
fi

for required in .env secrets/google-credentials.json google/google-token.json; do
    if ! "${inspect_cmd[@]}" test -f "$required"; then
        echo "Missing server file: $required. Configure credentials before starting Atlas." >&2
        exit 1
    fi
done
"${inspect_cmd[@]}" chmod 600 .env

# Use the GHCR image built by GitHub Actions, like qrave1/sozvon.
# Pull before replacing the container, so a failed pull leaves it running.
"${docker_cmd[@]}" compose pull
"${docker_cmd[@]}" compose up -d
"${docker_cmd[@]}" compose ps
"${docker_cmd[@]}" compose logs --tail=40 atlas
