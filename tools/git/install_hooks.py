#!/usr/bin/env python3
"""Enable repository hooks without replacing an existing hook configuration."""
from pathlib import Path
import subprocess
import sys


def main():
    root = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
    configured = subprocess.run(["git", "config", "--get", "core.hooksPath"], capture_output=True, text=True)
    if configured.returncode not in (0, 1):
        return 1
    if configured.returncode == 0 and configured.stdout.strip() != ".githooks":
        print("Existing core.hooksPath found. Add the sensitive-content check to your existing pre-commit hook; see docs/commit-safety.md.", file=sys.stderr)
        return 1
    default = Path(subprocess.check_output(["git", "rev-parse", "--git-path", "hooks"], text=True).strip())
    if configured.returncode == 1 and default.exists():
        if any(p.is_file() and not p.name.endswith(".sample") for p in default.iterdir()):
            print("Existing Git hooks found. Integrate the check manually; see docs/commit-safety.md.", file=sys.stderr)
            return 1
    hook = root / ".githooks/pre-commit"
    hook.chmod(hook.stat().st_mode | 0o111)
    subprocess.run(["git", "config", "--local", "core.hooksPath", ".githooks"], check=True)
    print("Enabled sensitive-content checks for this clone.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
