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

Flows that use types removed in v2 with no automated replacement (e.g. `MultipleCondition`, `nashorn.Eval`) are still migrated as far as possible, but produce warnings:

- In `--check` mode: red `✗` markers under the affected flow
- In migration mode: yellow `⚠` warnings on stderr, file is still written

These flows must be rewritten manually.

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
