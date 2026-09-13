# Configuration and startup

## Local processes

The service reads its process's `WAVE_*` environment variables. Consult the repository's
`deploy/.env.example`; do not execute or source an untrusted env file. In the intended
process environment, run:

```bash
wave config check
wave serve --help
```

Choose api, worker, scheduler or all with `wave serve -role ROLE`; the default comes from
WAVE_ROLE. API and worker processes share PostgreSQL, blob storage settings and the
master key. Workers also need valid model settings, and tasks with environments need a
working sandbox. Remote API callers do not need these server-side settings.

Set `WAVE_STORAGE_BACKEND=local` for local files; S3 is the default. S3 requires a stable
32-byte WAVE_MASTER_KEY encoded as 64 hexadecimal characters. Existing credentials depend
on the original key; do not replace it merely to pass startup validation. Restore the
saved settings for the same instance.

## Repository Compose setup

Work from the repository root and check whether `deploy/.env` already contains settings;
the deployment tools read this file:

```bash
make setup
# Edit deploy/.env according to the user's choices; retain existing generated keys.
make up
docker compose --project-directory deploy ps
docker compose --project-directory deploy logs --tail 100 wave
```

`make setup` fills missing values only. `make up` starts dependencies and initializes the
database; use it when starting that deployment is intended. For an existing failing
deployment, inspect logs before repeating the initialization workflow.

Inside a running container, use
`docker compose --project-directory deploy exec wave /usr/local/bin/wave config check`
to inspect its actual environment. Running `wave config check` on the host does not
validate the Compose environment.

`make down` retains volumes. Before data maintenance, identify backups for the database,
file content, keys and persistent sandbox disks. Deleting volumes or reinitializing is
not a routine troubleshooting step.
