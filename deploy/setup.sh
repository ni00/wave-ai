#!/bin/sh
# Fill missing deployment secrets once, preserving existing configuration.
set -eu
umask 077
deploy_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
env_file=${1:-"$deploy_dir/.env"}
command -v openssl >/dev/null || { echo "setup requires openssl" >&2; exit 1; }
lock_dir="$env_file.setup.lock"
mkdir "$lock_dir" 2>/dev/null || { echo "another setup is running; retry after it finishes" >&2; exit 1; }
temp_file=
cleanup() { [ -z "$temp_file" ] || rm -f "$temp_file"; rmdir "$lock_dir"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
temp_file=$(mktemp "$env_file.setup.XXXXXX")
if [ -f "$env_file" ]; then
  cat "$env_file" > "$temp_file"
else
  cat "$deploy_dir/.env.example" > "$temp_file"
fi
for name in WAVE_MASTER_KEY AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY; do
  # Do not source .env: it is configuration, not executable shell code.
  if awk -v name="$name" '
    $0 ~ "^" name "=" {
      value = substr($0, length(name) + 2)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (value != "" && value != "\042\042" && value != "\047\047") found = 1
    }
    END { exit !found }
  ' "$temp_file"; then
    continue
  fi
  value=$(openssl rand -hex 32)
  # Generated values contain only hex; existing nonempty values never change.
  sed -i "/^${name}=/d" "$temp_file"
  printf '\n%s=%s\n' "$name" "$value" >> "$temp_file"
done
chmod 600 "$temp_file"
mv "$temp_file" "$env_file"
temp_file=
echo "Deployment configuration ready; existing secrets preserved."
