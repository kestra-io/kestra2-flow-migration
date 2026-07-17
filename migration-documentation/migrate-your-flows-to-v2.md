# Migrate Your Kestra Flows from v1.3 to v2.0

This guide walks you through migrating your Kestra flow definitions from v1.3 to v2.0 using the `kestra-migrate` CLI tool and `kestractl`.

## Prerequisites

- **kestra-migrate** — the migration CLI (download from [Releases](https://github.com/kestra-io/kestra2-flow-migration/releases) or build from source with `go build -o kestra-migrate .`)
- **kestractl** — the Kestra CLI, authenticated against your v2 instance (`kestractl config set-context ...` or pass `--host` and `--token` flags)
- Your v1.3 flow YAML files on disk (exported from your v1 instance or from a Git repository)

## Overview

The migration workflow has three steps:

1. **Check** — audit your flows to see what needs to change
2. **Migrate** — auto-rewrite flows to v2 format
3. **Validate & Deploy** — validate against your v2 instance, then deploy

```
v1.3 flows ──► kestra-migrate ──► v2.0 flows ──► kestractl validate ──► kestractl deploy
```

## Step 1: Check your flows

Run `kestra-migrate --check` to audit your flows without modifying anything:

```bash
kestra-migrate --check ./flows/
```

Each flow is printed with a status:

| Symbol | Meaning |
|--------|---------|
| **green tick** | Already v2-compatible, no changes needed |
| **yellow edit** | Needs migration — a unified diff shows the exact changes |
| **red cross** | Contains a removed type that requires manual rewriting |

The tool exits with code 1 if any flows need migration, and prints a summary:

```
⚠  25/386 flows need migration
```

## Step 2: Migrate your flows

Run `kestra-migrate` with the `--out` flag to write migrated flows to a directory:

```bash
kestra-migrate --out v2-flows/ ./flows/
```

This applies all automated migration rules and writes the results to `v2-flows/`, preserving the directory structure. Flows that are already v2-compatible are copied as-is without reformatting.

### What gets migrated automatically

The tool handles the following v1 → v2 changes:

- **Input renames**: `name` → `id`, `BOOLEAN` → `BOOL`, `ENUM` → `SELECT`
- **Property renames**: `maxAttempt` → `maxAttempts`, `taskDefaults` → `pluginDefaults`, `scheduleConditions` → `conditions`
- **Pause task**: `delay` → `pauseDuration`
- **fetchType normalization**: `fetchType: STORE` → `store: true`
- **Type renames**: 60+ type renames including `Template` → `Subflow`, `Echo` → `Log`, `io.kestra.core.*` → `io.kestra.plugin.core.*`, State Store → KV Store, storage aliases (`storage.Purge` → `execution.PurgeExecutions`), and third-party plugin renames (notifications, Slack, Kubernetes, Datagen, AstraDB, fs.http, log.Fetch). Flow-iteration types (`ForEach`, `ForEachItem`, `EachSequential`, `EachParallel`) are **not** auto-renamed — they are removed in v2 in favor of `Loop` and flagged for manual rewrite
- **Condition suffix removal**: `ExecutionStatusCondition` → `ExecutionStatus`
- **Property removals**: `Subflow.outputs`, `Schedule.backfills`, trigger `minLogLevel`, `options.connectionPoolIdleTimeout`
- **PurgeKV**: deprecated `expiredOnly: <x>` → `behavior: {type: key, expiredOnly: <x>}` (preserves purge-everything semantics for `false`)
- **Exit state**: `CANCELED` → `CANCELLED` (the v2 spelling of the same state)
- **Worker groups (EE)**: `workerGroup: {key, fallback}` → `workerSelector: {tags: [<key>], fallback}`, pinning `fallback: WAIT` when absent (v1 waited by default; v2 fails). Templated or non-RFC-1123 keys are flagged for manual rewrite instead.
- **MULTISELECT inputs**: `options` → `values`
- **HTTP auth migration**: `basicAuthUser`/`basicAuthPassword` → `options.auth` block
- **Input defaults constraint**: removes `required: false` on inputs with `defaults` (v2 requires these to be required)
- **Reserved flow IDs**: appends `-flow` suffix to IDs that clash with v2 reserved keywords
- **Template tasks**: renames `templateId` → `flowId`

### What requires manual rewriting

Some changes cannot be automated. The tool will print warnings (yellow `⚠` in migration mode, red `✗` in check mode) for flows that use removed types with no drop-in replacement:

- `MultipleCondition` — rewrite using multiple `Flow` triggers or combine conditions directly
- `Count` — use KV Store or custom logic
- `Resume` — use the SDK to manipulate execution states
- `Toggle` — use the API or SDK to enable/disable triggers
- `git.Push` — use `SyncFlows` or Git API tasks
- `nashorn.Eval` / `nashorn.FileTransform` — migrate to GraalJS or other script tasks

Additionally, these structural changes are not automated and may need manual attention:

- **Listeners** → rewrite as separate flows with `Flow` triggers
- **`runner` → `taskRunner`** with `docker.image` → `containerImage` restructuring
- **Recursive Pebble rendering** → wrap with `{{ render(...) }}`
- **`LocalFiles`/`outputDir`** → `inputFiles`/`outputFiles` on `WorkingDirectory` (the type rename is automated, but the property restructuring is not)
- **Core script task type renames** (`io.kestra.core.tasks.scripts.*` → `io.kestra.plugin.scripts.<lang>.*`)
- **Pebble `read()`/`fileURI()` `version=` argument** → renamed to `revision=` in v2 with no fallback; the tool warns when it detects the old argument but cannot safely rewrite inside expressions

For the full list of breaking changes, see [flows-changes.md](./flows-changes.md).

## Step 3: Validate against your v2 instance

Use `kestractl flows validate` to check your migrated flows against a running Kestra v2 instance:

```bash
kestractl flows validate v2-flows/
```

This sends each flow to the v2 server for validation and reports constraint violations. It catches issues that static migration cannot detect, such as missing plugins, null required fields, or invalid task configurations.

Use `--output json` for machine-readable output:

```bash
kestractl flows validate v2-flows/ --output json
```

Fix any reported errors, then re-validate until all flows pass.

## Step 4: Deploy to your v2 instance

Once validation passes, deploy the migrated flows:

```bash
kestractl flows deploy v2-flows/
```

To update flows that already exist on the instance:

```bash
kestractl flows deploy v2-flows/ --override
```

To deploy into a specific namespace (overriding what's in each flow file):

```bash
kestractl flows deploy v2-flows/ --namespace production
```

To stop on the first error instead of processing all files:

```bash
kestractl flows deploy v2-flows/ --fail-fast
```

## End-to-end example

```bash
# 1. Export your v1.3 flows to a local directory
#    (e.g. from Git, or use kestractl flows list + get on your v1 instance)

# 2. Check what needs migration
kestra-migrate --check ./v1-flows/

# 3. Migrate
kestra-migrate --out ./v2-flows/ ./v1-flows/

# 4. Address any warnings printed during migration (manual rewrites)

# 5. Validate against v2
kestractl flows validate ./v2-flows/

# 6. Deploy
kestractl flows deploy ./v2-flows/ --override
```

## Troubleshooting

### `kestra-migrate` reports warnings but still writes the file

This is expected. Warnings indicate flows that contain removed types (`MultipleCondition`, `dbt.Build`, etc.) which must be rewritten manually. The tool migrates everything it can and flags what it cannot.

### `kestractl flows validate` fails with "Invalid type"

The flow uses a type that was removed in v2 or a plugin that is not installed on your v2 instance. Check the [flows-changes.md](./flows-changes.md) reference for the replacement, or install the required plugin.

### `kestractl flows validate` fails with "must not be null"

A required property (e.g. a connection host) is set to a `secret()` or variable that the validation server cannot resolve. This is typically fine — the flow will work at runtime when secrets are configured. You can safely ignore these during validation.

### `kestractl flows validate` fails with "reserved keyword"

The flow ID conflicts with a v2 reserved keyword. `kestra-migrate` handles this automatically by appending `-flow`, but if you see this on flows that weren't migrated, rename the flow ID manually.

### Flows are byte-identical but `--check` reports them as needing migration

The flow contains a removed type that triggers a validation warning. Run `--check` and look for red `✗` lines to see which types are flagged.
