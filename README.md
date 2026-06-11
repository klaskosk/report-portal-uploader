# report-portal-uploader

CLI for uploading xUnit test results to [Report Portal](https://reportportal.io/).

## Commands

- `rpu send` — upload using a legacy metadata JSON file (Layer 1 migration path).
- `rpu upload` — upload using a TOML config file (CI-oriented, Layer 2).

## `rpu upload` config

Pass `--config path/to/config.toml` or `--config -` to read TOML from stdin. The API token is **not** stored in the config; use `REPORTPORTAL_TOKEN` or the root `--token` flag.

### Source globs (`[[sources]].path`)

Each `[[sources]]` entry expands its `path` with Go's [`filepath.Glob`](https://pkg.go.dev/path/filepath#Glob):

- Only `*` (any characters) and `?` (one character) are supported.
- There is **no** `**` recursive glob and **no** brace expansion (`{a,b}`).
- A literal file path works when the file exists.
- Zero matches for an entry is a hard error.
- The same file may appear in multiple `[[sources]]` entries; each match is uploaded separately (with its own lane tag when configured).

Example:

```toml
[report_portal]
hostname = "reportportal.example.com"
project = "my-project"

[launch]
name = "nightly"

[[sources]]
lane = "gm"
path = "xml/gm/*.xml"

[[sources]]
lane = ""
path = "xml/shared/*.xml"
```

See [layer2_detailed_plan.md](layer2_detailed_plan.md) for the full config schema.

## Build and test

```bash
make build
make test-unit
make test-integration
```
