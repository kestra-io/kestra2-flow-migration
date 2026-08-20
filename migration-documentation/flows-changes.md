# REFERENCE Flows migration v1.3 to v2.0 changelog

this document lists breaking changes in Flow definition between version 1.3 and 2.0.
Below is the list of properties to change to have a valid v2.0 Flow

Last reconciled against the customer-facing v2.0.0 migration guide on **2026-08-11** — kestra-io/docs PR [#4566](https://github.com/kestra-io/docs/pull/4566) (`src/contents/docs/11.migration-guide/v2.0.0/`) — with disputed claims re-verified against the shipped `releases/v2.0.x` branch of kestra-io/kestra. Where the two disagree, this document follows the code and says so inline.

## OSS changes
- **Core script tasks removed:** Replace legacy core script task types with the script plugin tasks and install the required plugins. Migrate to `io.kestra.plugin.scripts.shell.Commands` or `io.kestra.plugin.scripts.shell.Script` for Bash, `io.kestra.plugin.scripts.node.Commands` or `io.kestra.plugin.scripts.node.Script` for Node, and `io.kestra.plugin.scripts.python.Commands` or `io.kestra.plugin.scripts.python.Script` for Python.
- **Templates removed:** Replace `io.kestra.plugin.core.flow.Template` with `io.kestra.plugin.core.flow.Subflow`, and rename `templateId` to `flowId`. If you still have template definitions, convert them into standard flows and invoke them as subflows.
- **Listeners removed:** Move listener tasks into a separate flow and use a `io.kestra.plugin.core.trigger.Flow` trigger with the equivalent conditions. Update any listener conditions to trigger conditions on that new flow.
- **Non-recursive rendering is mandatory:** Recursive Pebble rendering is removed. Wrap variables that must be rendered recursively with `{{ render(...) }}` and remove reliance on implicit recursive rendering.
- **Inputs `name` removed:** Rename all input definitions to use `id` instead of `name`. You can use `displayName` input property for proper name displayed in the UI forms.
- **Trigger `conditions` / Schedule `scheduleConditions` removed:** The whole `conditions` subsystem (including Schedule's `scheduleConditions`) is removed. Replace with a top-level `when` Pebble expression on the trigger. All `io.kestra.plugin.core.condition.*` and `io.kestra.core.models.conditions.types.*` types are removed. See the "Trigger conditions → `when` / `dependsOn`" section below for the full mapping. The migration tool rewrites `conditions` / `scheduleConditions` into a single top-level `when:` expression on non-Flow triggers (Schedule, Webhook, etc.) for the following condition types: `Expression`, `DayWeek`, `DayWeekInMonth`, `Weekend`, `PublicHoliday`, `DateTimeBetween`, `TimeBetween` (whole-hour boundaries only — sub-hour values would lose precision under `hourOfDay()` and are left as warnings), `HasRetryAttempt`, `Not` (with one or more inner conditions — v1's `Not([A, B])` evaluated the inner list as an AND and negated it, so the rewrite emits `not ((A) and (B))` to preserve that semantic), and `Or`. Lists of multiple conditions are joined with `and`. On Flow triggers, the tool rewrites `conditions` into a single `dependsOn:` entry whenever the list comprises only `ExecutionStatus`, `ExecutionFlow`, `ExecutionNamespace`, `ExecutionLabels`, `ExecutionOutputs`, and/or `HasRetryAttempt` (a top-level `states:` on the v1 trigger is folded into the new entry). A bare `ExecutionStatus` becomes a `dependsOn: [{states: [...]}]` entry — matching v1 semantics of "fire on any upstream execution with these states". A single-child `Not` wrapping one of the when-convertible condition types (`ExecutionNamespace` any shape, `ExecutionFlow`, `ExecutionOutputs`, or `HasRetryAttempt`) is also handled, producing `when: "{{ not (<inner>) }}"`. An `Or` wrapper whose children are all when-convertible is combined into `when: "(<A>) or (<B>) …"`; a sibling `ExecutionStatus` still contributes `states:` on the same dependsOn entry. A `preconditions:` block is rewritten when it contains only `id` (dropped), exactly one of `flows:` (fanned out — one `dependsOn` entry per flow) or `where:` (fanned out — one `dependsOn` entry per `where` entry, with filters AND-combined into a `when:` clause), and optionally a `timeWindow:` of type `DAILY_TIME_DEADLINE` (mapped to top-level `window: {deadline: ...}`). `where` filters are supported for `NAMESPACE` / `FLOW_ID` (with `EQUALS` / `NOT_EQUALS` / `STARTS_WITH` / `ENDS_WITH`) and `EXPRESSION` + `IS_TRUE`; other fields (`LABELS`, `STATE`) leave the rewrite as a warning. A compatible `conditions` list (only `ExecutionOutputs`, `HasRetryAttempt`, `ExecutionNamespace` prefix/suffix, or `Not` around one of those) may accompany `preconditions` as a shared `when:` clause duplicated across every entry. A `MultipleCondition` wrapper inside `conditions:` is fanned out the same way — each inner `ExecutionFlow` becomes its own `dependsOn` entry, `window`/`windowAdvance` (zero-advance only) lift to a top-level `window: {lookback: ...}`, and any sibling `ExecutionStatus` contributes shared states to every entry. Any other condition shape — `Or`, `Not` wrapping a field-producing condition (`ExecutionStatus`/`ExecutionFlow`/`ExecutionLabels`/exact `ExecutionNamespace`), `MultipleCondition`, `preconditions.where`, non-deadline `timeWindow` types, conflicting filters, or an existing `dependsOn:` — is left untouched and flagged with a validation warning because the rewrite requires a judgement call.
- **Flow trigger `preconditions` and `timeWindow` removed:** Replace `preconditions.flows` with a top-level `dependsOn` list on the Flow trigger. Replace `preconditions.timeWindow` with a top-level `window` block using one of: `deadline`, `from`/`to`, `every`/`offset`, `lookback`. `preconditions.id` is dropped. `preconditions.resetOnSuccess: true` is dropped, since v2 always resets the stored dependency results after the trigger has fired; `resetOnSuccess: false` has no equivalent and needs `mode: ANY`. The `MultipleCondition` wrapper is gone; each upstream dependency is its own `dependsOn` entry.
- **Flow trigger default states:** Re-verified in code on the shipped `releases/v2.0.x` branch (2026-08-11, `core/.../plugin/core/trigger/Flow.java`): the trigger-level `states` property is `@Builder.Default` to **all terminated states + `PAUSED`** (`ListUtils.concat(State.Type.terminatedTypes(), List.of(PAUSED))` → `SUCCESS, WARNING, FAILED, KILLED, CANCELLED, RETRIED, SKIPPED, PAUSED`), and a `dependsOn` entry's per-entry `states` (`Flow.Dependency.states`) carries **no `@Builder.Default`** — unset means no per-entry filter (`DependencyCondition` only filters when `dependency.states != null`). ⚠️ The customer-facing v2.0 guide (docs PR [#4566](https://github.com/kestra-io/docs/pull/4566), `trigger-conditions-redesign`) documents the `dependsOn` entry default as `[SUCCESS, WARNING]` and says it "changed from `[SUCCESS, WARNING, PAUSED]`" — that does **not** match the shipped code and should be treated as a docs bug. Either way the practical advice is the same: flows that want only success-ish transitions must set `states: [SUCCESS, WARNING]` explicitly on the trigger or on the `dependsOn` entry rather than relying on any default.
- **Trigger outputs are scoped by flow id:** Upstream outputs on Flow triggers are no longer deep-merged into a flat `trigger.outputs` map. Access them as `{{ trigger.outputs.<flowId>.<key> }}`. When there's only one upstream flow, unscoped access still works as a shorthand.
- **Subflow task `outputs` removed:** Define outputs at the root level inside the child flow and access them via `{{ outputs.subflow.outputs.<id> }}` in the parent flow.
- **JSON serialization changed to NON_NULL:** Empty lists/maps are now defined. Update Pebble expressions that relied on `is defined` or `??` to handle empty values explicitly (for example, check `is empty`).
- **ION output files are now binary:** ION task-output files are stored in binary format in v2, so `read()` on an ION URI returns `byte[]` instead of a `String`. Expressions that do string operations on `read(...)` (e.g. `contains`, embedding in a log/message, raw comparison) must wrap it: `fromIon(read(...))` for the first row or `fromIon(read(...), allRows=true)` for all rows. Not automated (expression-level) — review affected flows manually. Non-ION files (CSV/JSON/XML/YAML) are unaffected: `read()` still returns a `String` for them, and it distinguishes the two by inspecting the file header. `fromIon()` accepts both `String` and `byte[]`, so the wrapped form is safe on v1.3 and v2 alike. Tasks that emit ION URIs: `FileTransform` (all script runtimes), query tasks with `fetchType: FETCH` / `FETCH_ONE`, `io.kestra.plugin.core.storage.Write` with an `.ion` extension, and `Split` / `Concat` producing `.ion`. Existing text-format ION files written by 1.x are still readable by 2.0 (no data migration), but **2.0 backups cannot be restored by 1.x** — take a backup before upgrading if you may roll back. See `migration-guide/v2.0.0/ion-binary-format`.
- **`LocalFiles` and `outputDir` removed:** Use `inputFiles` and `outputFiles` on `WorkingDirectory` and script tasks. Remove references to `{{ outputDir }}` and write files directly to the declared `outputFiles` paths.
- **Renamed core plugin classes required:** Update all `type` values that used `io.kestra.core.*` or older plugin paths to `io.kestra.plugin.core.*`, including conditions, triggers, runners, storage tasks, and serdes tasks (for example, `CsvReader` -> `CsvToIon`). Aliases are removed in 2.0.
- **`runner` removed:** Replace `runner` with `taskRunner` in script tasks. For Docker, move `docker.image` to top-level `containerImage`.
- **State Store tasks removed:** Replace `io.kestra.plugin.core.state.*` with KV Store tasks (`io.kestra.plugin.core.kv.*`).
- **Condition class suffix removed:** Use the new condition names without `Condition` (for example, `ExecutionStatusCondition` -> `ExecutionStatus`).
- **Git tasks default branch changed:** If you relied on the old default branch `kestra`, set `branch` explicitly or update to `main`.
- **`local.Delete` `recursive` defaults to `false`:** `io.kestra.plugin.fs.local.Delete` now defaults `recursive` to `false` (was `true`), matching the other `plugin-fs` Delete tasks. Flows deleting a directory without an explicit `recursive` will silently stop removing subdirectory contents. Automated: `recursive: true` is added when the property is absent, preserving v1 behavior (harmless on file targets, where `recursive` has no effect). See `migration-guide/v2.0.0/local-delete-recursive-default`.
- **Restart behavior for subflows changed:** To keep the old behavior when restarting parents, set `restartBehavior: NEW_EXECUTION` on `Subflow` and `ForEachItem` tasks.
- **Missing `secret()` now fails:** Ensure secrets exist or refactor flows to avoid optional secret lookups. Missing secrets now throw errors in OSS.
- **`kv()` now errors on missing keys:** Update expressions to `{{ kv('KEY', errorOnMissing=false) }}` where you want `null` instead of a failure.
- **Input type `BOOLEAN` removed:** Replace `BOOLEAN` with `BOOL` in input definitions.
- **Flow trigger explicit states recommended:** the v2 default `states` list is broad (all terminated states + `PAUSED` — see "Flow trigger default states"). Flows that previously used an `ExecutionStatus` condition (removed) to restrict firing must set `states:` explicitly on the trigger or on the relevant `dependsOn` entry, e.g. `states: [SUCCESS, WARNING]`.
- **JDBC `autocommit` removed:** Remove `autocommit` from `Query` and `Queries` tasks.
- **LoopUntil defaults changed:** If you relied on the old limits, set `checkFrequency` explicitly in the task or via plugin defaults.
- **Python script default image changed:** If you depended on `ghcr.io/kestra-io/kestrapy:latest`, set `containerImage` or add `dependencies` for `kestra` and `amazon-ion`.
- **Script warnings no longer affect task state:** WARNING/ERROR logs on stderr no longer mark tasks as WARNING; use explicit failures or non-zero exit codes if needed.
- **LangChain4j plugin renamed:** Replace `io.kestra.plugin.langchain4j.*` with `io.kestra.plugin.ai.*` for tasks and providers.
- **Retry property renamed:** Replace `maxAttempt` with `maxAttempts`.
- **Dynamic input defaults:** Move Pebble expressions out of `defaults` and into task logic (for example, `{{ inputs.sessionId ?? execution.id }}`).
- **Reserved flow IDs disallowed:** Rename flows using reserved IDs (`pause`, `resume`, `force-run`, `change-status`, `kill`, `executions`, `search`, `source`, `disable`, `enable`). Append a `-flow` suffix to any flow whose `id` matches a reserved keyword.
- **Singer plugin removed:** Replace Singer flows with Airbyte, dlt, or CloudQuery tasks.
- **ForEachItem iteration starts at 0:** If you used `{{ taskrun.iteration }}`, adjust any off-by-one logic.
- **JDBC `Query` task single statement only:** Split into multiple `Query` tasks or use `Queries`.
- **Inputs with defaults must be required:** In v2, inputs that have a `defaults` value must be required (the default). If an input has both `defaults` and `required: false`, remove `required: false` so the input becomes required.
- **Input `prefill` guidance:** If optional inputs used `defaults` but must be clearable, switch to `prefill` and remove `defaults`. **Caveat:** do not do this for an input consumed by an automatic trigger (see next entry) — a `prefill`-only input has no `defaults` and will break scheduled executions.
- **Triggers must supply every input lacking a `defaults`:** In v2, a trigger that launches executions non-interactively — verified on `io.kestra.plugin.core.trigger.Schedule`, and by the same rule any automatic trigger — must be able to resolve every flow `input`. Any input without a `defaults` value must be provided by the trigger's `inputs:` map, **keyed by input `id`** (`inputs: {<inputId>: <value>}`). A `prefill` value and/or `required: false` do **not** satisfy this — `prefill` is only a UI hint for manual runs — so a v1 flow that scheduled fine with an unprovided optional/prefilled input is rejected by v2 with `Invalid Flow: Missing inputs for Schedule Trigger '<triggerId>', missing inputs: '<inputId>'`. (Note the v1 verbose trigger-input form `inputs: {name: <id>, value: <v>}` is read literally in v2 as inputs named `name`/`value`, so it also fails to supply `<id>` — rewrite to `inputs: {<id>: <v>}`.) **Not automated:** the migrator cannot invent input values, so affected flows are flagged with a validation warning; fix by adding a `defaults` to the input or supplying the value in the trigger's `inputs:`. Inputs gated by a `dependsOn` are only required when their condition holds, so they are **not** flagged (avoids false positives on conditional inputs).
- **Input type `ENUM` removed:** Replace `ENUM` with `SELECT` (single choice) or `MULTISELECT` (multiple choices) in input definitions.
- **`Echo` task removed:** Replace `io.kestra.plugin.core.debug.Echo` with `io.kestra.plugin.core.log.Log`.
- **Flow YAML expand helper removed:** The `[[>/path/to/file.txt]]` include syntax is no longer supported. Inline the content or use `Subflow` references instead.
- **`taskDefaults` / `pluginDefaults` removed entirely:** ⚠️ **Supersedes the earlier "rename `taskDefaults` → `pluginDefaults`" guidance.** In v2 the `pluginDefaults` keyword is removed at *all* scopes — flow level, namespace level (EE), tenant level (EE), and the global `kestra.plugins.defaults` server config. A flow carrying a `pluginDefaults:` (or `taskDefaults:`) block **fails to parse** on 2.0. Verified in code on `releases/v2.0.x` (2026-08-11, `core/.../models/flows/Flow.java`): the flow model has no `pluginDefaults` field, and a new `policyRefs: [<policyId>]` field is present. Replacements:
  - **EE** — [Policies](https://kestra.io/docs/enterprise/governance/policies). Each `pluginDefaults` entry becomes an `Add` rule (`type: io.kestra.plugin.ee.rules.Add`, `on: PLUGIN`, `where: [{field: type, operator: EQUAL_TO|STARTS_WITH, value: <type>}]`, `values: {...}`). `forced: true` → `override: true` on the rule. A `REFERENCE` Policy applies only to flows that opt in via `policyRefs:`; an `ACTIVE` Policy applies to every flow in scope. Existing **namespace-level** Plugin Defaults are auto-migrated to Policies during the upgrade; server-config and flow-level ones are not.
  - **OSS** — no centralized replacement: inline the values onto each task, or hoist them into a flow-level `variables:` block referenced as `{{ vars.x }}`.
  - Behavioral differences to check when porting: lists are **replaced**, not merged, when `override: true`; plugin **aliases are not resolved** (rule `type` matching is literal, so migrate flows to canonical type names first); `EVALUATE`-mode Policies inject nothing.
  - **Not automated** — the tool cannot invent a Policy resource from inside a flow file, nor know which tasks a `type:`-prefix default applied to. The block is left untouched and flagged with a validation warning for manual rewrite, like the flow-iteration types. Under `--stay-v1-compatible` the pre-v2 normalization still applies (`taskDefaults` → `pluginDefaults`, `forced` stripped) — both outputs remain valid on v1.3. See `migration-guide/v2.0.0/plugin-defaults-removed`.
- **`pluginDefaults.forced` removed:** the `forced` property is removed from flow-level `pluginDefaults` entries and causes a **hard parse failure** on its own. Removing it is now only an *interim* step: it leaves a `pluginDefaults:` block that still fails to parse on 2.0 (see the entry above), which is why the v2 path flags the whole block rather than stripping `forced` from it. Rationale for the removal: `forced: true` let any flow author override plugin defaults an administrator had set at namespace or tenant level. See `migration-guide/v2.0.0/plugin-defaults-forced-removed`.
- **`ForEach`, `ForEachItem`, `EachSequential`, `EachParallel` removed → `Loop`:** All four flow-iteration task types are removed in v2 and replaced by `io.kestra.plugin.core.flow.Loop`. This **supersedes** the earlier `EachSequential`/`EachParallel` → `ForEach` automation — `ForEach` is itself removed in v2. The migration tool does **not** auto-transform these; the rewrite is non-trivial. Flows using any of these types are flagged with a validation warning for manual rewrite. Confirmed against the shipped customer guide `migration-guide/v2.0.0/foreach-loop` (docs PR #4566): flows referencing `ForEach` / `ForEachItem` **fail to parse** on 2.0. The rewrite involves:
  - **Execution model:** each `Loop` iteration runs as an isolated **sub-execution** (`ForEach` ran every iteration as task runs inside the same execution). This is the reason for the change — a large `ForEach` could exhaust executor memory and destabilize the whole instance.
  - **Expressions:** `{{ taskrun.value }}` → `{{ item.value }}`; `{{ taskrun.iteration }}` → `{{ item.index }}` (zero-based in both); `parent.taskrun.value` / `parents[0].taskrun.value` inside a nested flowable (`If`, `Parallel`) → just `{{ item.value }}` — `item` is reachable at any depth. `parents[0].taskrun.value` **only** maps to `item.parent.value` when it refers to the outer loop of two nested `Loop`s; deeper levels use `item.parents[n]`. Inside an iteration, `outputs.<task>[taskrun.value].value` → `outputs.<task>.value` (outputs are scoped to the sub-execution).
  - **Outputs are no longer auto-merged.** Declare them on the Loop task: `outputs: [{id, type, value}]`, plus `fetchType` — `AUTO` (default: `STORE` when `values` is a URI, else `FETCH`), `FETCH` (in-memory list at `outputs.<loopId>.outputs`), or `STORE` (file URI at `outputs.<loopId>.uri`, for large iteration counts). Post-loop access: `outputs.<loopId>.outputs[n].outputs.<outputId>` by index, or the new `loopOutputs(outputs.<loopId>.outputs, '<outputId>')` function to collect one output across all iterations as a list. Key-based access (`outputs.<foreachId>[<value>].field`) is gone. `outputs.<loopId>.iterationCount` gives the iteration count.
  - **Failure handling:** the `AllowFailure` wrapper is replaced by `transmitFailed: false` on the `Loop` task.
  - **Carried over unchanged:** `concurrencyLimit`. **New in `Loop`:** native map iteration (`item.key` / `item.value` when `values` is a map), `finally:`, typed `outputs:`. When list elements are not plain strings, `item.value` is a **string** — use `fromJson(item.value).field`, never `item.value.field`.
  - **`ForEachItem`:** `batch.rows: 1` maps to a plain `Loop` over the source URI (one line per iteration). For flows that relied on per-batch isolation, use `io.kestra.plugin.core.storage.Split` (`rows: <batchSize>`) → `Loop` over `outputs.split.uris` → `Subflow` per batch, then `Concat` over `loopOutputs(...)`. `subflowOutputs` becomes a flow-level `outputs:` declaration in the child flow.
- **Pebble `json` filter and function removed:** Replace the `json` Pebble filter with `toJson` and the `json()` Pebble function with `fromJson()` (identical signature and behavior). The `json` Pebble *test* (`{% if x is json %}`) is unrelated and still works — only the filter and function forms change. Verified in code on `releases/v2.0.x`: only `ToJsonFilter.java` and `FromJsonFunction.java` exist. Note the customer guide (`migration-guide/v2.0.0/json-function-removed`) documents only the `json()` **function**; the `json` **filter** removal is equally breaking and is not covered there. See `migration-guide/v2.0.0/json-function-removed`.
- **`MultipleCondition` removed:** Replace `io.kestra.plugin.core.condition.MultipleCondition` with a top-level `dependsOn` list on the Flow trigger (one entry per upstream flow). Arbitrary string keys used as wrapper ids are dropped. `window`/`windowAdvance` move to the new top-level `window` block.
- **`FlowCondition` and `FlowNamespaceCondition` removed:** Replace `io.kestra.plugin.core.condition.FlowCondition` and `io.kestra.plugin.core.condition.FlowNamespaceCondition` with a `dependsOn` entry using the `flowId` / `namespace` properties. For prefix/pattern namespace matching, move the logic into `when` using `startsWith` / `endsWith`.
- **JSON flow definitions removed:** Flow definitions must be in YAML format. JSON-defined flows are no longer accepted.
- **`FILE` input `extension` property removed:** Remove the `extension` property from `FILE`-type inputs; it is no longer enforced.
- **`Count` execution task removed:** `io.kestra.plugin.core.execution.Count` is removed. Use KV Store or custom logic for execution counting.
- **`Resume` execution task removed:** `io.kestra.plugin.core.execution.Resume` is removed. Use the SDK to manipulate other execution states for RBAC compliance.
- **`Toggle` trigger removed:** `io.kestra.plugin.core.trigger.Toggle` is removed. Use the API or SDK to enable/disable triggers programmatically.
- **`schedule` Pebble variable removed:** The `{{ schedule }}` variable is no longer available in run context. Use `{{ trigger }}` properties instead.
- **`MultiselectInput.options` removed:** Replace `options` with `values` on `MULTISELECT` inputs.
- **`AbstractTrigger.minLogLevel` removed:** Remove `minLogLevel` from trigger definitions; it is no longer supported.
- **`Pause.delay` and `Pause.tasks` removed:** Remove `delay` and inline `tasks` from `Pause` task definitions. Use `timeout` for time-based pausing and define child tasks separately.
- **`Schedule.backfills` removed:** Remove `backfills` from `Schedule` trigger definitions.
- **`PurgeKV.expiredOnly` deprecated → `behavior`:** `expiredOnly` on `io.kestra.plugin.core.kv.PurgeKV` is deprecated in favor of a polymorphic `behavior` object (`behavior: {type: key|version, ...}`), defaulting to `{type: key, expiredOnly: true}`. The deprecated `expiredOnly` still parses in v2 (and overrides `behavior` when set), but should be migrated. **Do not simply delete it**: dropping `expiredOnly: false` silently flips the task to expired-only purging (the v2 default). Automated: `expiredOnly: <x>` → `behavior: {type: key, expiredOnly: <x>}`. `behavior` also exists on v1.3.28 (backported), but not on earlier 1.3.x patches, so the conversion is gated to the v2 path and skipped under `--stay-v1-compatible` (safe: v2 still accepts the deprecated property). Verified in code on `develop` (`PurgeKV.java`, `KvPurgeBehavior.java`, `Key.java`).
- **HTTP task properties restructured:** In `io.kestra.plugin.core.http.Request`, `Download`, and `Trigger`, deprecated root-level authentication and SSL properties are removed in favor of `options.auth` and `options.ssl` sub-objects. Remove `sslOptions` from HTTP triggers.
- **`Exit.ExitState.CANCELED` removed:** Replace the single-L `CANCELED` with `CANCELLED` (double L) in `io.kestra.plugin.core.execution.Exit` task state configuration. The v2 enum is `SUCCESS, WARNING, KILLED, FAILED, CANCELLED`; v1.3 accepted both spellings mapping to the same `CANCELLED` state, and develop dropped the deprecated single-L alias (kestra commit `3def65d714`). **Do not map to `KILLED`** — `KILLED` sends an out-of-band kill event that stops sibling running tasks, whereas `CANCELLED` only marks the execution; the semantics are not equivalent. Verified in code on `develop` (`Exit.java`).
- **Storage task aliases removed:** `io.kestra.plugin.core.storage.Purge` is removed; use `io.kestra.plugin.core.execution.PurgeExecutions` (note the **`execution`** subpackage — v1.3.28 carries the alias on that class, and `io.kestra.plugin.core.storage.PurgeExecutions` never existed; aliases were dropped on develop in kestra commit `db4416f48d`). `io.kestra.plugin.core.storage.PurgeExecution` is removed; use `io.kestra.plugin.core.storage.PurgeCurrentExecutionFiles`.
- **Deprecated API endpoints removed:** `/executions/trigger`, `/namespaces/{namespace}/kv`, and `/namespaces/{namespace}/secrets` endpoints are removed. Use the current versioned API paths.
- **Trigger aliases removed:** Non-core trigger aliases that were kept for backwards compatibility are removed. Use the canonical trigger type names.
- **Pebble `read()` / `fileURI()` named argument `version` removed:** the `version` named argument of the `read()` and `fileURI()` Pebble functions is renamed to `revision`, with **no alias or fallback** (kestra PR [#16699](https://github.com/kestra-io/kestra/pull/16699), merged 2026-06-23 — first shipped in v2.0.0-rc3; the KV list API's `version` field is likewise now `revision`). An expression like `{{ read(namespace.files.x, version=2) }}` hard-fails on v2. **Not automated** (rewriting inside arbitrary expressions — including ones embedded in script bodies — risks corrupting non-Pebble code): the migration tool emits a validation warning when it finds `read(`/`fileURI(` with a `version=` argument; update those expressions to `revision=` manually. Verified in code on `develop` (`ReadFileFunction.java` accepts only `revision`).

## v2-only compatible changes

Most migrations in `## OSS changes` produce YAML that still parses on v1.3 (removed properties, type renames that had aliases). The migrations below are different: their output introduces constructs that v1.3 does not understand, so migrated flows can only be deployed to a v2 instance.

- **Trigger conditions & preconditions → `when` / `dependsOn`** — the rewrite emits `when:` (on any trigger) and `dependsOn:` / `window:` / `mode:` / `minSatisfied:` (on Flow triggers), none of which are recognized by v1.3. See `## OSS changes`: "Trigger `conditions` / Schedule `scheduleConditions` removed" and "Flow trigger `preconditions` and `timeWindow` removed".
- **`checks[].condition` → `checks[].when`** — flow-level `checks` rename their `condition` property to `when`, unifying it with the `when` used on tasks and triggers. A deprecated `condition` alias still parses in 2.0 (confirmed by the customer guide, which also states the alias is scheduled for removal in a later release), so this rewrite is future-proofing rather than required; because `when` on `checks` is not recognized by v1.3, it is gated to the v2-only path and **skipped under `--stay-v1-compatible`**. Only the top-level `checks:` list is rewritten — task-level `condition` (on `If`, `Fail`, `LoopUntil`, `Switch`) is left untouched. See `migration-guide/v2.0.0/checks-condition-renamed-when`.
- **`PurgeKV.expiredOnly` → `behavior`** — the conversion targets the `behavior` property, which exists on v2 and v1.3.28 but not on earlier 1.3.x patches; gated to the v2-only path (see the OSS entry above). Under `--stay-v1-compatible` the deprecated `expiredOnly` is left in place (it still parses on v2).
- **`workerGroup` → `workerSelector`** (EE) — `workerSelector` does not exist on v1.3; gated to the v2-only path. See the EE section below for the full mapping.

## Third-party plugin changes
These are plugin-level type renames discovered via the Kestra v1.3.10 deprecated-tasks API. They are not core breaking changes but affect flows using these plugins.

- **Notifications plugin split into per-service plugins:** `io.kestra.plugin.notifications.slack.SlackIncomingWebhook` → `io.kestra.plugin.slack.notifications.SlackIncomingWebhook`, `io.kestra.plugin.notifications.slack.SlackExecution` → `io.kestra.plugin.slack.notifications.SlackExecution`, `io.kestra.plugin.notifications.mail.MailSend` → `io.kestra.plugin.email.MailSend`, `io.kestra.plugin.notifications.discord.DiscordExecution` → `io.kestra.plugin.discord.DiscordExecution`.
- **Slack plugin internal restructure:** `io.kestra.plugin.slack.SlackIncomingWebhook` → `io.kestra.plugin.slack.notifications.SlackIncomingWebhook`, `io.kestra.plugin.slack.SlackExecution` → `io.kestra.plugin.slack.notifications.SlackExecution`.
- **Kubernetes plugin `core` subpackage:** `io.kestra.plugin.kubernetes.PodCreate` → `io.kestra.plugin.kubernetes.core.PodCreate`.
- **Datagen plugin `core` subpackage:** `io.kestra.plugin.datagen.Generate` → `io.kestra.plugin.datagen.core.Generate`.
- **AstraDB plugin moved under Cassandra:** `io.kestra.plugin.astradb.Query` → `io.kestra.plugin.cassandra.astradb.Query`.
- **`fs.http` plugin moved to core HTTP:** `io.kestra.plugin.fs.http.Request` → `io.kestra.plugin.core.http.Request`, `io.kestra.plugin.fs.http.Download` → `io.kestra.plugin.core.http.Download`.
- **Log Fetch task moved to kestra plugin:** `io.kestra.plugin.core.log.Fetch` → `io.kestra.plugin.kestra.logs.Fetch`.
- **dbt plugin `Build` task deprecated:** `io.kestra.plugin.dbt.cli.Build` is deprecated. Migrate to `io.kestra.plugin.dbt.cli.DbtCLI`. Because `DbtCLI` requires an explicit `commands:` list (the old `Build` task ran `dbt build` implicitly), migration adds `commands: [dbt build]` when not already set. Properties not valid on `DbtCLI` are also reshaped: `dbtPath` is dropped (dbt is expected on `PATH` inside the container image) and `dockerOptions.image` is promoted to `containerImage` (the rest of `dockerOptions` is dropped since it is not valid on `DbtCLI`).
- **Git plugin `Push` task deprecated:** `io.kestra.plugin.git.Push` is deprecated with no direct replacement. Use `io.kestra.plugin.git.SyncFlows` or the Git API tasks.
- **Nashorn script plugin deprecated:** `io.kestra.plugin.scripts.nashorn.Eval` and `io.kestra.plugin.scripts.nashorn.FileTransform` are `@Deprecated` (still built and published from plugin-scripts as of 2026-07, but scheduled for removal). Migrate to GraalJS or other script tasks.
- **Serdes `InferAvroSchemaFromIon` metadata flag:** `io.kestra.plugin.serdes.avro.InferAvroSchemaFromIon` is flagged as deprecated but the replacement is the same type (no action needed).

## Removed types (no automated replacement — require manual rewrite)

The following types are removed in v2 with no drop-in replacement. Flows using them will produce a validation warning during migration and must be rewritten manually.

- flow-level `pluginDefaults:` / `taskDefaults:` (a keyword, not a type) — removed at every scope; rewrite as an EE Policy referenced via `policyRefs:`, or inline the values onto each task in OSS
- `io.kestra.plugin.core.execution.Count` — use KV Store or custom logic
- `io.kestra.plugin.core.execution.Resume` — use the SDK to manipulate execution states
- `io.kestra.plugin.core.trigger.Toggle` — use the API or SDK to enable/disable triggers
- `io.kestra.plugin.git.Push` — use `io.kestra.plugin.git.SyncFlows` or Git API tasks
- `io.kestra.plugin.scripts.nashorn.Eval` — migrate to GraalJS or other script tasks
- `io.kestra.plugin.scripts.nashorn.FileTransform` — migrate to GraalJS or other script tasks
- `io.kestra.plugin.core.flow.ForEach` — removed; rewrite manually as `io.kestra.plugin.core.flow.Loop`
- `io.kestra.plugin.core.flow.ForEachItem` — removed; rewrite manually as `io.kestra.plugin.core.flow.Loop` (inline) or `Loop` + `Subflow` for isolated per-batch execution
- `io.kestra.plugin.core.flow.EachSequential` / `io.kestra.plugin.core.flow.EachParallel` — removed; rewrite manually as `io.kestra.plugin.core.flow.Loop`

### Trigger condition types (all removed in 2.0)

The full `conditions` subsystem was replaced by `when` (Pebble) on all triggers and `dependsOn` on Flow triggers. Every trigger-side condition type listed below is removed and must be rewritten. See "Trigger conditions → `when` / `dependsOn`" below for the mapping.

- `io.kestra.plugin.core.condition.MultipleCondition` — rewrite as `dependsOn` entries on the Flow trigger
- `io.kestra.plugin.core.condition.ExecutionStatus` — `dependsOn` entry with `states`, or `when` on a `dependsOn` entry
- `io.kestra.plugin.core.condition.ExecutionFlow` — `dependsOn` entry with `flowId` + `namespace`
- `io.kestra.plugin.core.condition.ExecutionNamespace` — `dependsOn` entry with `namespace` (exact) or `when` using `startsWith` / `endsWith`
- `io.kestra.plugin.core.condition.ExecutionLabels` — `dependsOn` entry with `labels`
- `io.kestra.plugin.core.condition.ExecutionOutputs` — `when` expression on a `dependsOn` entry accessing `outputs`
- `io.kestra.plugin.core.condition.HasRetryAttempt` — `dependsOn` `when: "{{ hasRetryAttempt == true }}"`
- `io.kestra.plugin.core.condition.Expression` — direct `when` on the trigger
- `io.kestra.plugin.core.condition.VariableCondition` — direct `when` on the trigger
- `io.kestra.plugin.core.condition.Not` / `Or` — use `not` / `or` operators inside `when`, or `mode: ANY` on a Flow trigger
- `io.kestra.plugin.core.condition.DayWeek` — `when: "{{ dayOfWeek(trigger.date) == '<DAY>' }}"`
- `io.kestra.plugin.core.condition.DayWeekInMonth` — `when: "{{ isDayWeekInMonth(trigger.date, '<DAY>', '<POSITION>') }}"`
- `io.kestra.plugin.core.condition.Weekend` — `when: "{{ isWeekend(trigger.date) }}"`
- `io.kestra.plugin.core.condition.PublicHoliday` — `when: "{{ isPublicHoliday(trigger.date, '<country>') }}"`
- `io.kestra.plugin.core.condition.DateTimeBetween` — `when: "{{ trigger.date > '<after>' and trigger.date < '<before>' }}"`
- `io.kestra.plugin.core.condition.TimeBetween` — `when: "{{ hourOfDay(trigger.date) >= <from> and hourOfDay(trigger.date) < <to> }}"`
- `io.kestra.plugin.core.condition.FlowCondition` / `FlowNamespaceCondition` — `dependsOn` entry with `flowId` / `namespace` (exact) or `when` with `startsWith` / `endsWith`

The old-path variants under `io.kestra.core.models.conditions.types.*` (same class names with a `Condition` suffix) are removed in the same way.

## v2-incompatible flows (`--disable-v2-incompatible`)

Warnings emitted by the migrator fall into two severities, decided by whether Kestra 2.0 **rejects the flow on save** (`YamlParser` deserializes flows with `FAIL_ON_UNKNOWN_PROPERTIES = true`, and `FlowValidator` violations are returned as constraint violations):

| Severity | Warnings | Effect on 2.0 |
|----------|----------|---------------|
| **v2-incompatible** | removed types, flow-level `pluginDefaults` / `taskDefaults`, unrewritten trigger `conditions` / `preconditions` / `scheduleConditions`, leftover `workerGroup`, Schedule triggers missing an input without `defaults` | Flow is **rejected**; it cannot be deployed at all |
| **advisory** | Pebble `read()` / `fileURI()` using the removed `version=` argument | Flow deploys; breaks at run time |

`--disable-v2-incompatible` (off by default) rewrites every flow carrying at least one **v2-incompatible** warning into a deployable placeholder, so that a bulk migration can be pushed to a 2.0 instance in one go and the flows needing manual work are visible in the UI instead of failing silently at deploy time:

- the original (best-effort migrated) definition is commented out at the end of the file — nothing is lost, and Kestra stores flow source verbatim, so the comments survive UI edits and Git sync;
- `disabled: true` is set — a disabled flow pauses its triggers and rejects new executions;
- the label `v2-migration: needs-manual-rewrite` is added, so the whole set is one label filter away in the UI. Existing labels are preserved (in whichever shape they use — 2.0 accepts both a map and a list of `key`/`value` pairs);
- the reason for each warning is prepended to the flow `description`, under a `[kestra-migrate] NEEDS MANUAL REWRITE` marker; an existing description is preserved below a `--- original description ---` separator;
- a placeholder `io.kestra.plugin.core.log.Log` task with id `needs_manual_rewrite` is emitted, because `Flow.tasks` is `@NotEmpty` — a flow with no tasks would not parse, and the disabled/labelled state would never become visible.

Triggers are commented out along with the rest of the body. `disabled: true` already pauses them; the reason they cannot simply be left in place is that a trigger holding a removed type or a v1 `conditions:` block fails to deserialize.

The label key is `v2-migration`, not `v2-migration:needs-manual-rewrite` — label keys are validated against `^[\p{Ll}][\p{L}0-9._-]*$` (`Label.java`), which excludes `:`.

The flag is rejected together with `--stay-v1-compatible`: the severities above describe a 2.0 deployment, and a v1.3 instance parses the flows unchanged.

## Trigger conditions → `when` / `dependsOn` (reference)

New Pebble helper functions available inside `when` expressions: `isPublicHoliday(date, countryCode, [subdivision])`, `isDayWeekInMonth(date, dayOfWeek, position)` (position: `FIRST`/`SECOND`/`THIRD`/`FOURTH`/`LAST`), `isWeekend(date)`, `isLastWorkingDay(date, [workingDays])` (working days default Mon–Fri), `dayOfWeek(date)` (returns `MONDAY`…`SUNDAY`), `hourOfDay(date)` (0–23), `dayOfMonth(date)` (1–31), `monthOfYear(date)` (1–12).

### `when` expression context

The variables available inside `when` depend on the trigger type:

| Trigger type | Available variables |
|---|---|
| Schedule | `trigger.date`, `trigger.timestamp` |
| Webhook | `trigger.body`, `trigger.headers` |
| Flow | `namespace`, `flowId`, `state`, `labels`, `outputs`, `hasRetryAttempt` |

**Schedule date skipping:** a `when` on a Schedule trigger is evaluated against each *candidate* date — when it returns `false` the scheduler skips that date and advances to the next cron-matching one. This preserves the old `conditions` behavior; `when` selects which scheduled dates fire, not merely whether a single date fires.

### Schedule / Webhook / HTTP triggers

| Old `conditions` shape | New |
|---|---|
| `Expression` (body or variable) | `when: "<pebble>"` directly on the trigger |
| `DayWeek` (e.g. MONDAY) | `when: "{{ dayOfWeek(trigger.date) == 'MONDAY' }}"` |
| `Weekend` | `when: "{{ isWeekend(trigger.date) }}"` |
| `Not` > `Weekend` (weekdays only) | `when: "{{ not isWeekend(trigger.date) }}"` |
| `Not` > `DayWeek` (e.g. exclude SUNDAY) | `when: "{{ dayOfWeek(trigger.date) != 'SUNDAY' }}"` |
| `PublicHoliday` (country: FR) | `when: "{{ isPublicHoliday(trigger.date, 'FR') }}"` |
| `DayWeekInMonth` | `when: "{{ isDayWeekInMonth(trigger.date, '<DAY>', '<POSITION>') }}"` |
| `DateTimeBetween` | `when: "{{ trigger.date > '<after>' and trigger.date < '<before>' }}"` |
| `TimeBetween` | `when: "{{ hourOfDay(trigger.date) >= <from> and hourOfDay(trigger.date) < <to> }}"` |
| Multiple `Expression` conditions | Combined with `and` / `or` in a single `when` |

### Flow triggers

Both the trigger-level `conditions` list and the `preconditions` object are replaced by a single `dependsOn` list plus optional `when` / `window` / `mode` / `onMiss` properties.

| Old shape | New |
|---|---|
| `conditions` with `ExecutionStatus` + `ExecutionFlow` | `dependsOn` entry with `states` and `flowId` / `namespace` |
| `conditions` with `ExecutionNamespace` (prefix/suffix) | `dependsOn` entry with `when` using `startsWith` / `endsWith` |
| `conditions` with `ExecutionLabels` | `dependsOn` entry with `labels` |
| `conditions` with `ExecutionOutputs` expression | `dependsOn` entry with `when` accessing `outputs` |
| `conditions` with `HasRetryAttempt` | `dependsOn` entry with `when: "{{ hasRetryAttempt == true }}"` |
| `conditions` with `Not` / `Or` wrappers | `not` / `or` operators in `when`, or `mode: ANY` |
| `preconditions.flows` + `MultipleCondition` | `dependsOn` entries (no wrapper, no `preconditions.id`) |
| `multipleConditions` (top-level Flow-trigger property — distinct from the `MultipleCondition` condition type) | `dependsOn` entries + `window.every` (the arbitrary string keys are dropped; `windowAdvance` has no direct equivalent) |
| `preconditions.timeWindow` | top-level `window`: `deadline`, `from`/`to`, `every`/`offset`, or `lookback` |
| `preconditions.where` filters | `dependsOn` entries with `labels` and `when` |
| `preconditions.resetOnSuccess: true` | dropped (v2 always resets after firing) |
| `preconditions.resetOnSuccess: false` | no equivalent — use `mode: ANY` |
| Deep-merged `trigger.outputs` | scoped: `trigger.outputs.<flowId>.<key>` |

For "N of M" semantics, set `mode: AT_LEAST` + `minSatisfied: <n>` on the Flow trigger (`minSatisfied` ≥ 1 and ≤ the number of `dependsOn` entries). `mode` defaults to `ALL`; `mode: ANY` fires as soon as any one entry is satisfied (replacing the old "N separate triggers for OR logic" pattern).

**Runtime behavior changes on Flow triggers** (no YAML migration action, but they change what happens after the upgrade):

- **Render failures now create executions.** In v1 a Flow trigger whose `inputs:` expression failed to render (e.g. a missing upstream output key) silently dropped the event. In v2 a `FAILED` execution is created instead. Review `inputs:` expressions on Flow triggers before upgrading to avoid a wave of unexpected `FAILED` executions — this interacts with the `trigger.outputs.<flowId>.<key>` scoping change above.
- **Accumulated window state is reset.** `dependsOn` entry keys are derived from each entry's `namespace` + `flowId` (order-independent), replacing v1's positional auto-generated `condition_1`, `condition_2`… keys. The trigger-level state-store key also changes from `preconditions.id` to `{flowId}/{triggerId}`, so state accumulated under v1 is not found after the upgrade: in-flight multi-flow triggers re-evaluate from scratch, costing at most one missed trigger cycle. Old-format queued events are discarded with a warning.

SLA misses are declared with a trigger-level `onMiss:` block (peer to `window:`): `onMiss: {behavior: FAIL, labels: {sla: miss}}` creates a `FAILED` execution when a `window` deadline passes without all dependencies satisfied, applying the given labels for downstream alerting.

## EE changes
All OSS flow changes apply to EE. Additional EE-only flow changes are listed below.

- **`workerGroup` → `workerSelector`:** the `workerGroup: {key, fallback}` property on tasks and triggers is **removed** in v2 (no deprecated alias) and replaced by `workerSelector: {tags: [...], match, fallback}` (kestra commit `da42c3422b`, tag-based Worker Queue routing; model verified on `develop` in `WorkerSelector.java`). Migration mapping:
  - `workerGroup.key: <k>` → `workerSelector.tags: [<k>]` — each tag must be an **RFC 1123 label** (lowercase alphanumerics and hyphens, must start/end alphanumeric, ≤ 63 chars); v1 keys that don't comply (uppercase, underscores, templated `{{ ... }}` values) cannot be mapped mechanically and are flagged with a validation warning.
  - `match` is new (`ALL` | `ANY`, default `ALL`) — a single-tag selector behaves like the old single key either way.
  - **Fallback default flipped:** v1 `workerGroup.fallback` defaulted to `WAIT`; v2 `workerSelector.fallback` defaults to **`FAIL`** when unset. To preserve v1 behavior the migration pins `fallback: WAIT` when v1 omitted it. v2 adds a fourth value `IGNORE` (drop the tag requirement, route to the default queue). `fallback` without `tags` is invalid in v2, so a v1 `workerGroup` carrying only `fallback` is flagged with a warning instead of converted.
  - Automated on the v2 path; skipped under `--stay-v1-compatible` (`workerSelector` does not exist on v1.3).
- **Azure Log Exporter split:** Replace `io.kestra.plugin.ee.azure.LogExporter` with `io.kestra.plugin.ee.azure.monitor.LogExporter` or `io.kestra.plugin.ee.azure.storage.LogExporter`.
- **Cross-namespace `kv()` permission check:** Ensure the execution has access to the target namespace or update role bindings.
- **PurgeAuditLogs property rename:** Replace `permissions` with `resources` in `io.kestra.plugin.ee.core.log.PurgeAuditLogs` tasks.

