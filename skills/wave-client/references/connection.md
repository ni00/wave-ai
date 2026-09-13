# Connection, configuration and output

## Select a deployment

```bash
wavectl config list --format table
wavectl config show --effective
wavectl doctor --offline
```

URL precedence is `--url`, `WAVE_BASE_URL`, profile, then localhost. For keys,
`WAVE_API_KEY` overrides the profile. Environment variables may override a newly selected
profile; inspect `url_source` / `key_source` when diagnosing the wrong connection instead
of issuing another Key. Wave API Keys and model-provider keys are not interchangeable.

Save new connection details supplied by the user:

```bash
wavectl config set local --url http://localhost:8080
wavectl config set local --key-stdin < /path/to/wave-key.txt
wavectl config use local
wavectl doctor
```

`config set` does not change the active selection; `config use` does. For a single operation,
use `--profile NAME`. An explicitly selected missing profile fails instead of falling back
to another service. `config show` and `doctor` never print keys. Override the configuration
path with `WAVE_CONFIG_FILE`; the default is `wave-ai/config.json` under the user configuration directory.

## Read results

- Default JSON is suited to programs; `--pretty` helps inspection and `--format table` displays resource lists.
- `--select /id --raw` emits a raw ID. Selection uses JSON Pointer, including `/data/0/id`
  and the `~0` / `~1` escapes. Missing fields fail instead of returning a misleading empty value.
- `--all --format jsonl` emits each item across pages; lines emitted before an error remain valid.
- SSE `data` is the raw event-data string. Read frames as JSONL, then parse data according to the event type.
- Error JSON on stderr includes exit_code and message; API errors also include type, status and request_id.

Choose one request-body source: field flags, or full `--body @file.json` / `--body -`.
They are not merged, preventing accidental configuration overrides. Repeat string IDs
with `--skill-id ID` or `--expert-id ID`. Text-file flags such as `--input-file`,
`--instructions-file` and `--token-file` accept `-` for stdin.

`doctor` checks configuration, database health and API scope only; it does not prove
that workers, models or sandboxes are ready.
