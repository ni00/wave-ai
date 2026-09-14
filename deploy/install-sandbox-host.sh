#!/usr/bin/env bash
# Ubuntu host setup only. Wave itself uses SDK/API calls, never these CLIs.
set -euo pipefail
if [ "$(id -u)" != 0 ]; then exec sudo bash "$0" "$@"; fi
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg uidmap dbus-user-session podman slirp4netns fuse-overlayfs
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
if ! id wave-sandbox >/dev/null 2>&1; then useradd --create-home --shell /usr/sbin/nologin wave-sandbox; fi
grep -q '^wave-sandbox:' /etc/subuid
grep -q '^wave-sandbox:' /etc/subgid
podman_uid=$(id -u wave-sandbox)
loginctl enable-linger wave-sandbox
systemctl start "user@${podman_uid}.service"
runuser -u wave-sandbox -- env XDG_RUNTIME_DIR="/run/user/${podman_uid}" systemctl --user enable --now podman.socket
install -d -m 0755 /run/wave-podman
ln -sfn "/run/user/${podman_uid}/podman/podman.sock" /run/wave-podman/podman.sock
# Recreate the stable socket link after reboot.
printf 'd /run/wave-podman 0755 root root -\nL+ /run/wave-podman/podman.sock - - - - /run/user/%s/podman/podman.sock\n' "$podman_uid" > /etc/tmpfiles.d/wave-podman.conf
runsc --version
podman --version
printf 'Rootless Podman socket: unix:///run/wave-podman/podman.sock (UID %s)\n' "$podman_uid"
