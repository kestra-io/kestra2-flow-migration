# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Purpose

CLI tool to migrate [Kestra](https://kestra.io/) flow definitions from v1 to v2 YAML format. Accepts local `.yml`/`.yaml` files or directories as input, rewrites flows where migration is needed, and outputs to a directory or stdout. Flows using removed types with no replacement are flagged with validation warnings.

## Commands

```bash
# Build
go build ./...
go build -o kestra-migrate .   # same binary that --check/-o use; rebuild before QA

# Run
go run . [flags] <file-or-dir...>

# Test
go test ./...
go test ./... -run TestName    # single test
go test ./... -v               # verbose

# Lint (golangci-lint)
golangci-lint run

# Format
gofmt -w .
```

### Debugging a single flow

Don't write a one-off Go program to call `migrate.Apply` — the CLI already does it. Reach for these first:

```bash
./kestra-migrate --check path/to/flow.yaml       # unified diff of required migration
./kestra-migrate        path/to/flow.yaml        # migrated result on stdout
/usr/bin/diff -u input.yaml <(./kestra-migrate input.yaml)   # custom diff view
cat -A path/to/flow.yaml                          # reveal trailing whitespace / tabs (forces yaml.v3 to quote scalars)
```

`internal/migrate` is an internal package — a helper in `cmd/` has to live under the same module, but there's no reason to create one; the CLI covers both "show the diff" and "show the output" modes.

## Architecture

The tool is structured around three concerns:

1. **Input resolution** — accepts local file paths or directories (walked recursively for `.yml`/`.yaml` files).

2. **Migration** — each flow YAML is parsed via `gopkg.in/yaml.v3` node trees and inspected for v1-specific constructs. A set of discrete, composable migration rules transforms the parsed structure to v2 syntax. Removed types with no replacement are detected and returned as validation warnings.

3. **Output** — migrated flows are written to a target directory (preserving subdirectory structure) or printed to stdout. Flows that require no changes are passed through as original bytes to preserve formatting.

### Package layout

```
main.go              CLI entrypoint
internal/
  input/             File and directory resolution
  migrate/           Migration rules (v1 → v2 transformations)
    migrate.go       Rule implementations + helpers
    migrate_test.go  Unit tests (~190 tests)
  output/            Write to dir or stdout
e2e/
  validate_test.go   E2E validation against a live Kestra v2 instance (build tag: e2e)
migration-documentation/
  flows-changes.md                Canonical list of breaking changes (source of truth for rules)
  migrate-your-flows-to-v2.md    Customer-facing migration guide
  archive/                        Research artifacts (commit extractions, etc.)
tools/
  comment-extend.py  Helper script for comment-based annotations
input-flows/         ~400 Kestra flow YAML files used as migration input corpus
```

## Workflow

Always run `/qa` before `git push`. Do not push if any QA stage fails.

## Key design decisions to maintain

- Migration rules live in `internal/migrate` and are applied per-flow; avoid coupling input/output logic to migration logic.
- YAML is round-tripped with comment preservation in mind (prefer `gopkg.in/yaml.v3` which supports node-level access).
- Unchanged flows return original bytes to avoid yaml.v3 round-trip formatting artifacts.

### yaml.v3 round-trip preservation (what `internal/migrate/restore.go` is for)

yaml.v3 is lossy on round-trip. It silently rewrites:

- Block scalars (`|`, `>`) → double-quoted with `\n` escapes, whenever the content has **trailing whitespace on a line**, **non-ASCII characters** (emoji → `\U…`), or in some cases `{{ … }}` expressions.
- Compact sequence indent (`tasks:\n- id:`) → child-indented (`tasks:\n  - id:`).
- Flow-style collections (`[a, b, c]`) normalized (spaces inside brackets stripped).
- Trailing whitespace on content lines stripped.

**We do not fight the emitter.** Instead:

1. `restoreUnchangedBlocks(original, migrated)` walks both parsed docs in parallel, matching mapping entries by key name and sequence items by index. For any subtree whose value is semantically equal, it replaces migrated bytes with original bytes at line-start granularity (preserving indent + `- ` prefix + between-entry comments verbatim).
2. `restoreBlankLines(original, migrated)` re-inserts blank lines. Assignments are **position-ordered**: each insert (in original order) claims the earliest migrated line ≥ cursor whose text matches the anchor. **Do not** revert to greedy first-match — with `restoreUnchangedBlocks` in place, anchor text collides (e.g. `for item in roadmap:` appearing in two script blocks) and greedy matching consumes the wrong slot.

**Known limitation.** Rules that rename a **container key** (e.g. `scheduleConditions` → `conditions`, `taskDefaults` → `pluginDefaults`) break the parallel walk on that key — subtree restoration can't match across the rename, so children still round-trip through yaml.v3. If noise reappears on these paths, add a key-alias map rather than making the walker fuzzy-match.

**Debugging restoration.** When a test/file shows unexpected noise:

- First, is a block scalar being mangled? `cat -A` the original to spot trailing spaces that force yaml.v3 to quote it.
- Second, is the anchor text for `restoreBlankLines` duplicated? `grep -c "^<anchor>$"` the original.
- The sequence of transforms is: rules mutate → `marshalYAML` → `restoreUnchangedBlocks` → `restoreBlankLines`. Bisect by commenting them out from the bottom up.

## Migration Reference

The full list of breaking changes from v1.3 to v2.0 is documented in `migration-documentation/flows-changes.md`. All migration rules must be derived from that document.

### Implemented rules (in `internal/migrate/migrate.go`)

Rules are applied in order via the `rules` slice. Each rule is a `func(*yaml.Node) error` that mutates the parsed YAML in-place.

| Rule | What it does |
|------|-------------|
| `renameInputNameToID` | Inputs: `name` → `id` |
| `renameInputTypes` | Inputs: `BOOLEAN` → `BOOL`, `ENUM` → `SELECT` |
| `renameMaxAttemptToMaxAttempts` | Retry: `maxAttempt` → `maxAttempts` (global) |
| `renamePauseDelayToPauseDuration` | Pause task: `delay` → `pauseDuration` |
| `normalizeFetchType` | `fetchType: STORE/FETCH` → `store: true` / `fetch: true` (specific plugins) |
| `renameTaskDefaults` | Top-level: `taskDefaults` → `pluginDefaults`. **Not** in the `rules` slice — v1-compat-only post-step in `Apply()`, applied **only** under `--stay-v1-compatible` (`pluginDefaults` is removed in v2; see note below) |
| `stripPluginDefaultsForced` | Removes `forced` from each `pluginDefaults` entry. Same gating as `renameTaskDefaults`, and runs after it. |
| `renameScheduleConditions` | Schedule trigger: `scheduleConditions` → `conditions` |
| `renameTypes` | 60+ type renames via `typeRenames` map (Template→Subflow, Echo→Log, state→kv, old `io.kestra.core.*` paths, condition types, storage aliases, log.Fetch→kestra.logs.Fetch, third-party plugin renames: notifications→slack/email/discord, slack internal restructure, kubernetes/datagen core subpackages, astradb→cassandra, fs.http→core.http). Also renames `templateId` → `flowId` on Template tasks. |
| `renameConditionSuffix` | Strips `Condition` suffix from `io.kestra.plugin.core.condition.*` types (excludes `MultipleCondition`) |
| `removeDeprecatedProperties` | Removes: `Subflow.outputs`, `Schedule.backfills`, `Schedule.backfill`, trigger `minLogLevel` |
| `renameExitCanceled` | Exit task: state `CANCELED` → `CANCELLED` (v2 enum spelling; **not** `KILLED`, which kills sibling tasks) |
| `migratePurgeKVExpiredOnly` | PurgeKV: deprecated `expiredOnly: <x>` → `behavior: {type: key, expiredOnly: <x>}` (blind removal was lossy for `false`). **Not** in the `rules` slice — gated post-step in `Apply()`, skipped under `--stay-v1-compatible` (`behavior` needs v1.3.28+) |
| `migrateWorkerGroupToWorkerSelector` | EE: `workerGroup: {key, fallback}` → `workerSelector: {tags: [<key>], fallback}`, pinning `fallback: WAIT` when absent (v1 waited by default, v2 fails). Templated / non-RFC-1123 keys and fallback-without-key produce warnings instead. Gated post-step in `Apply()`, skipped under `--stay-v1-compatible` |
| `renameMultiselectOptions` | MULTISELECT inputs: `options` → `values` |
| `migrateHTTPBasicAuth` | `options.basicAuthUser`/`basicAuthPassword` → `options.auth: {type: BASIC, username, password}` |
| `removeDeprecatedHTTPOptions` | Removes `options.connectionPoolIdleTimeout` from any task |
| `setLocalDeleteRecursive` | `io.kestra.plugin.fs.local.Delete`: adds `recursive: true` when absent (v2 default flipped to `false`); preserves v1 behavior, no-op on file targets |
| `renameChecksCondition` | Top-level `checks[]`: `condition` → `when` (scoped to `checks` only). **Not** in the `rules` slice — called as a gated post-step in `Apply()`, skipped under `--stay-v1-compatible` (v2-only construct) |
| `removeRequiredFalseWithDefaults` | Inputs: removes `required: false` when `defaults` is present (v2 requires inputs with defaults to be required) |
| `renameReservedFlowIDs` | Appends `-flow` to flow IDs that clash with v2 reserved keywords (`pause`, `resume`, `search`, etc.) |
| `migrateDbtBuildToDbtCLI` | Renames `io.kestra.plugin.dbt.cli.Build` → `io.kestra.plugin.dbt.cli.DbtCLI`, adds `commands: [dbt build]` when not already set (the old `Build` task ran `dbt build` implicitly), drops `dbtPath` (not a DbtCLI property), and promotes `dockerOptions.image` → `containerImage`. |

> **`pluginDefaults` is removed entirely in v2** (verified 2026-08-11 on kestra `releases/v2.0.x`: no `pluginDefaults` field on `Flow.java`; new `policyRefs` field). A flow carrying `pluginDefaults:` fails to parse on 2.0, and the replacement — an EE Policy or inlined task values — cannot be generated mechanically. So on the v2 path the block is **left untouched and flagged** via `detectPluginDefaults()`, exactly like the flow-iteration types. `renameTaskDefaults` / `stripPluginDefaultsForced` still run under `--stay-v1-compatible`, where `pluginDefaults` is a valid v1.3 keyword.

### Removed type detection (validation warnings)

`Apply()` returns `([]byte, []Warning, error)`. A `Warning` is a message plus a `V2Incompatible` flag: `true` when Kestra 2.0 **rejects the flow on save** (unknown type or property — `YamlParser` sets `FAIL_ON_UNKNOWN_PROPERTIES = true` — or a `FlowValidator` violation), `false` when the flow deploys and breaks at run time. Detectors keep returning `[]string`; `Apply` tags them via `v2Incompatible(...)` / `advisory(...)`. `detectPebbleVersionArg` and `detectSdkAuth` are the advisory detectors today.

Warnings cover types removed in v2 with no automated replacement; those flows are still written, but flagged for manual rewrite. Detected via the `removedTypes` map and `detectRemovedTypes()`.

Detected types: `MultipleCondition`, `Count`, `Resume`, `Toggle`, `git.Push`, `nashorn.Eval`, `nashorn.FileTransform`, and the flow-iteration types removed in favor of `Loop` (`ForEach`, `ForEachItem`, `EachSequential`, `EachParallel` — both old `io.kestra.core.tasks.flows.*` and new `io.kestra.plugin.core.flow.*` paths; warning-only, no auto-transform).

`detectPluginDefaults()` flags a flow-level `pluginDefaults:` / `taskDefaults:` block — removed in v2 at every scope, no mechanical replacement (EE Policies / inlined values). Warning-only; gated to the v2 path.

`detectSdkAuth()` flags tasks that call the Kestra API internally (`io.kestra.plugin.git.SyncFlows` / `NamespaceSync` / `SyncNamespaceFiles`, any `io.kestra.plugin.kestra.*`, `io.kestra.plugin.ai.KestraFlow`) and carry no inline `auth:` block — those calls were unauthenticated on v1.3 and 401 on v2. Advisory, not v2-incompatible: credentials may already be set at namespace/tenant or server level, which the flow file cannot show. Warning-only; gated to the v2 path. Note `renameTypes` moves `core.log.Fetch` → `kestra.logs.Fetch`, i.e. into the namespace that needs auth.

`detectPebbleVersionArg()` flags Pebble `read()`/`fileURI()` calls using the removed `version=` named argument (renamed to `revision` in v2 with **no fallback**, kestra PR #16699 / rc3). Warning-only — rewriting inside arbitrary expressions could corrupt embedded script code. Gated to the v2 path.

Beyond removed types, `detectMissingTriggerInputs()` emits a **semantic** validation warning: a `Schedule` trigger that fails to supply every flow input lacking a `defaults` (`prefill` / `required: false` do not count) — v2 rejects these with "Missing inputs for Schedule Trigger". Inputs gated by a `dependsOn` are skipped (conditionally required). Not auto-fixable (values can't be invented); gated to the v2 path (skipped under `--stay-v1-compatible`). Called from `Apply()` alongside `detectRemovedTypes`.

### `--disable-v2-incompatible`

Opt-in output mode (`migrate.DisableV2Incompatible()`, `internal/migrate/disable.go`). Flows with at least one `V2Incompatible` warning are rewritten into a deployable placeholder: `disabled: true`, the label `v2-migration: needs-manual-rewrite`, the reasons prepended to `description` under a `[kestra-migrate] NEEDS MANUAL REWRITE` marker, a stub `Fail` task, and the migrated definition appended as a comment block.

Constraints that shape the output — all verified against `releases/v2.0.x`:

- `Flow.tasks` is `@NotEmpty`, so a fully commented-out flow does not parse and its disabled/labelled state never becomes visible → the stub task is mandatory.
- Label keys are validated against `^[\p{Ll}][\p{L}0-9._-]*$` (`Label.java`), so the marker must be the pair `v2-migration: needs-manual-rewrite`, never one `key:value` string.
- Labels accept both map and list-of-`key`/`value` shapes (`ListOrMapOfLabelDeserializer`); `labelsWithMigrationLabel` mirrors whichever the flow already uses.
- yaml.v3 downgrades a `|` literal scalar to a `\n`-escaped double-quoted string when any line has trailing whitespace, hence `trimLineEnds` on the generated description. (Non-ASCII in a literal scalar is fine — the emoji problem described above applies to round-tripped block scalars, not to ones we construct.)

Idempotent by construction: a disabled flow has no live incompatible construct left, so it produces no warning and never re-enters `disableFlow`. Rejected together with `--stay-v1-compatible`.

### Not yet automated (require manual migration)

- **Listeners** → must be rewritten as separate flows with `Flow` triggers
- **`runner` → `taskRunner`** with `docker.image` → `containerImage` restructuring
- **Recursive Pebble rendering** → wrap with `{{ render(...) }}`
- **`LocalFiles`/`outputDir`** → `inputFiles`/`outputFiles` on `WorkingDirectory` (type rename is automated, property restructuring is not)
- **Script task type renames** (`io.kestra.core.tasks.scripts.*` → `io.kestra.plugin.scripts.<lang>.*`)

### Adding a new rule

1. **Document first** — add or update the entry in `migration-documentation/flows-changes.md` before writing code
2. Write the rule function in `migrate.go` following the `func(*yaml.Node) error` signature
3. Use the helpers: `walkMappings`, `stringValue`, `renameKey`, `removeKey`, `setStringValue`, `addBoolKey`
4. Append to the `rules` slice (order matters — prefix renames before specific type renames)
5. Add tests in `migrate_test.go` using the `apply(t, in)` helper
6. Update the rules table in this file
7. Run `go test ./internal/migrate/ -v`

### Key categories from the reference doc

- **Type renames:** `io.kestra.core.*` → `io.kestra.plugin.core.*` (conditions, triggers, runners, serdes, storage tasks)
- **Removed features:** Templates → Subflow, Listeners → Flow trigger, `LocalFiles`/`outputDir` → `inputFiles`/`outputFiles`, `runner` → `taskRunner`, State Store → KV Store
- **Input changes:** `name` → `id`, `BOOLEAN` → `BOOL`, `ENUM` → `SELECT`
- **Property renames:** `scheduleConditions` → `conditions`, `maxAttempt` → `maxAttempts`, `taskDefaults` → `pluginDefaults`
- **Property removals:** `Subflow.outputs`, `Schedule.backfills`, `PurgeKV.expiredOnly`, `AbstractTrigger.minLogLevel`
- **EE-only changes:** documented in `migration-documentation/flows-changes.md` under the EE section
