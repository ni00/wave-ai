# Check commits for sensitive content

[简体中文](commit-safety.zh-CN.md)

Enable the repository's Git hook to reject sensitive content before a commit.
The check requires Git and Python 3.10 or later. It uses no external packages.

## Enable the check

1. Run this command from the repository root in each clone:

   ```bash
   make hooks-install
   ```

2. Stage your changes, then run the check or commit normally:

   ```bash
   git add <paths>
   make sensitive-check
   git commit
   ```

The installer sets the local `core.hooksPath` to `.githooks`. It refuses to replace
an existing hook configuration or existing hooks. If you already use hooks, add
`python3 tools/git/check_sensitive.py` to your pre-commit hook, run it from the
repository root, and propagate its nonzero exit status.

## Resolve a blocked commit

The check reads the full staged version of each added, modified, copied, renamed,
or type-changed file. Unstaged edits do not affect the result. Deleted files are
not scanned. Reports contain the filename, line number, and rule; they omit the
matched value. A Git read error also blocks the commit.

1. Replace real values with environment references such as `${VPS_HOST}`,
   `${VPS_USER}`, or `${WAVE_MODEL_API_KEY}`. Store the values in an ignored local
   configuration file.
2. Use reserved examples such as `host.example.com`, `192.0.2.1`, or
   `2001:db8::1` in documentation and tests.
3. Stage the corrected files and run `make sensitive-check` again.

The rules detect:

- Private-key headers, common provider token formats, and JWT-shaped strings.
- Quoted credential assignments, environment assignments, and Bearer values
  containing at least 16 characters from the supported credential character set.
- Globally routable IPv4 and IPv6 literals, including IPv4-mapped IPv6.
- Literal deployment connection variables, SSH command destinations, and URLs
  containing passwords.
- Sensitive filenames such as `.env`, `.env.production`, `*.local`, private-key
  files, and common credential stores. Files ending in `.example` are exempt
  from the filename rule but still receive content checks.

Loopback, private, documentation, multicast, and the deprecated 6to4 relay
addresses are allowed. Example domains and local database fixture URLs are
allowed. Four existing synthetic credential values are explicitly listed in
`TEST_CREDENTIALS` in the scanner; files containing tests are otherwise checked
normally. Binary files receive filename, private-key, provider-token, and JWT
checks, but not IP or generic credential checks.

## Run an audit

```bash
make sensitive-audit              # Scan every tracked file in the index.
make sensitive-test               # Run scanner and hook regression tests.
python3 tools/git/check_sensitive.py --range BASE..HEAD
```

Range mode scans changed files in every included commit, including intermediate
commits that a later commit fixes or deletes. The **Sensitive content** GitHub
Actions workflow uses this mode for pull requests and pushes to `main` or
`master`. Configure its `sensitive-content` job as a required status check in
branch protection if you want it to block merges. CI runs after upload; the
local hook is the check that runs before a commit.

## Limits

These checks use patterns. They cannot identify every secret, arbitrary hostnames
in prose, encoded values, or all binary content. Generic credentials shorter
than 16 characters may pass. Review staged changes before committing. A local
hook can be bypassed, and clones must each install it. The workflow does not
configure branch protection automatically.

The check does not remove existing Git history. If a real credential was already
committed, revoke or rotate it and handle history cleanup separately.
