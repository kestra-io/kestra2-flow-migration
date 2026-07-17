Full QA pipeline for the kestra2-flow-migration tool. Run all stages in order and report results.

## Stage 1: Unit tests

Run `go test ./...` (without the `e2e` build tag). All tests must pass. Report the count.

## Stage 2: E2E validation (optional)

If `$KESTRACTL_TOKEN` is set, run the `/validate-v2-flows` skill against `./output-flows`. If the token is not set, skip this stage and note it was skipped.

## Stage 3: Build CLI

Run `go build -o kestra-migrate .` and confirm it succeeds.

## Stage 4: Check mode

Run `./kestra-migrate --check input-flows/` and capture the output. Report:
- How many flows are already v2-compatible (green ticks)
- How many need migration (yellow, with diffs)
- The summary banner

## Stage 5: Migration run

Remove `output-flows/` and regenerate it:
```
rm -rf output-flows
./kestra-migrate --out output-flows input-flows/
```
Confirm it exits cleanly with no errors. Report the total number of output files.

## Stage 6: Diff analysis

Compare `input-flows/` against `output-flows/` to verify correctness.

**Important:** Always use `/usr/bin/diff` (absolute path) for all diff commands — never bare `diff` which may be aliased.

1. **File count parity:** confirm the same number of `.yaml`/`.yml` files exist in both trees.

2. **Unchanged files:** files where no migration rule applied must be byte-identical between input and output. Use `/usr/bin/diff -rq` and cross-reference with the `--check` output from Stage 4. If a file shows as v2-compatible in `--check` but differs in the diff, that is a **formatting regression** — flag it as a failure.

   Exact parity-check snippet (strips ANSI from `--check`, compares the two sets):
   ```bash
   /usr/bin/diff -rq input-flows output-flows | grep "^Files " \
     | sed 's|Files input-flows/\(.*\) and output-flows/.* differ|\1|' | sort > /tmp/diff-files.txt
   ./kestra-migrate --check input-flows/ 2>&1 | sed 's/\x1b\[[0-9;]*m//g' \
     | grep -oE '✎ \S+' | awk '{print $2}' | sort > /tmp/check-needs.txt
   comm -23 /tmp/diff-files.txt /tmp/check-needs.txt   # differ but not flagged → regression
   comm -13 /tmp/diff-files.txt /tmp/check-needs.txt   # flagged but identical → check bug
   ```
   Both `comm` calls must print nothing for PASS. Note the `^Files ` grep — `/usr/bin/diff -rq` also emits `Only in …` lines for files that exist on one side only, and those are not pair diffs.

3. **Changed files:** for each file that differs, show the diff with `/usr/bin/diff -u` and verify it contains only expected migration changes (type renames, property renames/removals, auth restructuring, etc.). To sample quickly:
   ```bash
   shuf /tmp/diff-files.txt | head -6 | while read f; do
     echo "=== $f ==="; /usr/bin/diff -u "input-flows/$f" "output-flows/$f" | head -25
   done
   ```
   Flag any unexpected changes:
   - Indentation changes on lines not touched by a rule
   - Blank line additions/removals unrelated to a removed property
   - String quoting style changes
   - Reordering of YAML keys

   Note: blank line removal adjacent to a migrated property is a known yaml.v3 round-trip artifact and is acceptable. Only flag blank line changes that are far from any migrated line.

4. Report a summary table:
   - Total files
   - Identical (no migration needed, byte-equal)
   - Correctly migrated (diff matches expected rules)
   - Formatting regressions (if any)

## Final verdict

Print a clear PASS or FAIL verdict. PASS requires:
- All unit tests pass
- CLI builds successfully
- No formatting regressions on unchanged files
- All diffs in changed files correspond to known migration rules
- File count is identical between input and output
