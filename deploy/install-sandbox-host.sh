#!/usr/bin/env bash
# Ubuntu host setup only. Wave itself uses SDK/API calls, never these CLIs.
set -euo pipefail
if [ "$(id -u)" != 0 ]; then exec sudo bash "$0" "$@"; fi
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg
curl --connect-timeout 15 --max-time 90 -fsSL https://gvisor.dev/archive.key | gpg --batch --yes --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
printf 'deb [arch=%s signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main\n' "$(dpkg --print-architecture)" > /etc/apt/sources.list.d/gvisor.list
apt-get update -qq
apt-get install -y -qq runsc
# Preserve existing daemon settings (including forwarding settings used by K3s).
python3 - <<'PY'
import json, pathlib, shutil, time
p = pathlib.Path('/etc/docker/daemon.json')
data = json.loads(p.read_text()) if p.exists() else {}
if p.exists(): shutil.copy2(p, p.with_name('daemon.json.before-wave-' + str(int(time.time()))))
data.setdefault('runtimes', {})['wave-runsc'] = {'path': '/usr/bin/runsc', 'runtimeArgs': ['--platform=systrap', '--overlay2=none', '--file-access=shared']}
p.parent.mkdir(parents=True, exist_ok=True)
p.write_text(json.dumps(data, indent=2)+'\n')
PY
systemctl reload docker
runsc --version
