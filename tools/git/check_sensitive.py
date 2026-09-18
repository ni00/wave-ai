#!/usr/bin/env python3
"""Check Git blobs without printing matched values. Uses only the standard library."""

import argparse
import ipaddress
from pathlib import PurePosixPath
import re
import subprocess
import sys


IPV4 = re.compile(r"(?<![\w.])(?:\d{1,3}\.){3}\d{1,3}(?![\w.])")
IPV6 = re.compile(r"(?<![\w:.])(?:[0-9a-fA-F]{0,4}:){2,}[0-9a-fA-F:.]*(?:%[\w.-]+)?")
PRIVATE_KEY = re.compile(r"-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----")
TOKEN = re.compile(
    r"\b(?:sk-(?:proj-|ant-[\w-]*?)?[A-Za-z0-9_-]{20,}"
    r"|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}"
    r"|(?:AKIA|ASIA)[A-Z0-9]{16}|xox[baprs]-[A-Za-z0-9-]{20,}"
    r"|AIza[A-Za-z0-9_-]{35})\b"
)
JWT = re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b")
ASSIGNMENT = re.compile(
    r"(?i)(?<![\w])(?:[\w-]*(?:password|passwd|secret|token|api[_-]?key|master[_-]?key|access[_-]?key)[\w-]*)"
    r"[\"']?\s*[:=]\s*[\"']([^\"'\r\n]+)[\"']"
)
ENV_SECRET = re.compile(
    r"^\s*(?:export\s+)?[A-Z_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_KEY|MASTER_KEY|ACCESS_KEY)[A-Z_]*\s*=\s*([^\s#]+)"
)
BEARER = re.compile(r"(?i)\bBearer\s+([A-Za-z0-9._~+/-]{16,}=*)")
URL_PASSWORD = re.compile(r"\b[a-z][a-z0-9+.-]*://[^\s/:\"']+:([^\s/@\"']+)@([^\s/\"']+)", re.I)
HOST_ASSIGNMENT = re.compile(
    r"(?i)\b(?:VPS_HOST|VPS_USER|WAVE_QA_HOST|WAVE_QA_SSH|DEPLOY_HOST|SSH_HOST)"
    r"[\"']?\s*[:=]\s*[\"']?([a-zA-Z0-9_$<][^\s\"',;]*)"
)
SSH_HOST = re.compile(r"\b[\w.-]+@((?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,})\b")
SAFE_EXAMPLES = {"example", "example.com", "example.org", "example.net", "localhost"}
TEST_CREDENTIALS = {"wave-test-password", "fake-initial-token", "fake-rotated-token", "rotated-fixture-token"}


def placeholder(value):
    """Allow explicit placeholders, environment references and short test values."""
    return (
        value.startswith(("$", "<", "{{"))
        or value.lower() in {"changeme", "change-me", "replace-me", "your-api-key", "your-key"}
    )


def example_host(value):
    host = value.rsplit("@", 1)[-1].split(":", 1)[0].lower().rstrip(".")
    return host in SAFE_EXAMPLES or host.endswith((".example.com", ".example.org", ".example.net", ".test", ".invalid", ".localhost"))


def sensitive_path(path):
    name = PurePosixPath(path).name.lower()
    if name.endswith(".example"):
        return False
    return (
        name == ".env" or name.startswith(".env.") or name.endswith(".local")
        or name in {"id_rsa", "id_ed25519", "id_ecdsa", "credentials.json"}
        or name.endswith((".p12", ".pfx", ".key"))
        or path.endswith((".aws/credentials", ".docker/config.json", ".kube/config"))
    )


