Run kestractl to validate flows against the v2 Kestra instance and report failures.

The user may specify a target directory as an argument (e.g. `/validate-v2-flows input-flows`). If no argument is given, default to `./output-flows`.

Execute this command and save full output to a temp file (the JSON can be very large):

```
kestractl --token=$KESTRACTL_TOKEN --host=https://postgres-ee.preview.dev.kestra.io/ flows validate <target-dir> --output json > /tmp/validate-results.json 2>&1
```

Then parse and present the results as follows:

1. If `$KESTRACTL_TOKEN` is not set, stop and tell the user to export it: `export KESTRACTL_TOKEN=<your-token>`.

2. Parse the JSON output using `jq` (not Python). **Important:** kestractl appends a plain-text error summary line after the JSON array when there are failures. Use `jq` with `--slurp` or pipe through `head -n -1` to strip the trailing non-JSON line if needed. Each entry represents one flow file. Look for flows where `"success": false`. Ignore warnings, infos, deprecations, and outdated flags — these do not count as failures.

3. Report a summary:
   - Total flows validated
   - Number that passed
   - Number that failed

4. For each failed flow, show:
   - File path
   - Each constraint violation message

5. Categorize failures into three buckets:
   - **Migration-fixable:** issues that our migration rules should handle (deprecated properties, type renames, removed fields). Reference what rules exist or are missing.
   - **Known unfixable by migration:** flow content issues unrelated to v1→v2 migration (reserved flow IDs, null required fields, non-runnable tasks in WorkingDirectory, default values on non-required inputs).
   - **New/unknown:** anything not in the above two categories — flag these for investigation.

   Known reserved flow IDs in Kestra v2: `pause`, `resume`, `force-run`, `change-status`, `kill`, `executions`, `search`, `source`, `disable`, `enable`.

6. If all flows pass, say so clearly.

7. If the command itself errors (e.g. auth failure, host unreachable), report the raw error and stop.
