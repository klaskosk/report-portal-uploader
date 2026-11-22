# report-portal-uploader

`rpu` is a Go CLI that uploads xUnit/JUnit XML results to [Report Portal](https://reportportal.io/) via the `junit` plugin import. It prepares XML, zips it, imports it, and reconciles Report Portal test items so properties and attachments land on the right nodes.

Built for two audiences:

| Workflow | Command | Config | Audience |
|---|---|---|---|
| **Data Router compatible** | `rpu send` | `metadata.json` (legacy shape) | Teams that already produce Data Router metadata and need a drop-in replacement without breaking existing pipelines. |
| **Expanded / standalone** | `rpu upload` | `config.toml` | Teams free of Data Router constraints that want lanes, multi-source globs, and simpler CI wiring. |

Both commands share the same import and post-import pipeline.

---

## Prerequisites

- Go 1.27+ (for building) or the prebuilt `bin/rpu` binary.
- podman (optional; only for building or using the container image).
- A Report Portal project with the `junit` import plugin enabled.
- A Report Portal API token with permission to create imports in that project.

---

## Install

```bash
git clone https://github.com/klaskosk/report-portal-uploader
cd report-portal-uploader

make build            # -> bin/rpu
./bin/rpu --help
```

Other useful targets:

```bash
make build-static-linux-amd64  # CGO_ENABLED=0 linux/amd64
make test                      # unit tests (alias for test-unit)
make test-unit
make test-integration          # requires Docker + a Report Portal instance
make lint                      # golangci-lint --fix (root + test/integration)
make tidy
```

### Container image

The binary is also published as a container image, which is usually easier to
consume from CI than a loose binary. It is rebuilt on every commit to
`main`, and versioned on every `v*` tag (e.g. `v0.1.0` → `:v0.1.0`):

```bash
podman pull quay.io/rh-ee-klaskosk/report-portal-uploader:latest
```

The image carries the statically linked `rpu` binary on top of UBI 9 micro
(`registry.access.redhat.com/ubi9/ubi-micro`), plus the CA bundle copied in
from `ubi9/ubi-minimal` so HTTPS works. There is no package manager, compiler,
or Go toolchain. `/work` is the image's working directory, so mount the
directory holding your config and results there and pass the token with
`REPORTPORTAL_TOKEN`:

```bash
podman run --rm \
  -e REPORTPORTAL_TOKEN \
  -v "$PWD:/work:ro,z" \
  quay.io/rh-ee-klaskosk/report-portal-uploader:latest \
  upload --config /work/config.toml
```

`rpu send` works the same way:

```bash
podman run --rm \
  -e REPORTPORTAL_TOKEN \
  -v "$PWD:/work:ro,z" \
  quay.io/rh-ee-klaskosk/report-portal-uploader:latest \
  send --metadata /work/metadata.json --results '/work/results/*.xml'
```

Notes:

- The `z` on the mount relabels it for SELinux; it is required on enforcing
  hosts (RHEL, Fedora, CentOS Stream) and ignored elsewhere. Use `:Z` instead
  when the directory is used by this container only.
- Relative `[[sources]].path` and `--results` patterns resolve against `/work`;
  use absolute in-container paths for anything mounted elsewhere.
- The mounts above are read-only. Drop `:ro` when `output.launch_url_file` has
  to be written back to the host.
- The image runs as UID 1001 with GID 0, which is also what OpenShift assigns,
  so `runAsNonRoot` policies accept it. Under rootless podman GID 0 is your own
  group, so the mounted directory only needs to be group-readable. Add
  `--user 0` when the mount is root-only instead: a `0600` config, or a
  directory you cannot write when `output.launch_url_file` is set. Under
  rootless podman container UID 0 is your own user, so nothing is created as
  root on the host.
- The binary is built `CGO_ENABLED=0` and stripped (`-ldflags="-s -w"`): it uses
  Go's own TLS stack rather than OpenSSL, so it does not run in OpenSSL FIPS
  mode, and panic traces carry no symbol names. Both are deliberate for a
  batch CLI; build from source with different flags if you need otherwise.
- `ubi-micro` ships no CA bundle, so the image copies `tls-ca-bundle.pem` from
  `ubi9/ubi-minimal` to `/etc/pki/tls/certs/ca-bundle.crt` (the path Go reads
  first) and `/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem`. Those are the
  public roots. If Report Portal is behind a private CA, point `SSL_CERT_FILE`
  at a bundle containing it; that is additive, since Go still scans
  `/etc/pki/tls/certs`, so public endpoints keep working:

  ```bash
  podman run --rm \
    -e REPORTPORTAL_TOKEN \
    -e SSL_CERT_FILE=/work/private-ca.pem \
    -v "$PWD:/work:ro,z" \
    quay.io/rh-ee-klaskosk/report-portal-uploader:latest \
    upload --config /work/config.toml
  ```

  Mounting a file over `/etc/pki/tls/certs/ca-bundle.crt` also works, but that
  is the image's only bundle: overriding it drops the public roots, so the
  mounted file has to contain them as well.
- The work directory (`/tmp/rpu-workdir-*`) lives inside the container;
  `--keep-workdir` only keeps it until the container exits.
- `podman run <image>` with no arguments prints `rpu --help`.
- Exit status, stdout, and stderr are unchanged from running `rpu` directly, so
  `--json` / `--output` pipelines keep working.

Rebuild and publish by hand:

```bash
make image       # podman build -t quay.io/rh-ee-klaskosk/report-portal-uploader:dev .
make image-push  # TAG=... to override; local builds default to :dev, not :latest
```

`.github/workflows/container-image-pr.yaml` builds the image on every pull
request without registry credentials and checks that the container starts.
`.github/workflows/container-image.yaml` builds the image on every push to
`main` (`:latest` and `:sha-<commit>`) and on every `v*` tag (`:v*` and
`:sha-<commit>`); the commit SHA and image digest are written to the
workflow run summary. It
authenticates with the `QUAY_USERNAME` and `QUAY_TOKEN` repository secrets (a
Quay robot account token with write access to
`rh-ee-klaskosk/report-portal-uploader`). The Quay repository must already exist
(Quay does not create it on push) and must be public, or whatever pulls the
image has to log in to Quay first.

---

## Authentication

Tokens are never stored in config files.

```bash
# preferred: environment variable
export REPORTPORTAL_TOKEN="api-token-here"
rpu upload --config config.toml
rpu send --metadata metadata.json --results "results/*.xml"

# or per-invocation flag (same for both subcommands)
rpu upload --config config.toml --token "api-token-here"
```

The flag wins if both are set.

---

## Workflow 1 — Data Router compatibility: `rpu send`

Use this when your pipeline already produces a Data Router `metadata.json` and you want `rpu` to behave like the legacy uploader. No caller changes except binary name.

### When to use

- You have CI jobs that emit `metadata.json` with `targets.reportportal`.
- You want templated metadata, `property_filter` as RE2 full-string patterns, `testcase_concat_char` handling, and `disabled` short-circuit — exactly as Data Router did.
- You cannot yet migrate to TOML.

### Metadata JSON

`rpu send` reads `targets.reportportal` from a JSON file. Unknown top-level keys are ignored. Minimal valid file:

```json
{
  "targets": {
    "reportportal": {
      "disabled": false,
      "config": {
        "hostname": "reportportal.example.com",
        "project": "demo"
      },
      "processing": {
        "property_filter": ["inventory-.*", "ci-lane", "attachment"],
        "disable_testitem_updater": false,
        "testcase_concat_char": ".",
        "launch": {
          "name": "nightly-main",
          "description": "main branch, nightly trigger",
          "attributes": [
            { "key": "branch", "value": "main" },
            { "key": "team", "value": "sre", "system": false }
          ]
        }
      }
    }
  }
}
```

Key contract (all under `targets.reportportal`):

- `disabled` (`bool`, default `false`) — if `true` the run exits cleanly without uploading and prints `Report Portal target disabled in metadata; skipping upload`.
- `config.hostname` (**required**) — bare host (`reportportal.example.com`) or full URL (`https://rp.example.com`). `rpu` normalizes to a trailing-slash API base.
- `config.project` (**required**) — Report Portal project to import into.
- `processing.property_filter` (`[]string`, may be `null`) — RE2 regexes matched with full-string semantics (`^(?:pattern)$`). Only `<property name>` values that match become item attributes; `attachment` properties are also used to discover files on disk. `null` and `[]` are distinct: `null` means "no filter" (nothing inherited from Data Router), `[]` means explicitly empty.
- `processing.disable_testitem_updater` (`bool`) — when `false` (default in many pipelines) the post-import step patches each test item with matched properties and pushes attachment logs. When `true` that reconciliation is skipped.
- `processing.testcase_concat_char` (`string`, 0 or 1 char) — when set, `classname` is prepended to `name` on every `<testcase>` (`C.N` with `.`, `C/N` with `/`, etc.). Forbidden: `< > " ? * '`.
- `processing.launch.name` / `processing.launch.description` / `processing.launch.attributes` — launch bookkeeping; `name` defaults to `rpu uploaded test result` when omitted, and `rpu` always injects `rpu-req-id=<uuid>` plus `skippedIsNotIssue=true` (system) if absent.
- `processing.apply_tfa` / `processing.apply_llm_analysis` — parsed but not executed in this CLI; a warning is emitted if `true` so pipelines can see the gap.

Fixtures in `test/testdata/metadata_*.json` show full, disabled, and updater-disabled shapes.

### Template variables

Metadata files can be Go `text/template` templates. Pass values with repeated `--var`:

```bash
rpu send \
  --metadata metadata.json \
  --results "results/*.xml" \
  --var HOST=reportportal.example.com \
  --var PROJECT=demo
```

Inside `metadata.json` (still under the required `targets.reportportal` wrapper):

```json
{
  "targets": {
    "reportportal": {
      "config": { "hostname": "{{.HOST}}", "project": "{{.PROJECT}}" }
    }
  }
}
```

Both `key=value` and `key:value` separators are accepted. Missing keys fail the run (`missingkey=error`).

### CLI

```bash
rpu send --metadata <path> --results <glob> [flags]
```

Required:

- `--metadata` — path to the JSON file (validated against `..` traversal).
- `--results` — `filepath.Glob` pattern for xUnit files (see glob notes below).

Optional:

- `--attachments <dir>` — directory that holds per-test-case attachment files. Only meaningful with `--results`.
- `--var key=value` (repeatable) — template variables.
- `--launch-url-file <path>` — write the resulting launch URL to a file for downstream CI steps.

Global flags (both commands): `--token`, `--http-timeout`, `--keep-workdir`, `--verbose` / `--verbosity`, `--output` / `--json` — see [Common flags and output](#common-flags-and-output).

Example:

```bash
REPORTPORTAL_TOKEN=$TOKEN rpu send \
  --metadata metadata.json \
  --results "test-results/*/*.xml" \
  --attachments test-attachments \
  --launch-url-file /tmp/rp-url.txt \
  --json
```

---

## Workflow 2 — Expanded standalone: `rpu upload`

Use this when you do not need to preserve the Data Router JSON contract and want a leaner, CI-native config.

### When to use

- Greenfield CI or a pipeline you control end-to-end.
- Multiple XML sources that need different lane tags (e.g., `gm`, `interop`, shared).
- You want `rpu` to do lane labeling, entity sanitization, and time normalization without external preprocessing.
- You prefer TOML and stdin support over templated JSON.

### TOML config

Loaded via `--config path/to/config.toml` or `--config -` (stdin). The token is never stored here.

Full example with every section:

```toml
[report_portal]
hostname = "reportportal.example.com"  # required; bare host or https://...
project = "my-project"                # required

[launch]
name = "nightly"                      # required
description = "main, nightly trigger"
attributes = { branch = "main", team = "sre" }  # map[string]string, optional

# one or more sources; order is preserved
[[sources]]
lane = "gm"                           # optional; empty means no lane tagging
path = "xml/gm/*.xml"                 # required; Go filepath.Glob pattern

[[sources]]
lane = ""
path = "xml/shared/*.xml"

[processing]
lane_property = "ci-lane"             # default "ci-lane"
property_filter = ["inventory-.*"]    # extra RE2 full-string patterns; lane_property is always prepended
testcase_concat_char = ""             # optional single char, same rules as send
exclude_patterns = ["*report_testrun*"] # default ["*report_testrun*"]; matched against basename

[attachments]
directory = "test-attachments"        # optional; per-test-case files

[output]
launch_url_file = "/tmp/rp-launch-url.txt"  # optional; TOML-only for `rpu upload` (use `rpu send --launch-url-file` for the send workflow)
```

Config schema at a glance:

| Section | Field | Required | Default | Notes |
|---|---|---|---|---|
| `report_portal` | `hostname` | yes | — | Normalized to `https://host/` if no scheme; trimmed and slashed. |
| `report_portal` | `project` | yes | — | Report Portal project slug/name. |
| `launch` | `name` | yes | — | Empty string rejected. |
| `launch` | `description` | no | `""` | Free-form launch description. |
| `launch` | `attributes` | no | `{}` | Map of string to string; blank keys/values dropped; `rpu-req-id` + `skippedIsNotIssue=true` injected. |
| `sources` | `path` | yes | — | At least one entry required; glob semantics below. |
| `sources` | `lane` | no | `""` | `""` disables lane labeling for that entry. |
| `processing` | `lane_property` | no | `"ci-lane"` | Property name used when `lane` is set. |
| `processing` | `property_filter` | no | `[]` | Additional RE2 patterns; effective filter is `[lane_property] + property_filter`. |
| `processing` | `testcase_concat_char` | no | `""` | Single char, same forbidden-char set as `rpu send`. |
| `processing` | `exclude_patterns` | no | `["*report_testrun*"]` | `filepath.Match` on basename; matched files are skipped with a warning. |
| `attachments` | `directory` | no | `""` | Base dir for attachment discovery. |
| `output` | `launch_url_file` | no | `""` | TOML-only for `rpu upload`; `rpu send` also supports `--launch-url-file` flag. |

Validation errors include: missing required fields, empty `sources`, empty `sources[].path`, invalid RE2 in `property_filter` (wrapped as `invalid property_filter pattern "…"`) and illegal `testcase_concat_char`.

### Source globs (`[[sources]].path`)

Each `[[sources]].path` is expanded with Go's `filepath.Glob`:

- Supports `*` (any run) and `?` (single char).
- **No** `**` recursion and **no** brace expansion (`{a,b}`).
- A literal file path is fine when it exists.
- Zero matches for an entry is a hard error: `matched no files (Go glob: * and ? only; no ** or brace expansion)`.
- Directories are silently ignored; only files proceed.
- The same file may appear in multiple `[[sources]]` entries — each appearance becomes a separate record (with its own lane tag), and `WorkdirName` deduplicates on disk as `<index>_<short-uuid>_<basename>`.
- At least one valid XML must survive preparation and exclusion, otherwise `no valid XML files after preparation`.

Example from the repo:

```toml
[[sources]]
lane = "gm"
path = "xml/gm/*.xml"

[[sources]]
lane = ""
path = "xml/shared/*.xml"
```

Tip: read config from a generated file or heredoc without touching disk:

```bash
cat <<TOML | REPORTPORTAL_TOKEN=$TOKEN rpu upload --config - --json
[report_portal]
hostname = "reportportal.example.com"
project = "demo"
[launch]
name = "pr-1234"
[[sources]]
path = "results/*.xml"
TOML
```

### Lane and transform behavior

Layer 2 transforms run after glob expansion and exclusion, before the shared pipeline:

1. **Lane labeling** — for each file whose `[[sources]].lane` is non-empty, every `<testsuite name>` is prefixed as `[<lane>] <name>` (if not already) and a property `<property name="<lane_property>" value="<lane>">` is ensured on each suite.
2. **System stream sanitization** — `&gt;`/`&lt;`/`&#xA;` are decoded in `<system-out>`/`<system-err>` bodies, then every `<`/`>` is stripped and runs of `<<<<`/`>>>>` collapsed.
3. **Attribute-to-property migration** — `status` on `<testcase>` and `disabled` on `<testsuite>`/`<testsuites>`, plus child elements `<results_directory>`/`<results_summary>` (and their grandchildren), are hoisted to `<property>` elements.
4. **Time normalization** — `time` attributes on `<testsuite>`/`<testcase>` must be parseable as float (after removing commas); otherwise they are reset to `"0.0"` with a warning that includes the file basename and element path.

Files that fail `parsing document` are skipped with `Ignoring file <basename>: …` and the rest of the batch continues.

### CLI

```bash
rpu upload --config <path|->
```

Required:

- `--config` — TOML path or `-` for stdin.

Optional global flags as with `rpu send`. For `rpu upload`, set `output.launch_url_file` in TOML to write the launch URL to a file (no CLI flag); for `rpu send`, pass `--launch-url-file <path>` to write the launch URL to a file (no TOML field).

Examples:

```bash
# minimal
REPORTPORTAL_TOKEN=$TOKEN rpu upload --config config.toml

# inspect what would be zipped
rpu upload --config config.toml --keep-workdir --verbose
# logs the temp dir: keeping work directory path=/tmp/rpu-workdir-...

# JSON output for CI
rpu upload --config config.toml --json > result.json
```

---

## What both workflows share

### Pipeline

```
Parse config/metadata → expand globs → Layer 2 transforms (upload only)
  → stage XML → Layer 1 prepare → zip → POST /api/v1/{project}/launch/import (junit plugin)
    → resolve launch UUID (with 504 recovery) → PATCH /launch/{id} (attributes)
    → reconcile test items (when enabled): list items, patch attributes, upload attachment logs
  → write launch URL → print result
```

- **Workdir** — `os.MkdirTemp("", "rpu-workdir-*")` with `results/` inside. Removed on return (both success and failure) unless `--keep-workdir` is set (then logged). In `rpu send`, prepared XML keeps the source basename; in `rpu upload`, it is `<index>_<short-uuid>_<basename>` so duplicate basenames never clash.
- **ZIP** — every prepared XML is added with basename (send) or unique internal name (upload) under deflate; the Report Portal `junit` plugin imports from this single ZIP.
- **504 recovery** — if the import returns 504/`GatewayTimeoutError` or an empty UUID, `rpu` fetches `GetLatestByName` and checks the latest launch: if it carries `rpu-req-id`, the value must exactly match the current invocation's `RequestID` (mismatch is a hard error); if no `rpu-req-id` is present, the launch is accepted when `LastModified` is missing (cannot be age-checked) or within 50 seconds, otherwise it is rejected as stale. Otherwise the error surfaces. Each invocation generates a fresh `rpu-req-id`, so a retry cannot correlate with the prior run's launch.
- **Launch URL** — `LaunchUIURL` builds `https://<host>/ui/#<project>/launches/all/<id>` and is written to stdout (table mode), JSON `launchUrl`, and optionally a file (`rpu send --launch-url-file` flag or `rpu upload` `output.launch_url_file` in TOML).

### XML preparation (Layer 1)

Runs on every file before zipping, for both commands (via `upload` after Layer 2):

- Removes empty `timestamp` attributes and strips commas from `time` values (retaining the attribute with a comma-free value).
- Populates `<failure>` bodies as `type:\n\nmessage` when child text is missing (empty `<error>` elements are left unchanged).
- Drops `<property>` without name/value, or with name over 128 bytes (`len()` byte count), and warns; every value over 128 bytes (`len()` byte length) is skipped (removed from the import). Warnings for long values are suppressed only for `java.class.path`, `surefire.test.class.path`, and `sun.java.command` (long values still skipped); empty-value warnings are suppressed only for `line.separator` and `sun.cpu.isalist`.
- Concatenates `classname + concatChar + name` on `<testcase>` when `testcase_concat_char` is set.
- Removes empty `<testsuite>` subtrees.
- Errors if no valid `<testcase>` remains; the file is then skipped with a warning and the rest of the batch continues. If all files are skipped, the run hard-fails.

### Post-import reconciliation

After the launch exists:

- If `disable_testitem_updater` / `DisableTestItemUpdater` is `true`, reconciliation is skipped.
- Otherwise `rpu` paginates `ListLaunchItems` / `ListChildItems`, matches local `TestItem` tree (built from prepared XML) to server items — suites by `name` plus duplicate index, child test cases by exact `name` only (`className` and `id` are used only for attachment discovery, not for matching) — then:
  - patches each matched item whose `<property>` matched `property_filter` (including `attachment`),
  - resolves attachments by looking under `<attachmentsDir>` in variants: `<name>`, `<sha256(name)>`, `<xmlBasename>/<name>`, `<xmlBasename>/<sha256>`, sanitized slash-replaced names, and `id`-based dirs (synchronous discovery while building the test-item tree),
  - uploads matched files as item logs (property and log updates run with concurrency 10).

Warnings from every stage are merged and surfaced.

### Common flags and output

Global flags on `rpu` (both subcommands):

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--token` | `REPORTPORTAL_TOKEN` | `""` | Report Portal API token (**required** either way). |
| `--http-timeout` | — | `0` (none) | `http.Client` timeout for all RP calls. |
| `--keep-workdir` | — | `false` | Keep the temp workdir and log its path. |
| `--verbose` / `-v` | — | `false` | Shorthand for `--verbosity=2`. |
| `--verbosity` | — | `0` (silent) | `0` silent, `1` info, `2` debug, `3` trace (takes precedence over `-v`). |
| `--output -o` | — | `table` | `table`, `json`, or `go-template=<tmpl>`. |
| `--json` | — | `false` | Shorthand for `--output=json`. |

Output:

- Table mode (default) — the table printer is called with no rows/headers (producing a blank line) and then the launch URL is printed on its own line.
- JSON mode (`--json` / `--output=json`) — `{"launchUrl","launchId","launchUuid","warnings,omitempty","disabled,omitempty","message,omitempty"}`. Errors are printed as `{"error":"…"}` to stderr.
- `rpu send` in disabled mode prints `Report Portal target disabled in metadata; skipping upload` (JSON-or-text aware).
- Warnings go to stderr as `Warning: …` and are also returned in `warnings`.

Path safety: traversal-checked paths (`--metadata`, expanded `--results` files, `--config` file, and `--launch-url-file`) are rejected if `filepath.Clean` contains `..`. `--attachments` and TOML `sources[].path` / `attachments.directory` are not traversal-checked; they flow directly into file access (attachment discovery performs only a lexical `filepath.Rel` check that resolved paths stay under the attachments directory; symlinks are not resolved and symlink escape is not prevented).

---

## Choosing a workflow

```text
Do you have pipelines that emit Data Router metadata.json and cannot change now?
  yes -> rpu send  (pin property_filter, concat char, and templates in metadata; migrate later)
  no  -> rpu upload (single TOML, multiple sources with lanes, no templating)
```

You can run both in the same repo: keep `rpu send` for legacy jobs and adopt `rpu upload` for new ones. They do not share config files.

---

## Troubleshooting

- `no valid XML files after preparation` — every file was excluded or failed prepare. Check `--results` / `[[sources]].path` patterns, `exclude_patterns`, and XML validity (run `xmllint` or `rpu … --verbose`).
- `results glob "…" matched no files` / `sources[…].path "…" matched no files` — literal typos or empty directories; remember no `**`.
- `config is nil` / `parsing config TOML` — malformed TOML; validate with `cat config.toml | toml-lint` or `go run github.com/BurntSushi/toml/cmd/tomlv`.
- `invalid property_filter pattern "…" : …` — RE2 compilation failed; use Go RE2 syntax and expect full-string anchoring (`"prefix-.*"` matches `prefix-foo`, not `x-prefix-foo`).
- `testcase_concat_char contains forbidden character` — one of `< > " ? * '` was used; pick a safe separator like `.`, `:`, or `/`.
- `gateway timeout during import` that then fails recovery — the launch may not have been created; you may retry the run, but each invocation generates a new `rpu-req-id` so the retry will not match the prior launch's request ID — a mismatch surfaces as `request ID on latest launch does not match`; check Report Portal for a recent launch with the expected name and handle duplicates manually.
- `no API token configured` — set `REPORTPORTAL_TOKEN` or `--token`.
- `matched no files (Go glob: * and ? only; …)` — switch to an explicit file list or multiple `[[sources]]` entries instead of `**`.

---

## Development

### Layout

```
cmd/rpu              entry point (cobra root)
internal/cli         CLI wiring: root, send, upload, core/{output,logging,errors,group}
internal/send        shared pipeline: metadata, prepare, import, postimport, zip, warnings
internal/upload      Layer 2: TOML config, sources/globs, transforms, lane labeling
internal/xunit       XML tree parser/encoder and helpers
test/testdata        fixtures: results/*.xml, upload/*.xml, metadata_*.json
test/integration     Ginkgo suite against a real Report Portal (docker-compose.test.yml)
```

### Testing

```bash
make test-unit          # go test ./...
make test-integration   # cd test/integration && go test -tags=integration -v ./...
```

### CI

`.github/workflows/ci.yml` runs on `push`/`pull_request` to `main` (markdown-only changes ignored for `pull_request` only; `push` to `main` still runs):

1. `golangci-lint v2.15.0` on root and on `test/integration`.
2. `make test` (unit).
3. Integration tests with coverage and Ginkgo JSON report (`-ginkgo.json-report=integration-report.json`), then a step that fails if any spec is not `passed`.

`.github/workflows/container-image.yaml` builds the UBI 9 micro image on `push` to `main` (`:latest` and `:sha-<commit>`) and on `v*` tags (`:v*` and `:sha-<commit>`) and pushes to Quay.

### Linting and formatting

```bash
make lint   # golangci-lint-v2 run --fix ./... (both modules)
make tidy   # go mod tidy for both modules
```

### Contributing

1. Create a branch, run `make lint && make test-unit`.
2. For new transforms or filters, add fixtures under `test/testdata` and golden tests beside the pipeline code.
3. Avoid adding secrets to TOML; prefer `REPORTPORTAL_TOKEN` in CI.
4. Do not change git remotes unless asked; the project's remote is SSH-based.

---

## License

> [!IMPORTANT]
> This is not an official Red Hat product.

Apache License 2.0 — see [LICENSE](LICENSE). Copyright 2026 Red Hat, LLC.
