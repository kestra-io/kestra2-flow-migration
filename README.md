# kestra2-flow-migration

CLI tool to migrate Kestra flow definitions from v1.3 to v2.0 YAML format.

Applies 16 automated migration rules (type renames, property renames/removals, auth restructuring, and more) and flags flows that use removed types requiring manual rewriting.

For a detailed customer-facing walkthrough, see [Migrate Your Flows to v2](migration-documentation/migrate-your-flows-to-v2.md).

## Adding a new migration rule (Documentation Engineers)

Use the `/add-flow-migration-rule` Claude Code skill to contribute a new v1→v2 migration rule. It guides you through three steps:

1. **Describe the rule** — provide a plain-language description of what v1 construct changes and what the v2 equivalent is. The skill checks `migration-documentation/flows-changes.md` for duplicates or contradictions and appends the new entry to the correct section.
2. **Provide example flows** — paste or point to a v1 flow containing the construct, and then its v2-migrated equivalent. Both are validated as well-formed YAML and saved to `input-flows/additional-test-cases/` and `output-flows/additional-test-cases/`.
3. **Verify consistency** — the skill runs the migration tool against your input flow and diffs the output against your expected result, reporting **PASS** (already implemented) or **NEEDS IMPLEMENTATION** (with pointers to where to add the rule in code).

To start, open this repository in Claude Code and run:

```
/add-flow-migration-rule
```

## Install

### Quick install (macOS/Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/kestra-io/kestra2-flow-migration/main/install-scripts/install.sh | bash
```

Pin a version or override the install directory:

```bash
curl -fsSL https://raw.githubusercontent.com/kestra-io/kestra2-flow-migration/main/install-scripts/install.sh | VERSION=1.0.0 INSTALL_DIR=~/.local/bin bash
```

### Direct binary download

Grab the binary for your platform from the [releases page](https://github.com/kestra-io/kestra2-flow-migration/releases):

```bash
curl -fsSL -o kestra-migrate https://github.com/kestra-io/kestra2-flow-migration/releases/download/1.0.0/kestra-migrate_1.0.0_linux_arm64
chmod +x kestra-migrate
```

### From source

```bash
go build -o kestra-migrate .
```

## Usage

```
kestra-migrate [flags] <file.yml|dir>...
```

### Flags

| Flag | Description |
|------|-------------|
| `-o, --out <dir>` | Write each flow to a file in `<dir>` (preserves subdirectory structure). Default: stdout. |
| `--check` | Show migration status for each flow: green `✔` if v2-compatible, unified diff if not, red `✗` for removed types. Exits with code 1 when any flow needs migration. |
| `--stay-v1-compatible` | Skip migration rules whose output is not valid on a v1.3 instance, so the migrated flows can still be deployed to v1.3. |
| `--disable-v2-incompatible` | Rewrite flows that Kestra 2.0 would reject into a disabled placeholder labelled `v2-migration: needs-manual-rewrite`, keeping the original definition as comments. Off by default. |

### Examples

Check which flows need migration:
```bash
kestra-migrate --check ./flows/
```

Migrate flows to a directory:
```bash
kestra-migrate -o v2-flows/ ./flows/
```

Print a single migrated flow to stdout:
```bash
kestra-migrate my-flow.yaml
```

### Validation warnings

Flows using constructs the tool cannot rewrite are still migrated as far as possible, and produce a warning. Warnings come in two severities:

| Severity | Example | Effect on 2.0 |
|----------|---------|---------------|
| **v2-incompatible** (red `✗`) | a removed type such as `MultipleCondition` or `EachSequential`, flow-level `pluginDefaults`, a Schedule trigger missing an input | Kestra 2.0 **rejects the flow** — it cannot be deployed at all |
| **advisory** (yellow `⚠`) | `read()` / `fileURI()` using the removed `version=` argument; tasks requiring SDK authentication (Git sync, `io.kestra.plugin.kestra.*`, `ai.KestraFlow`) with no inline `auth:` | the flow deploys, but breaks at run time |

In `--check` mode both are printed under the affected flow; in migration mode both go to stderr and the file is still written. Either way, these flows must be rewritten manually.

### Keeping a bulk migration deployable (`--disable-v2-incompatible`)

A single v2-incompatible flow fails to deploy, which is easy to miss in a bulk `kestractl flows deploy` over hundreds of files. With `--disable-v2-incompatible`, those flows are instead rewritten into a placeholder that **does** deploy and is visible in the UI:

```yaml
id: nightly-rollup
namespace: company.team
disabled: true
labels:
  owner: data-team
  v2-migration: needs-manual-rewrite
description: |
  [kestra-migrate] NEEDS MANUAL REWRITE

  This flow is not compatible with Kestra 2.0 and was disabled by
  kestra-migrate. Kestra 2.0 rejects it because:
    - each uses io.kestra.plugin.core.flow.EachSequential (removed in v2; ...)
  ...
tasks:
  - id: needs_manual_rewrite
    type: io.kestra.plugin.core.execution.Fail
    errorMessage: This flow was not migrated to Kestra 2.0 automatically. See the flow description.

# ---------------------------------------------------------------------------
# Original definition below, migrated as far as kestra-migrate could take it.
# ...
```

Nothing is lost: the migrated definition is kept as comments (Kestra stores flow source verbatim, so it survives UI edits and Git sync), existing labels and the flow's own description are preserved, and the whole set is one label filter away in the UI. Re-running the tool over its own output is a no-op.

Flows carrying only advisory warnings are left alone. The flag cannot be combined with `--stay-v1-compatible`.

## Validate and deploy with kestractl

After migration, use [kestractl](https://kestra.io/docs/getting-started/kestractl) to validate against a running v2 instance:

```bash
kestractl flows validate ./v2-flows/
```

Then deploy:

```bash
kestractl flows deploy ./v2-flows/ --override
```

## Test

```bash
# Unit tests
go test ./...

# E2E validation against a live Kestra v2 instance (requires KESTRACTL_TOKEN)
go test ./e2e/ -tags e2e -v
```

## Architecture

```
main.go                     CLI entrypoint (cobra)
internal/
  input/input.go            Resolves file paths and directories into []Flow
  migrate/migrate.go        16 migration rules + removed type detection
  migrate/migrate_test.go   ~190 unit tests
  output/output.go          Writes flows to a directory or stdout
e2e/
  validate_test.go          E2E validation against live Kestra v2
migration-documentation/
  flows-changes.md          Canonical v1→v2 breaking changes reference
  migrate-your-flows-to-v2.md  Customer-facing migration guide
```

Migration rules live in `internal/migrate`. Each rule is a `func(*yaml.Node) error` applied in sequence by `migrate.Apply()`. The YAML is round-tripped via `gopkg.in/yaml.v3` node trees; unchanged flows return original bytes to preserve formatting.

## Migration reference

For the full list of what is and isn't automated, see [flows-changes.md](migration-documentation/flows-changes.md).
