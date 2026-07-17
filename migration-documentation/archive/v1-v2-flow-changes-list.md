# flows migration v1.3 to v2.0 documentation

this document lists breaking changes in Flow definition between version 1.3 and 2.0.
Below is the list of properties to change to have a valid v2.0 Flow

## OSS changes
- **Core script tasks removed:** Replace legacy core script task types with the script plugin tasks and install the required plugins. Migrate to `io.kestra.plugin.scripts.shell.Commands` or `io.kestra.plugin.scripts.shell.Script` for Bash, `io.kestra.plugin.scripts.node.Commands` or `io.kestra.plugin.scripts.node.Script` for Node, and `io.kestra.plugin.scripts.python.Commands` or `io.kestra.plugin.scripts.python.Script` for Python.
- **Templates removed:** Replace `io.kestra.plugin.core.flow.Template` with `io.kestra.plugin.core.flow.Subflow`, and rename `templateId` to `flowId`. If you still have template definitions, convert them into standard flows and invoke them as subflows.
- **Listeners removed:** Move listener tasks into a separate flow and use a `io.kestra.plugin.core.trigger.Flow` trigger with the equivalent conditions. Update any listener conditions to trigger conditions on that new flow.
- **Non-recursive rendering is mandatory:** Recursive Pebble rendering is removed. Wrap variables that must be rendered recursively with `{{ render(...) }}` and remove reliance on implicit recursive rendering.
- **Inputs `name` removed:** Rename all input definitions to use `id` instead of `name`. You can use `displayName` input property for proper name displayed in the UI forms.
- **Schedule trigger `scheduleConditions` removed:** Rename `scheduleConditions` to `conditions` in `io.kestra.plugin.core.trigger.Schedule`.
- **Subflow task `outputs` removed:** Define outputs at the root level inside the child flow and access them via `{{ outputs.subflow.outputs.<id> }}` in the parent flow.
- **JSON serialization changed to NON_NULL:** Empty lists/maps are now defined. Update Pebble expressions that relied on `is defined` or `??` to handle empty values explicitly (for example, check `is empty`).
- **`LocalFiles` and `outputDir` removed:** Use `inputFiles` and `outputFiles` on `WorkingDirectory` and script tasks. Remove references to `{{ outputDir }}` and write files directly to the declared `outputFiles` paths.
- **Renamed core plugin classes required:** Update all `type` values that used `io.kestra.core.*` or older plugin paths to `io.kestra.plugin.core.*`, including conditions, triggers, runners, storage tasks, and serdes tasks (for example, `CsvReader` -> `CsvToIon`). Aliases are removed in 2.0.
- **`runner` removed:** Replace `runner` with `taskRunner` in script tasks. For Docker, move `docker.image` to top-level `containerImage`.
- **State Store tasks removed:** Replace `io.kestra.plugin.core.state.*` with KV Store tasks (`io.kestra.plugin.core.kv.*`).
- **Condition class suffix removed:** Use the new condition names without `Condition` (for example, `ExecutionStatusCondition` -> `ExecutionStatus`).
- **Git tasks default branch changed:** If you relied on the old default branch `kestra`, set `branch` explicitly or update to `main`.
- **Restart behavior for subflows changed:** To keep the old behavior when restarting parents, set `restartBehavior: NEW_EXECUTION` on `Subflow` and `ForEachItem` tasks.
- **Missing `secret()` now fails:** Ensure secrets exist or refactor flows to avoid optional secret lookups. Missing secrets now throw errors in OSS.
- **`kv()` now errors on missing keys:** Update expressions to `{{ kv('KEY', errorOnMissing=false) }}` where you want `null` instead of a failure.
- **Input type `BOOLEAN` removed:** Replace `BOOLEAN` with `BOOL` in input definitions.
- **Flow trigger reacts to `PAUSED`:** Add an explicit `ExecutionStatus` condition if you want only terminal states.
- **JDBC `autocommit` removed:** Remove `autocommit` from `Query` and `Queries` tasks.
- **LoopUntil defaults changed:** If you relied on the old limits, set `checkFrequency` explicitly in the task or via plugin defaults.
- **Python script default image changed:** If you depended on `ghcr.io/kestra-io/kestrapy:latest`, set `containerImage` or add `dependencies` for `kestra` and `amazon-ion`.
- **Script warnings no longer affect task state:** WARNING/ERROR logs on stderr no longer mark tasks as WARNING; use explicit failures or non-zero exit codes if needed.
- **LangChain4j plugin renamed:** Replace `io.kestra.plugin.langchain4j.*` with `io.kestra.plugin.ai.*` for tasks and providers.
- **Retry property renamed:** Replace `maxAttempt` with `maxAttempts`.
- **Dynamic input defaults:** Move Pebble expressions out of `defaults` and into task logic (for example, `{{ inputs.sessionId ?? execution.id }}`).
- **Reserved flow IDs disallowed:** Rename flows using reserved IDs (`pause`, `resume`, `force-run`, `change-status`, `kill`, `executions`, `search`, `source`, `disable`, `enable`).
- **Singer plugin removed:** Replace Singer flows with Airbyte, dlt, or CloudQuery tasks.
- **ForEachItem iteration starts at 0:** If you used `{{ taskrun.iteration }}`, adjust any off-by-one logic.
- **JDBC `Query` task single statement only:** Split into multiple `Query` tasks or use `Queries`.
- **Input `prefill` guidance:** If optional inputs used `defaults` but must be clearable, switch to `prefill` and remove `defaults`.

## EE changes
All OSS flow changes apply to EE. Additional EE-only flow changes are listed below.

- **Worker group fallback default:** Tasks wait by default when no workers are available. Set `workerGroup.fallback: FAIL` or `CANCEL` to restore old behavior.
- **Azure Log Exporter split:** Replace `io.kestra.plugin.ee.azure.LogExporter` with `io.kestra.plugin.ee.azure.monitor.LogExporter` or `io.kestra.plugin.ee.azure.storage.LogExporter`.
- **Cross-namespace `kv()` permission check:** Ensure the execution has access to the target namespace or update role bindings.
- **PurgeAuditLogs property rename:** Replace `permissions` with `resources` in `io.kestra.plugin.ee.core.log.PurgeAuditLogs` tasks.