def scan(path, data):
    findings = set()
    if sensitive_path(path):
        findings.add((1, "sensitive-file"))
    binary = b"\0" in data
    # Binary blobs still receive the high-confidence key/token checks.
    for number, line in enumerate(data.decode("utf-8", "replace").splitlines(), 1):
        for pattern, rule in ((PRIVATE_KEY, "private-key"), (TOKEN, "provider-token"), (JWT, "jwt")):
            if pattern.search(line):
                findings.add((number, rule))
        if binary:
            continue
        for pattern in (IPV4, IPV6):
            for match in pattern.finditer(line):
                try:
                    address = ipaddress.ip_address(match.group().split("%", 1)[0])
                except ValueError:
                    continue
                if address.version == 6 and address.ipv4_mapped:
                    address = address.ipv4_mapped
                # The deprecated 6to4 relay block is tested by the egress guard.
                deprecated_relay = address.version == 4 and address in ipaddress.ip_network("192.88.99.0/24")
                if address.is_global and not address.is_multicast and not deprecated_relay:
                    findings.add((number, "public-ip"))
        for pattern in (ASSIGNMENT, ENV_SECRET, BEARER):
            for match in pattern.finditer(line):
                value = match.group(1).strip("\"'")
                if (len(value) >= 16 and not placeholder(value) and value not in TEST_CREDENTIALS
                        and re.fullmatch(r"[A-Za-z0-9_./+=~!-]+", value)):
                    findings.add((number, "credential-literal"))
        for match in URL_PASSWORD.finditer(line):
            if not placeholder(match.group(1)) and not example_host(match.group(2)):
                host = match.group(2).split(":", 1)[0]
                if host != "127.0.0.1" and not match.group(2).startswith("[::1]"):
                    findings.add((number, "url-credentials"))
        for match in HOST_ASSIGNMENT.finditer(line):
            if (re.fullmatch(r"[A-Za-z0-9_.@:\[\]-]+", match.group(1))
                    and not placeholder(match.group(1)) and not example_host(match.group(1))):
                findings.add((number, "deployment-connection"))
        # Match SSH destinations in deployment commands/config, not ordinary email addresses.
        if re.search(r"(?i)\b(?:ssh|scp|rsync)\b|WAVE_QA_SSH", line):
            for match in SSH_HOST.finditer(line):
                if not example_host(match.group(1)):
                    findings.add((number, "ssh-host"))
    return sorted(findings)


def git(*args):
    return subprocess.check_output(["git", *args], stderr=subprocess.PIPE)


def blobs(revision=None, all_files=False):
    if all_files:
        paths = git("ls-files", "-z")
    elif revision:
        paths = git("diff-tree", "--root", "-m", "--no-commit-id", "--name-only", "-r", "-z",
                    "--no-renames", "--diff-filter=ACMT", revision)
    else:
        paths = git("diff", "--cached", "--name-only", "-z", "--no-renames", "--diff-filter=ACMT")
    for raw in sorted(set(paths.split(b"\0")) - {b""}):
        path = raw.decode("utf-8", "surrogateescape")
        spec = f"{revision or ''}:{path}"
        # Submodules point at commits, not blobs.
        if git("cat-file", "-t", spec).strip() == b"blob":
            yield path, git("cat-file", "blob", spec)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--all", action="store_true", help="check all tracked files in the index")
    mode.add_argument("--range", dest="revision_range", help="check changed files in every commit in a Git revision range")
    args = parser.parse_args()
    try:
        revisions = [None]
        if args.revision_range:
            revisions = git("rev-list", "--reverse", args.revision_range, "--").decode().splitlines()
        count = 0
        for revision in revisions:
            for path, data in blobs(revision, args.all):
                for line, rule in scan(path, data):
                    # repr prevents control characters in filenames from altering terminal output.
                    print(f"{path!r}:{line}: {rule}" + (f" (commit {revision[:12]})" if revision else ""), file=sys.stderr)
                    count += 1
        if count:
            print(f"Blocked: {count} sensitive-content finding(s). Use environment variables or reserved examples. "
                  "See docs/commit-safety.md. Matched values are never printed.", file=sys.stderr)
            return 1
        print("Sensitive-content check passed.")
        return 0
    except (OSError, subprocess.CalledProcessError):
        print("Sensitive-content check failed to read Git data; commit blocked. Check Git and Python 3.10+.", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
