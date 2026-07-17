# Report: kestra-io/kestra-ee#3238 — Remove Deprecated Features (Post 1.0)

| Field | Value |
|-------|-------|
| **Issue** | [kestra-io/kestra-ee#3238](https://github.com/kestra-io/kestra-ee/issues/3238) |
| **Title** | [Post 1.0 release] Remove deprecated features |
| **Status** | Open |
| **Labels** | `area/backend`, `kind/breaking-change` |
| **Created** | 2025-03-25 |
| **Primary author** | Loic Mathieu (@loicmathieu) |

## Summary

This tracking issue coordinates the removal of all deprecated features from Kestra ahead of the v2.0 release. Work spans two repositories — `kestra-io/kestra` (OSS core) and `kestra-io/kestra-ee` (enterprise edition) — and was carried out in 9 phases from October 2025 through April 2026.

**Totals across all PRs:**

| Metric | Value |
|--------|-------|
| Pull requests merged | 16 |
| Lines added | ~1,168 |
| Lines deleted | ~21,142 |
| Net lines removed | ~19,974 |
| Repositories affected | 2 (`kestra`, `kestra-ee`) |
| Timeline | Oct 2025 – Apr 2026 |

---

## Timeline of Changes

### Phase 1 — Oct 17, 2025

**kestra [#12066](https://github.com/kestra-io/kestra/pull/12066)** — "Feat/remove deprecated"
- Merged: 2025-10-17 | +158 / -4,723 | 10 commits

**kestra-ee [#5462](https://github.com/kestra-io/kestra-ee/pull/5462)** — "Feat/remove deprecated"
- Merged: 2025-10-17 | +47 / -1,132 | 2 commits

**Removals:**
- `Input.name` property (replaced by `id`)
- Flow listeners (both OSS and EE)
- `BOOLEAN`-type inputs (renamed to `BOOL`)
- `ENUM`-type inputs (renamed to `SELECT` / `MULTISELECT`)
- `Echo` task (`io.kestra.plugin.core.debug.Echo`)
- Templates (`Template` model, `TemplateRepository`, CLI commands, Kafka executor, EE controllers, ElasticSearch repository)
- Flow YAML expand helper (`IncludeHelperExpander`, `[[>/path/to/file]]` syntax)
- Task defaults (`taskDefaults` replaced by plugin defaults; `TaskGlobalDefaultConfiguration` removed)
- DB migrations switched to V3

---

### Phase 2 — Oct 22, 2025

**kestra [#12211](https://github.com/kestra-io/kestra/pull/12211)** — "Feat/remove deprecated step 2"
- Merged: 2025-10-22 | +210 / -1,238 | 5 commits

**kestra-ee [#5502](https://github.com/kestra-io/kestra-ee/pull/5502)** — "feat(flows): remove deprecated flow update task endpoint"
- Merged: 2025-10-22 | +2 / -72 | 2 commits

**Removals:**
- `PATCH /api/v1/flows/{namespace}/{id}/{taskId}` endpoint (single-task update)
- `EachSequential` task (`io.kestra.plugin.core.flow.EachSequential`)
- `EachParallel` task (`io.kestra.plugin.core.flow.EachParallel`)
- `LocalFiles` task (`io.kestra.plugin.core.storage.LocalFiles`)
- Pebble `json` filter (`JsonFilter`) and `json` function (`JsonFunction`)

---

### Phase 3 — Oct 27, 2025

**kestra [#12317](https://github.com/kestra-io/kestra/pull/12317)** — "Feat/remove deprecated 3"
- Merged: 2025-10-27 | +24 / -664 | 3 commits

**Removals:**
- `MultipleCondition` condition (`io.kestra.plugin.core.condition.MultipleCondition`)
- `FlowCondition` (`io.kestra.plugin.core.condition.FlowCondition`)
- `FlowNamespaceCondition` (`io.kestra.plugin.core.condition.FlowNamespaceCondition`)
- `Schedule.scheduleConditions` property (the `ScheduleCondition` marker interface itself is kept)

---

### Phase 4 — Nov 13, 2025

**kestra [#12826](https://github.com/kestra-io/kestra/pull/12826)** — "Chore/remove deprecated"
- Merged: 2025-11-13 | +52 / -1,389 | 6 commits

**kestra-ee [#5758](https://github.com/kestra-io/kestra-ee/pull/5758)** — "feat(flow): remove JSON flow support"
- Merged: 2025-11-13 | +68 / -134 | 2 commits

**Removals:**
- State Store (`StateStore`, `AbstractState`, `state.Delete`, `state.Get`, `state.Set`, CLI migration command)
- JSON flow support (flows could previously be defined in JSON; now YAML only)
- `FILE` input `extension` property
- `Property.of()` deprecated constructors (replaced by `Property.ofValue()` / `Property.ofExpression()`)
- `runner` property on script tasks (replaced by `taskRunner`; `RunnerType` enum removed)
- `FlowController` JSON endpoints (EE side)

---

### Phase 5 — Dec 16, 2025

**kestra [#13678](https://github.com/kestra-io/kestra/pull/13678)** — "feat(system): remove the deprecated kestra.tasks.scripts.docker.volume-enabled"
- Merged: 2025-12-16 | +2 / -19

**Removals:**
- `kestra.tasks.scripts.docker.volume-enabled` configuration property

---

### Phase 6 — Jan 19, 2026

**kestra [#14197](https://github.com/kestra-io/kestra/pull/14197)** — "chore(system): remove deprecated tasks"
- Merged: 2026-01-19 | +2 / -867 | 1 commit

**kestra-ee [#6442](https://github.com/kestra-io/kestra-ee/pull/6442)** — "chore(system): remove deprecated tasks"
- Merged: 2026-01-19 | +0 / -247 | 1 commit

**Removals:**
- `Count` task (`io.kestra.plugin.core.execution.Count`) and `ExecutionCount` / `Flow` statistics models
- `Resume` task (`io.kestra.plugin.core.execution.Resume`) — tasks manipulating other executions now require the SDK for RBAC compliance
- `Toggle` trigger (`io.kestra.plugin.core.trigger.Toggle`)
- ElasticSearch execution count repository methods (EE)

---

### Phase 7 — Mar 24, 2026

**kestra [#15179](https://github.com/kestra-io/kestra/pull/15179)** — "Chore/remove deprecated"
- Merged: 2026-03-24 | +91 / -2,325 | 4 commits

**Removals:**
- `schedule` Pebble variable from run variables
- Deprecated CLI commands: `FlowCreateCommand`, `FlowUpdateCommand`, `FlowUpdatesCommand` (old versions), `FlowValidateCommand` (old version), `FlowNamespaceUpdateCommand` (old version), `NamespaceFilesUpdateCommand` (old version)
- Deprecated CLI server command parameters
- `MultiselectInput.options` property
- `AbstractRetry.maxAttempt` (renamed to `maxAttempts`)
- `AbstractTrigger.minLogLevel`
- `Exit.ExitState.CANCELED`
- `Pause.delay` and `Pause.tasks` properties
- `Subflow.outputs` property
- `Schedule.backfills` property
- `PurgeKV.expiredOnly` property
- `HttpConfiguration` deprecated root properties (replaced by `auth` / `ssl` sub-objects)
- `io.kestra.plugin.core.http.Trigger.sslOptions`

---

### Phase 8 — Apr 7, 2026

**kestra [#15273](https://github.com/kestra-io/kestra/pull/15273)** — "chore(core): remove deprecated endpoints and endpoints parameters"
- Merged: 2026-04-07 | +154 / -2,479 | 14 files

**kestra-ee [#7205](https://github.com/kestra-io/kestra-ee/pull/7205)** — "chore(core): remove deprecated endpoints and endpoints parameters"
- Merged: 2026-04-07 | +100 / -2,134

**kestra [#15370](https://github.com/kestra-io/kestra/pull/15370)** — "chore(flows): remove core task aliases"
- Merged: 2026-04-07 | +88 / -108

**Removals:**
- Deprecated API endpoints: `/executions/trigger`, `/namespaces/{namespace}/kv`, `/namespaces/{namespace}/secrets`
- Deprecated endpoint parameters across `ExecutionController`, `FlowController`, `KVController`, `LogController`, `TriggerController`, `NamespaceSecretController`
- Core task aliases: `io.kestra.plugin.core.storage.Purge` -> `PurgeExecutions`, `io.kestra.plugin.core.storage.PurgeExecution` -> `PurgeCurrentExecutionFiles`
- OpenAPI spec updated to remove all deprecated endpoints and parameters

---

### Phase 9 — Apr 14, 2026

**kestra [#15522](https://github.com/kestra-io/kestra/pull/15522)** — "chore(flow): remove trigger aliases"
- Merged: 2026-04-14 | +4 / -7

**Removals:**
- Trigger aliases (non-core aliases that were previously kept for backwards compatibility)

---

## Commit Inventory

### kestra-io/kestra (21 commits)

| Date | SHA | Message |
|------|-----|---------|
| 2025-10-15 | `f2a41d09bb66` | feat(flows): remove deprecated input name |
| 2025-10-15 | `ce1dcb1786ff` | feat(flows): remove deprecated flow listeners |
| 2025-10-15 | `b57d56df57ea` | feat(flows): remove deprecated BOOLEAN inputs |
| 2025-10-15 | `5c6b19beaa87` | feat(flows): remove deprecated ENUM inputs |
| 2025-10-15 | `b8c0f6265156` | feat(flows): remove the deprecated Echo task |
| 2025-10-15 | `fd0140def405` | feat(flows): remove Templates |
| 2025-10-16 | `504337921926` | feat(flows): remove flow expand helper |
| 2025-10-16 | `f3e32efe616b` | feat(system): remove task defaults |
| 2025-10-20 | `561caa62bf0a` | feat(flows): remove deprecated flow update task endpoint |
| 2025-10-21 | `629486469a83` | feat(flows): remove deprecated EachSequential |
| 2025-10-21 | `bdf81b9639d5` | feat(flows): remove deprecated EachParallel task |
| 2025-10-21 | `4f782b38ee04` | feat(flows): remove deprecated Pebble json function and filter |
| 2025-10-21 | `68b618ad240e` | feat(flows): remove deprecated LocalFiles task |
| 2025-10-23 | `e10dfcd48201` | feat(flows): remove deprecated MultipleCondition condition |
| 2025-10-23 | `5fd73e301f20` | feat(flows): remove deprecated FlowCondition and FlowNamespaceCondition |
| 2025-10-23 | `74a601b0bca5` | feat(flows): remove deprecated Schedule.scheduleConditions |
| 2025-11-10 | `21e6f7f20183` | feat(flow): remove state store |
| 2025-11-10 | `f48629c757a2` | feat(flow): remove JSON flow support |
| 2025-11-10 | `4761244c03a1` | feat(flow): remove FILE input extension |
| 2025-11-10 | `cf1cfd6133e2` | feat(core): remove Property deprecated methods and constructors |
| 2025-11-10 | `dfae1c8a0305` | feat(core): remove deprecated runner property in favor of taskRunner |

### kestra-io/kestra-ee (5 commits with `Part-of` trailer)

| Date | SHA | Message |
|------|-----|---------|
| 2025-10-15 | `c2598361a0ca` | feat(flows): remove deprecated flow listeners |
| 2025-10-15 | `44db0be16d22` | feat(flows): remove Templates |
| 2025-10-20 | `39cca307e4aa` | feat(flows): remove deprecated flow update task endpoint |
| 2025-10-22 | `3d1693d4db42` | feat(flows): remove the deprecated EachParallel task |
| 2025-11-10 | `1c265973127f` | feat(flow): remove JSON flow support |

---

## Remaining Open Items

The following items from the issue checklist are **not yet completed** as of 2026-04-14:

| Item | Notes |
|------|-------|
| `runner` property on script tasks (pre-taskRunner) | Per last comment (2026-04-02): "will be removed at the end of the 2.0 sprint" |
| Remove Jython, Nashorn, Groovy Eval + FileTransform tasks | Tracked in [kestra-ee#3424](https://github.com/kestra-io/kestra-ee/issues/3424). Targets: `io.kestra.plugin.scripts.groovy.Eval`, `.FileTransform`, `nashorn.Eval`, `.FileTransform`, `jython.Eval`, `.FileTransform` |
| [docs] Migration guide from listeners to `afterExecution` | Listeners were removed but no documentation translating listener patterns to the new `afterExecution` + `runIf` approach has been written |

---

## Related Work

**kestra-ee [#7079](https://github.com/kestra-io/kestra-ee/pull/7079)** — "feat(flows): override deprecated endpoint with permission check in EE"
- Merged: 2026-03-24 | +6,416 / -19,304
- Adds a `GET /flows/deprecated` endpoint that helps users discover deprecated constructs in their flows, with EE namespace-level permission enforcement.

---

## Linked Issues

- [kestra-ee#3151](https://github.com/kestra-io/kestra-ee/issues/3151) — referenced in issue body (completed)
- [kestra-ee#3237](https://github.com/kestra-io/kestra-ee/issues/3237) — referenced in issue body (completed)
- [kestra-ee#3424](https://github.com/kestra-io/kestra-ee/issues/3424) — Jython/Nashorn/Groovy removal (open)
- [kestra#8225](https://github.com/kestra-io/kestra/issues/8225) — BOOLEAN-type input removal (completed)
- [kestra#3933](https://github.com/kestra-io/kestra/issues/3933) — `/` on internal storage (completed)
- [kestra#9565](https://github.com/kestra-io/kestra/pull/9565) — `schedule` Pebble variable removal (completed)
- [kestra-ee#6879](https://github.com/kestra-io/kestra-ee/issues/6879) — execution endpoint restructuring (moved out of scope)
