Guide a developer through adding a new Kestra v1→v2 flow migration rule: collect the rule description, validate it against the existing changelog, register it, collect example flows, and verify consistency.

---

## Step 1 — Rule description

Ask the user to describe the new migration rule in plain language. The description should cover:
- What v1 construct is being changed (type name, property name, behaviour)
- What the v2 equivalent is (rename, removal, structural change, etc.)
- Which section it belongs to (`## OSS changes`, `## Third-party plugin changes`, or `## EE changes`)

Once the user provides the description, read `migration-documentation/flows-changes.md` and check:

1. **No duplicate** — confirm no existing bullet already covers this change (same type or property).
2. **Coherent scope** — the change fits naturally in the target section and doesn't contradict any existing rule (e.g. a type that is already renamed cannot be renamed again).
3. **Correct section** — third-party plugins belong under `## Third-party plugin changes`, core OSS under `## OSS changes`, EE-only under `## EE changes`.

If the check fails (duplicate or contradiction), tell the user exactly which existing rule conflicts and stop.

If the check passes, append a new bullet to the correct section in `migration-documentation/flows-changes.md` following the exact style of the surrounding bullets (bold lead phrase, em-dash, plain-English description). Report the appended line back to the user.

---

## Step 2 — Example flows

### 2a — Legacy (v1) flow

Ask the user to paste or provide the path to an example v1 flow YAML that contains the construct targeted by the new rule.

- If the user provides a **file path**, read it. If the file does not exist, tell the user and ask again.
- If the user **pastes inline YAML**, write it to a temporary location and validate it there.

Validate the YAML is syntactically well-formed by running:
```bash
python3 -c "import sys, yaml; yaml.safe_load(sys.stdin)" < <file>
```
If validation fails, report the parse error and ask the user to fix and re-provide the flow.

Ask the user what filename to use (e.g. `my-rule-test.yaml`). Save the validated flow to:
```
input-flows/additional-test-cases/<filename>
```

Report the saved path.

### 2b — Migrated (v2) flow

Ask the user to provide the same flow but already migrated to v2 (i.e. the expected output after the rule is applied).

Apply the same YAML validation step. Save the validated flow to:
```
output-flows/additional-test-cases/<filename>
```
using the **exact same filename** chosen in step 2a.

Report the saved path.

---

## Step 3 — Consistency check

Run the migration tool on the saved input flow and compare the result against the expected output flow the user provided.

First, make sure the binary is up to date:
```bash
go build -o kestra-migrate .
```

Then run the migration:
```bash
./kestra-migrate input-flows/additional-test-cases/<filename>
```

Capture stdout and write it to a temp file, then diff against the saved expected output:
```bash
/usr/bin/diff -u output-flows/additional-test-cases/<filename> <(./kestra-migrate input-flows/additional-test-cases/<filename>)
```

Interpret the result:

- **No diff** — the tool already implements this rule correctly. Report PASS and note that no code change is needed for this rule (only the documentation and example files were added).
- **Diff present** — the tool does not yet implement the rule. Report the diff clearly and tell the developer:
  - The rule must be implemented in `internal/migrate/migrate.go` following the pattern in CLAUDE.md ("Adding a new rule").
  - Unit tests must be added in `internal/migrate/migrate_test.go`.
  - The rules table in `CLAUDE.md` must be updated.
  - After implementation, re-run this consistency check (step 3) to confirm the diff disappears.

End with a clear **PASS** or **NEEDS IMPLEMENTATION** verdict.
