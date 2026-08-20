package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// apply is a test helper that runs Apply and fatals on error.
func apply(t *testing.T, in string) string {
	t.Helper()
	out, _, err := Apply([]byte(in))
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	return string(out)
}

// applyWithWarnings is a test helper that returns both output and warnings.
func applyWithWarnings(t *testing.T, in string) (string, []string) {
	t.Helper()
	out, warnings := applyWithWarningDetails(t, in)
	return out, warningMessages(warnings)
}

// applyWithWarningDetails is applyWithWarnings without dropping the severity.
func applyWithWarningDetails(t *testing.T, in string, opts ...Option) (string, []Warning) {
	t.Helper()
	out, warnings, err := Apply([]byte(in), opts...)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	return string(out), warnings
}

// warningMessages flattens warnings to their messages.
func warningMessages(warnings []Warning) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w.Message)
	}
	return out
}

// hasWarningContaining reports whether any warning contains substr.
func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// ── Rule: renameInputNameToID ─────────────────────────────────────────────────

func TestApply_RenameInputNameToID(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - name: prompt
    type: STRING
    defaults: hello
  - name: count
    type: INT
`
	out := apply(t, in)
	if strings.Contains(out, "name: prompt") || strings.Contains(out, "name: count") {
		t.Error("output still contains 'name:' in inputs")
	}
	if !strings.Contains(out, "id: prompt") || !strings.Contains(out, "id: count") {
		t.Error("output missing renamed 'id:' fields")
	}
}

func TestApply_RenameInputNameToID_NoInputs(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hello
`
	out := apply(t, in)
	if !strings.Contains(out, "id: test-flow") {
		t.Error("flow id should be unchanged")
	}
}

// ── Rule: renameInputTypes ────────────────────────────────────────────────────

func TestApply_RenameInputType_BOOLEAN(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: flag
    type: BOOLEAN
`
	out := apply(t, in)
	if strings.Contains(out, "type: BOOLEAN") {
		t.Error("output still contains 'type: BOOLEAN'")
	}
	if !strings.Contains(out, "type: BOOL") {
		t.Error("output missing 'type: BOOL'")
	}
}

func TestApply_RenameInputType_ENUM(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: choice
    type: ENUM
    values:
      - A
      - B
`
	out := apply(t, in)
	if strings.Contains(out, "type: ENUM") {
		t.Error("output still contains 'type: ENUM'")
	}
	if !strings.Contains(out, "type: SELECT") {
		t.Error("output missing 'type: SELECT'")
	}
}

func TestApply_RenameInputType_PreservesOtherTypes(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: text
    type: STRING
  - id: num
    type: INT
`
	out := apply(t, in)
	if !strings.Contains(out, "type: STRING") || !strings.Contains(out, "type: INT") {
		t.Error("non-deprecated input types should be preserved")
	}
}

// ── Rule: renameMaxAttemptToMaxAttempts ────────────────────────────────────────

func TestApply_RenameMaxAttempt(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: flaky
    type: io.kestra.plugin.core.http.Request
    uri: https://example.com
    retry:
      maxAttempt: 5
      type: constant
      interval: PT1S
`
	out := apply(t, in)
	if strings.Contains(out, "maxAttempt:") {
		t.Error("output still contains 'maxAttempt:'")
	}
	if !strings.Contains(out, "maxAttempts: 5") {
		t.Error("output missing 'maxAttempts: 5'")
	}
}

func TestApply_RenameMaxAttempt_InPluginDefaults(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
pluginDefaults:
  - type: io.kestra.plugin.core.http.Request
    values:
      retry:
        maxAttempt: 3
        type: constant
        interval: PT1S
tasks:
  - id: call
    type: io.kestra.plugin.core.http.Request
    uri: https://example.com
`
	out := apply(t, in)
	if strings.Contains(out, "maxAttempt:") {
		t.Error("output still contains 'maxAttempt:' in pluginDefaults")
	}
	if !strings.Contains(out, "maxAttempts: 3") {
		t.Error("output missing 'maxAttempts: 3'")
	}
}

// ── Rule: renamePauseDelayToPauseDuration ─────────────────────────────────────

func TestApply_RenamePauseDelay(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: wait
    type: io.kestra.plugin.core.flow.Pause
    delay: PT1H
`
	out := apply(t, in)
	if strings.Contains(out, "delay:") {
		t.Error("output still contains 'delay:'")
	}
	if !strings.Contains(out, "pauseDuration: PT1H") {
		t.Error("output missing 'pauseDuration: PT1H'")
	}
}

func TestApply_RenamePauseDelay_OnlyOnPauseType(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: other
    type: io.kestra.plugin.core.http.Request
    delay: PT1H
`
	out := apply(t, in)
	if !strings.Contains(out, "delay: PT1H") {
		t.Error("delay on non-Pause task should be preserved")
	}
}

// ── Rule: normalizeFetchType ──────────────────────────────────────────────────

func TestApply_NormalizeFetchType_Store(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: read
    type: io.kestra.plugin.googleworkspace.sheets.Read
    spreadsheetId: abc123
    fetchType: STORE
`
	out := apply(t, in)
	if strings.Contains(out, "fetchType:") {
		t.Error("output still contains 'fetchType:'")
	}
	if !strings.Contains(out, "store: true") {
		t.Error("output missing 'store: true'")
	}
}

func TestApply_NormalizeFetchType_Fetch(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: read
    type: io.kestra.plugin.googleworkspace.sheets.Read
    spreadsheetId: abc123
    fetchType: FETCH
`
	out := apply(t, in)
	if strings.Contains(out, "fetchType:") {
		t.Error("output still contains 'fetchType:'")
	}
	if !strings.Contains(out, "fetch: true") {
		t.Error("output missing 'fetch: true'")
	}
}

func TestApply_NormalizeFetchType_UnknownValuePreserved(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: read
    type: io.kestra.plugin.googleworkspace.sheets.Read
    fetchType: FETCH_ONE
`
	out := apply(t, in)
	if !strings.Contains(out, "fetchType: FETCH_ONE") {
		t.Error("unknown fetchType should be preserved")
	}
}

// ── Rule: renameTaskDefaults (v1-compatible path only) ───────────────────────

// On the v2 path `taskDefaults` is left alone and flagged instead — the keyword
// it used to be renamed into is itself removed in v2.
func TestApply_TaskDefaults_V2WarnsAndLeavesUntouched(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
taskDefaults:
  - type: io.kestra.plugin.core.log.Log
    values:
      message: default
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "pluginDefaults:") {
		t.Errorf("v2 path must not rename taskDefaults into the removed pluginDefaults keyword; got:\n%s", out)
	}
	if !strings.Contains(out, "taskDefaults:") {
		t.Errorf("v2 path must leave taskDefaults in place for manual rewrite; got:\n%s", out)
	}
	if !hasWarningContaining(warnings, "`taskDefaults` is removed in v2") {
		t.Errorf("expected a manual-rewrite warning for taskDefaults, got: %v", warnings)
	}
}

func TestApply_PluginDefaults_V2Warns(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
pluginDefaults:
  - type: io.kestra.plugin.core.log.Log
    values:
      message: default
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, "pluginDefaults:") {
		t.Errorf("pluginDefaults must be left in place for manual rewrite; got:\n%s", out)
	}
	if !hasWarningContaining(warnings, "`pluginDefaults` is removed in v2") {
		t.Errorf("expected a manual-rewrite warning for pluginDefaults, got: %v", warnings)
	}
}

// A flow with no defaults block must not be flagged.
func TestApply_PluginDefaults_NoWarningWhenAbsent(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "pluginDefaults:") {
		t.Error("should not add pluginDefaults when the flow has none")
	}
	if hasWarningContaining(warnings, "removed in v2 and must be rewritten manually") {
		t.Errorf("unexpected pluginDefaults warning, got: %v", warnings)
	}
}

// Under --stay-v1-compatible the pre-v2 normalization is kept: v1.3 still
// accepts `pluginDefaults`, so the deprecated `taskDefaults` alias is renamed
// and `forced` is dropped. No manual-rewrite warning is emitted there.
func TestApply_TaskDefaults_StayV1CompatibleStillRenames(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
taskDefaults:
  - type: io.kestra.plugin.core.log.Log
    forced: true
    values:
      message: default
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
`
	out, warnings, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "taskDefaults:") {
		t.Errorf("StayV1Compatible should rename taskDefaults; got:\n%s", got)
	}
	if !strings.Contains(got, "pluginDefaults:") {
		t.Errorf("StayV1Compatible should emit pluginDefaults; got:\n%s", got)
	}
	if strings.Contains(got, "forced") {
		t.Errorf("StayV1Compatible should still strip forced; got:\n%s", got)
	}
	if hasWarningContaining(warningMessages(warnings), "must be rewritten manually") {
		t.Errorf("no manual-rewrite warning expected under StayV1Compatible, got: %v", warnings)
	}
}

// ── Rewrite: v1 trigger conditions → v2 `when:` (kestra-ee#3033) ─────────────
//
// v2 removes the whole `conditions` subsystem on triggers. Where the rewrite
// is unambiguous (Schedule/Webhook with known condition shapes) the tool
// produces a top-level `when:` Pebble expression. Everything else — Flow
// triggers, `preconditions`, `timeWindow`, unknown condition types — is left
// in place and flagged with a validation warning.

func TestApply_RewriteScheduleConditions_DayWeek(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 9 * * *"
    scheduleConditions:
      - type: io.kestra.plugin.core.condition.DayWeek
        dayOfWeek: MONDAY
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "scheduleConditions:") {
		t.Error("scheduleConditions should have been rewritten to `when:`")
	}
	if !strings.Contains(out, `when: "{{ dayOfWeek(trigger.date) == 'MONDAY' }}"`) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_RewriteWebhookConditions_Expression(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: hook
    type: io.kestra.plugin.core.trigger.Webhook
    key: abc
    conditions:
      - type: io.kestra.plugin.core.condition.Expression
        expression: "{{ trigger.body.tag is defined }}"
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "conditions:") {
		t.Error("trigger conditions should have been rewritten to `when:`")
	}
	if !strings.Contains(out, `when: "{{ trigger.body.tag is defined }}"`) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_RewriteScheduleConditions_Weekend(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.Weekend
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ isWeekend(trigger.date) }}"`) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_RewriteScheduleConditions_PublicHoliday(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.PublicHoliday
        country: FR
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ isPublicHoliday(trigger.date, 'FR') }}"`) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
}

func TestApply_RewriteScheduleConditions_DayWeekInMonth(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * 1"
    conditions:
      - type: io.kestra.plugin.core.condition.DayWeekInMonth
        dayOfWeek: MONDAY
        dayInMonth: FIRST
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ isDayWeekInMonth(trigger.date, 'MONDAY', 'FIRST') }}"`) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
}

func TestApply_RewriteScheduleConditions_DateTimeBetween(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "*/5 * * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.DateTimeBetween
        after: "2025-12-31T23:59:59Z"
        before: "2026-06-30T23:59:59Z"
`
	out, _ := applyWithWarnings(t, in)
	want := `when: "{{ trigger.date > '2025-12-31T23:59:59Z' and trigger.date < '2026-06-30T23:59:59Z' }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
}

func TestApply_RewriteScheduleConditions_NotSingleChild(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.Not
        conditions:
          - type: io.kestra.plugin.core.condition.Weekend
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ not (isWeekend(trigger.date)) }}"`) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// Multi-child Not preserves v1 semantics: `Not([A, B])` evaluated the inner
// list as an AND and negated the result, i.e. `not (A and B)`. The rewrite
// emits exactly that; if an author wrote multi-child Not expecting
// per-child negation ("not A and not B") the output will look surprising —
// but it faithfully matches what v1 did.
func TestApply_RewriteScheduleConditions_NotMultiChild(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.Not
        conditions:
          - type: io.kestra.plugin.core.condition.PublicHoliday
            country: FR
          - type: io.kestra.plugin.core.condition.Weekend
`
	out, warnings := applyWithWarnings(t, in)
	want := `when: "{{ not ((isPublicHoliday(trigger.date, 'FR')) and (isWeekend(trigger.date))) }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if strings.Contains(out, "conditions:") {
		t.Error("conditions should have been consumed")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_RewriteScheduleConditions_Or(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.Or
        conditions:
          - type: io.kestra.plugin.core.condition.Weekend
          - type: io.kestra.plugin.core.condition.PublicHoliday
            country: FR
`
	out, _ := applyWithWarnings(t, in)
	want := `when: "{{ (isWeekend(trigger.date)) or (isPublicHoliday(trigger.date, 'FR')) }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
}

// Multiple top-level conditions in the list are implicitly AND-combined.
func TestApply_RewriteScheduleConditions_AndCombine(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 9 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.DayWeek
        dayOfWeek: MONDAY
      - type: io.kestra.plugin.core.condition.Weekend
`
	out, _ := applyWithWarnings(t, in)
	want := `when: "{{ (dayOfWeek(trigger.date) == 'MONDAY') and (isWeekend(trigger.date)) }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
}

// Old-path `io.kestra.core.models.conditions.types.*` with `Condition` suffix
// must be recognised and rewritten.
func TestApply_RewriteScheduleConditions_OldPathConditionSuffix(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.core.models.conditions.types.WeekendCondition
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ isWeekend(trigger.date) }}"`) {
		t.Errorf("old-path WeekendCondition not rewritten, got:\n%s", out)
	}
}

// If any condition in the list is unsupported, leave the whole list alone and
// warn — don't emit a partial `when:`.
func TestApply_RewriteScheduleConditions_UnsupportedFallsBackToWarning(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.Weekend
      - type: io.kestra.plugin.core.condition.MultipleCondition
        conditions: {}
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, "conditions:") {
		t.Error("conditions list with unsupported type should be preserved")
	}
	if strings.Contains(out, "when:") {
		t.Error("should not emit partial `when:` when one condition is unsupported")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.conditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.conditions warning, got: %v", warnings)
	}
}

// TimeBetween with whole-hour boundaries and a timezone suffix maps onto
// hourOfDay() comparisons.
func TestApply_RewriteScheduleConditions_TimeBetweenWholeHours(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "@hourly"
    conditions:
      - type: io.kestra.plugin.core.condition.TimeBetween
        after: "08:00:00+02:00"
        before: "17:00:00+02:00"
`
	out, warnings := applyWithWarnings(t, in)
	want := `when: "{{ hourOfDay(trigger.date) >= 8 and hourOfDay(trigger.date) < 17 }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// Non-whole-hour boundaries (e.g. 08:30:00) can't be expressed precisely
// with hourOfDay() — refuse and warn.
func TestApply_RewriteScheduleConditions_TimeBetweenSubHour_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "@hourly"
    conditions:
      - type: io.kestra.plugin.core.condition.TimeBetween
        after: "08:30:00"
        before: "17:00:00"
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, "conditions:") {
		t.Error("sub-hour TimeBetween must be preserved for manual rewrite")
	}
	if strings.Contains(out, "when:") {
		t.Error("must not emit when: when sub-hour precision would be lost")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.conditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.conditions warning, got: %v", warnings)
	}
}

// Only one boundary present → only that half of the comparison emitted.
func TestApply_RewriteScheduleConditions_TimeBetweenAfterOnly(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "@hourly"
    conditions:
      - type: io.kestra.plugin.core.condition.TimeBetween
        after: "09:00:00"
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ hourOfDay(trigger.date) >= 9 }}"`) {
		t.Errorf("missing expected `when:`, got:\n%s", out)
	}
}

// A pre-existing `when:` must not be overwritten — combining with it risks
// changing semantics. Warn and leave `conditions:` alone.
func TestApply_RewriteScheduleConditions_ExistingWhenPreserved(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    when: "{{ vars.enabled }}"
    conditions:
      - type: io.kestra.plugin.core.condition.Weekend
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ vars.enabled }}"`) {
		t.Error("existing `when:` must be preserved")
	}
	if !strings.Contains(out, "conditions:") {
		t.Error("conditions should be preserved when an existing `when:` is present")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.conditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.conditions warning, got: %v", warnings)
	}
}

// ── Flow trigger rewrite: conditions → dependsOn entry ───────────────────────

// A bare ExecutionStatus on a Flow trigger meant "fire on any upstream
// execution with these states" in v1. The equivalent v2 shape is a
// `dependsOn` entry with only `states:` — no anchoring filter needed.
func TestApply_FlowTrigger_BareExecutionStatus_StatesOnlyEntry(t *testing.T) {
	in := `
id: test-flow
namespace: system
triggers:
  - id: on_failure
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in:
          - FAILED
          - WARNING
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "conditions:") {
		t.Errorf("conditions should have been consumed, got:\n%s", out)
	}
	for _, want := range []string{"dependsOn:", "states:", "- FAILED", "- WARNING"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_FlowTrigger_ExecutionStatusAndExecutionFlow(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in: [SUCCESS]
      - type: io.kestra.plugin.core.condition.ExecutionFlow
        flowId: upstream
        namespace: company.team
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "conditions:") {
		t.Errorf("conditions should have been consumed, got:\n%s", out)
	}
	for _, want := range []string{"dependsOn:", "flowId: upstream", "namespace: company.team", "states: [SUCCESS]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_FlowTrigger_ExecutionNamespacePrefixWithComparison(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: on_failure
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in:
          - FAILED
          - WARNING
      - type: io.kestra.plugin.core.condition.ExecutionNamespace
        namespace: company
        comparison: PREFIX
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ trigger.namespace startsWith 'company' }}"`) {
		t.Errorf("missing expected `when:` clause, got:\n%s", out)
	}
	if strings.Contains(out, "conditions:") {
		t.Error("conditions should have been consumed")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// Old `prefix: true` form (pre-dates the `comparison:` property) must also
// be recognised as a prefix match.
func TestApply_FlowTrigger_ExecutionNamespacePrefixWithPrefixTrue(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: on_failure
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in: [FAILED]
      - type: io.kestra.plugin.core.condition.ExecutionNamespace
        namespace: company.analytics
        prefix: true
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ trigger.namespace startsWith 'company.analytics' }}"`) {
		t.Errorf("missing expected startsWith clause, got:\n%s", out)
	}
}

func TestApply_FlowTrigger_ExecutionNamespaceSuffix(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: on_failure
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in: [SUCCESS]
      - type: io.kestra.plugin.core.condition.ExecutionNamespace
        namespace: prod
        comparison: SUFFIX
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ trigger.namespace endsWith 'prod' }}"`) {
		t.Errorf("missing expected endsWith clause, got:\n%s", out)
	}
}

func TestApply_FlowTrigger_ExecutionNamespaceExactGoesToNamespaceField(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: on_failure
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in: [SUCCESS]
      - type: io.kestra.plugin.core.condition.ExecutionNamespace
        namespace: company.prod
`
	out, _ := applyWithWarnings(t, in)
	// Exact ExecutionNamespace match populates the entry's `namespace:` key,
	// not a startsWith `when:` clause.
	if !strings.Contains(out, "namespace: company.prod") {
		t.Errorf("missing entry namespace field, got:\n%s", out)
	}
	if strings.Contains(out, "startsWith") {
		t.Error("exact match must not produce a startsWith `when:` clause")
	}
}

func TestApply_FlowTrigger_ExecutionLabels(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after_prod
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in: [SUCCESS]
      - type: io.kestra.plugin.core.condition.ExecutionLabels
        labels:
          env: production
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, "labels:") || !strings.Contains(out, "env: production") {
		t.Errorf("missing labels in dependsOn entry, got:\n%s", out)
	}
}

func TestApply_FlowTrigger_HasRetryAttempt(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after_flaky
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.HasRetryAttempt
`
	out, _ := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ hasRetryAttempt == true }}"`) {
		t.Errorf("missing hasRetryAttempt when clause, got:\n%s", out)
	}
}

// Top-level `states:` on a v1 Flow trigger is an additional status filter that
// must be folded into the new dependsOn entry (and removed from the trigger).
func TestApply_FlowTrigger_TopLevelStatesFoldedIntoEntry(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: flow_trigger
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionFlow
        flowId: upstream
        namespace: company.team
    states:
      - RUNNING
`
	out, _ := applyWithWarnings(t, in)
	if strings.Contains(out, "\n    states:") {
		t.Errorf("top-level `states:` on trigger must be removed after fold, got:\n%s", out)
	}
	for _, want := range []string{"dependsOn:", "flowId: upstream", "- RUNNING"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// In preconditions mode, `conditions` entries targeting per-flow fields
// (ExecutionStatus / ExecutionFlow / exact ExecutionNamespace / ExecutionLabels)
// would contend with the flows-derived entries — too ambiguous, refuse.
func TestApply_FlowTrigger_PreconditionsWithConflictingConditions_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: mixed
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in: [SUCCESS]
      - type: io.kestra.plugin.core.condition.ExecutionFlow
        flowId: upstream
        namespace: company.team
    preconditions:
      id: flows
      flows:
        - namespace: company.team
          flowId: other
          states: [SUCCESS]
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("must not rewrite when conditions fight preconditions — needs manual review")
	}
	if !strings.Contains(out, "conditions:") {
		t.Error("conditions must be preserved")
	}
	var gotCond, gotPre bool
	for _, w := range warnings {
		if strings.Contains(w, "trigger.conditions") {
			gotCond = true
		}
		if strings.Contains(w, "trigger.preconditions") {
			gotPre = true
		}
	}
	if !gotCond || !gotPre {
		t.Errorf("expected both conditions and preconditions warnings, got: %v", warnings)
	}
}

// `resetOnSuccess: true` is the v2 behavior, so it is dropped and the rest of
// the block still rewrites.
func TestApply_FlowTrigger_PreconditionsResetOnSuccessTrue_Dropped(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after_upstream
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: upstream
      resetOnSuccess: true
      flows:
        - namespace: company.team
          flowId: flow_a
          states: [SUCCESS]
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "preconditions:") {
		t.Error("preconditions should have been consumed")
	}
	if strings.Contains(out, "resetOnSuccess") || strings.Contains(out, "fireOnce") {
		t.Errorf("resetOnSuccess must not survive the rewrite:\n%s", out)
	}
	for _, want := range []string{"dependsOn:", "flowId: flow_a"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// `resetOnSuccess: false` has no v2 equivalent, so the rewrite is refused and
// the flow is left for manual review.
func TestApply_FlowTrigger_PreconditionsResetOnSuccessFalse_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after_upstream
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: upstream
      resetOnSuccess: false
      flows:
        - namespace: company.team
          flowId: flow_a
          states: [SUCCESS]
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, "preconditions:") {
		t.Error("must not rewrite when resetOnSuccess is false — needs manual review")
	}
	if len(warnings) == 0 {
		t.Error("expected a warning for resetOnSuccess: false")
	}
}

// Top-level `states:` + matching per-flow `states:` in preconditions.flows
// is a redundant but legal v1 shape. Values are equal — collapse to a single
// states field on the entry rather than refusing.
func TestApply_FlowTrigger_PreconditionsFlowsWithDuplicateStates(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: on_failure
    type: io.kestra.plugin.core.trigger.Flow
    states: [FAILED]
    preconditions:
      id: flowsFailure
      flows:
        - namespace: company.team
          flowId: flow_a
          states: [FAILED]
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "preconditions:") {
		t.Error("preconditions should have been consumed")
	}
	if strings.Contains(out, "\n    states:") {
		t.Error("top-level `states:` must be removed after folding into the entry")
	}
	for _, want := range []string{"dependsOn:", "flowId: flow_a", "states: [FAILED]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// Top-level `states:` that actually disagrees with per-flow `states:` changes
// semantics (v1 intersects the two sets). Refuse and warn.
func TestApply_FlowTrigger_PreconditionsFlowsWithDifferentStates_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: mismatch
    type: io.kestra.plugin.core.trigger.Flow
    states: [SUCCESS]
    preconditions:
      id: flows
      flows:
        - namespace: company.team
          flowId: flow_a
          states: [FAILED]
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("disagreeing states lists must not be auto-rewritten")
	}
	if !strings.Contains(out, "preconditions:") {
		t.Error("preconditions must be preserved")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.preconditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.preconditions warning, got: %v", warnings)
	}
}

// preconditions.flows (single entry) alone — the simplest case.
func TestApply_FlowTrigger_PreconditionsSingleFlow(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after_extract
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: flows
      flows:
        - namespace: company.team
          flowId: extract
          states: [SUCCESS]
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "preconditions:") {
		t.Error("preconditions should have been consumed")
	}
	for _, want := range []string{"dependsOn:", "flowId: extract", "namespace: company.team", "states: [SUCCESS]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// preconditions.flows combined with a when-producing condition
// (ExecutionOutputs) — the when is duplicated across each entry.
func TestApply_FlowTrigger_PreconditionsFlowsPlusExecutionOutputs(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after_extract
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionOutputs
        expression: "{{ outputs.row_count > 0 }}"
    preconditions:
      id: flows
      flows:
        - namespace: company.team
          flowId: extract
          states: [SUCCESS]
`
	out, warnings := applyWithWarnings(t, in)
	for _, want := range []string{
		"dependsOn:",
		"flowId: extract",
		"namespace: company.team",
		`when: "{{ outputs.row_count > 0 }}"`,
		"states: [SUCCESS]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "conditions:") || strings.Contains(out, "preconditions:") {
		t.Error("conditions and preconditions should both be consumed")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// preconditions.flows with N entries fans out into N dependsOn entries.
func TestApply_FlowTrigger_PreconditionsMultipleFlows(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after_staging
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: staging_deps
      flows:
        - namespace: company.team
          flowId: stg_sales
          states: [SUCCESS]
        - namespace: company.team
          flowId: stg_marketing
          states: [SUCCESS]
`
	out, _ := applyWithWarnings(t, in)
	for _, want := range []string{"flowId: stg_sales", "flowId: stg_marketing"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// `MultipleCondition` inside `conditions` fans into per-upstream dependsOn
// entries; its `window` maps to a top-level `window.lookback`. An outer
// ExecutionStatus contributes shared states applied to every entry.
func TestApply_FlowTrigger_MultipleConditionFansOut(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: multiple_listen_flow
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in:
          - SUCCESS
      - id: multiple
        type: io.kestra.plugin.core.condition.MultipleCondition
        window: P1D
        windowAdvance: P0D
        conditions:
          flow_a:
            type: io.kestra.plugin.core.condition.ExecutionFlow
            namespace: company.team
            flowId: flow_a
          flow_b:
            type: io.kestra.plugin.core.condition.ExecutionFlow
            namespace: company.team
            flowId: flow_b
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "conditions:") || strings.Contains(out, "MultipleCondition") {
		t.Errorf("conditions / MultipleCondition should have been consumed, got:\n%s", out)
	}
	for _, want := range []string{
		"dependsOn:",
		"flowId: flow_a",
		"flowId: flow_b",
		"window:",
		"lookback: P1D",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// A non-zero `windowAdvance` shifts the window forward; we don't translate
// that cleanly, so refuse and warn.
func TestApply_FlowTrigger_MultipleConditionNonZeroWindowAdvance_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: listen
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.MultipleCondition
        window: P1D
        windowAdvance: PT1H
        conditions:
          a:
            type: io.kestra.plugin.core.condition.ExecutionFlow
            namespace: company.team
            flowId: flow_a
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("non-zero windowAdvance must not be auto-rewritten")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.conditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.conditions warning, got: %v", warnings)
	}
}

// MultipleCondition with a non-ExecutionFlow inner is not in the supported
// subset — refuse.
func TestApply_FlowTrigger_MultipleConditionNonExecutionFlowInner_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: listen
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.MultipleCondition
        conditions:
          a:
            type: io.kestra.plugin.core.condition.ExecutionNamespace
            namespace: company
            comparison: PREFIX
`
	out, _ := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("MultipleCondition with non-ExecutionFlow inner must not be auto-rewritten")
	}
}

// preconditions.where translates each entry's AND-combined filter list into
// a `when:` clause on a fan-out dependsOn entry. NAMESPACE / FLOW_ID /
// EXPRESSION filters are supported.
func TestApply_FlowTrigger_PreconditionsWhere(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: listen
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: my_filter
      where:
        - id: flow1
          filters:
            - field: NAMESPACE
              type: STARTS_WITH
              value: io.kestra.tests
            - field: EXPRESSION
              type: IS_TRUE
              value: "{{ labels.some == 'label' }}"
`
	out, warnings := applyWithWarnings(t, in)
	want := `when: "{{ (trigger.namespace startsWith 'io.kestra.tests') and (labels.some == 'label') }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` clause, got:\n%s", out)
	}
	if strings.Contains(out, "preconditions:") {
		t.Error("preconditions should have been consumed")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// A where filter on an unsupported field (e.g. LABELS) has no clean v2
// when-mapping, so we refuse the whole rewrite.
func TestApply_FlowTrigger_PreconditionsWhereUnsupportedField_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: listen
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: my_filter
      where:
        - id: flow1
          filters:
            - field: LABELS
              type: CONTAINS
              value: "env:production"
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("unsupported where filter must not be auto-rewritten")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.preconditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.preconditions warning, got: %v", warnings)
	}
}

// preconditions with BOTH `flows:` and `where:` is ambiguous — refuse.
func TestApply_FlowTrigger_PreconditionsFlowsAndWhere_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: listen
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: both
      flows:
        - namespace: company.team
          flowId: extract
      where:
        - id: w
          filters:
            - field: NAMESPACE
              type: EQUALS
              value: company.team
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("preconditions with both flows and where must not be auto-rewritten")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.preconditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.preconditions warning, got: %v", warnings)
	}
}

// Non-DAILY_TIME_DEADLINE timeWindow types (DURATION_WINDOW, SLIDING_WINDOW,
// DAILY_TIME_WINDOW) are not yet mapped — fall back to warning.
func TestApply_FlowTrigger_PreconditionsUnsupportedTimeWindow_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: after
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: deps
      flows:
        - namespace: company.team
          flowId: extract
          states: [SUCCESS]
      timeWindow:
        type: DURATION_WINDOW
        duration: PT1H
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("unsupported timeWindow must cause refusal")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.preconditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.preconditions warning, got: %v", warnings)
	}
}

// A single-child Not wrapping a when-producing condition (ExecutionNamespace
// prefix/suffix, ExecutionOutputs, HasRetryAttempt) collapses into a single
// `when:` clause negated with `not (...)`.
func TestApply_FlowTrigger_NotWrappingExecutionNamespacePrefix(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: flow_condition
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.Not
        conditions:
          - type: io.kestra.plugin.core.condition.ExecutionNamespace
            namespace: company.analytics
            comparison: PREFIX
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, `when: "{{ not (trigger.namespace startsWith 'company.analytics') }}"`) {
		t.Errorf("missing expected negated when clause, got:\n%s", out)
	}
	if strings.Contains(out, "conditions:") {
		t.Error("conditions should have been consumed")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// Not wrapping ExecutionFlow inverts the flow match and becomes a negated
// when-clause: `not (trigger.flowId == 'X' and trigger.namespace == 'Y')`.
func TestApply_FlowTrigger_NotWrappingExecutionFlow(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: flow_condition
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.Not
        conditions:
          - type: io.kestra.plugin.core.condition.ExecutionFlow
            flowId: upstream
            namespace: company.team
`
	out, warnings := applyWithWarnings(t, in)
	want := `when: "{{ not (trigger.flowId == 'upstream' and trigger.namespace == 'company.team') }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// An Or wrapper combines when-convertible inner conditions (ExecutionFlow,
// ExecutionNamespace prefix/suffix/exact, ExecutionOutputs, HasRetryAttempt)
// into a single `(A) or (B)` Pebble expression. A sibling ExecutionStatus
// still contributes the `states:` filter at the dependsOn entry.
func TestApply_FlowTrigger_OrWrapper_MixedInner(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: alert
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in: [FAILED]
      - type: io.kestra.plugin.core.condition.Or
        conditions:
          - type: io.kestra.plugin.core.condition.ExecutionNamespace
            namespace: company.product
            prefix: true
          - type: io.kestra.plugin.core.condition.ExecutionFlow
            flowId: cleanup
            namespace: company.system
`
	out, warnings := applyWithWarnings(t, in)
	wantWhen := `when: "{{ (trigger.namespace startsWith 'company.product') or (trigger.flowId == 'cleanup' and trigger.namespace == 'company.system') }}"`
	if !strings.Contains(out, wantWhen) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if !strings.Contains(out, "states: [FAILED]") {
		t.Errorf("missing expected `states:` from sibling ExecutionStatus, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// An Or whose inner contains a condition with no when-mapping (e.g.
// ExecutionStatus, ExecutionLabels) must be left for manual rewrite.
func TestApply_FlowTrigger_OrWrapperWithUnsupportedInner_WarnsInstead(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: alert
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.Or
        conditions:
          - type: io.kestra.plugin.core.condition.ExecutionStatus
            in: [FAILED]
          - type: io.kestra.plugin.core.condition.ExecutionFlow
            flowId: cleanup
            namespace: company.system
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "dependsOn:") {
		t.Error("Or with an un-mappable inner must not be auto-rewritten")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "trigger.conditions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trigger.conditions warning, got: %v", warnings)
	}
}

// Flow trigger with `preconditions.flows` + `preconditions.timeWindow`
// (DAILY_TIME_DEADLINE) now rewrites: flows become dependsOn entries and the
// deadline moves to a top-level `window:` on the trigger.
func TestApply_FlowTrigger_PreconditionsFlowsAndDeadline(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: wait-for-upstream
    type: io.kestra.plugin.core.trigger.Flow
    preconditions:
      id: multi
      flows:
        - namespace: company.team
          flowId: extract
      timeWindow:
        type: DAILY_TIME_DEADLINE
        deadline: "09:00:00"
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "preconditions:") {
		t.Errorf("preconditions should have been consumed, got:\n%s", out)
	}
	for _, want := range []string{
		"dependsOn:",
		"flowId: extract",
		"namespace: company.team",
		"window:",
		`deadline: "09:00:00"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_DetectsRemovedTriggerConditions_IgnoresAssertTaskConditions(t *testing.T) {
	// `conditions:` on an Assert task is a list of Pebble expressions and is
	// NOT part of the removed trigger-conditions subsystem. Must not warn.
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: assert
    type: io.kestra.plugin.core.execution.Assert
    conditions:
      - "{{ outputs.something.code == 200 }}"
`
	_, warnings := applyWithWarnings(t, in)
	for _, w := range warnings {
		if strings.Contains(w, "trigger.conditions") {
			t.Errorf("Assert.conditions must not produce trigger.conditions warning, got: %s", w)
		}
	}
}

// ── Rule: renameTypes ─────────────────────────────────────────────────────────

func TestApply_RenameTypes_TemplateToSubflow(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: tmpl
    type: io.kestra.plugin.core.flow.Template
    templateId: my-template
    namespace: company.team
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.core.flow.Template") {
		t.Error("output still contains Template type")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.flow.Subflow") {
		t.Error("output missing Subflow type")
	}
	if strings.Contains(out, "templateId:") {
		t.Error("output still contains 'templateId:'")
	}
	if !strings.Contains(out, "flowId: my-template") {
		t.Error("output missing 'flowId: my-template'")
	}
}

func TestApply_RenameTypes_OldCoreTemplate(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: tmpl
    type: io.kestra.core.tasks.flows.Template
    templateId: old-template
`
	out := apply(t, in)
	if !strings.Contains(out, "io.kestra.plugin.core.flow.Subflow") {
		t.Error("output missing Subflow type")
	}
	if !strings.Contains(out, "flowId: old-template") {
		t.Error("output missing 'flowId: old-template'")
	}
}

func TestApply_RenameTypes_EchoToLog(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: say-hi
    type: io.kestra.plugin.core.debug.Echo
    format: hello
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.core.debug.Echo") {
		t.Error("output still contains Echo type")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.log.Log") {
		t.Error("output missing Log type")
	}
}

func TestApply_RenameTypes_OldCoreEcho(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: say-hi
    type: io.kestra.core.tasks.debugs.Echo
    format: hello
`
	out := apply(t, in)
	if !strings.Contains(out, "io.kestra.plugin.core.log.Log") {
		t.Error("output missing Log type for old core Echo path")
	}
}

// EachSequential (old io.kestra.core.tasks.flows.* path) is no longer rewritten
// to ForEach — ForEach is itself removed in v2. It is left intact and flagged for
// manual Loop migration.
func TestApply_EachSequential_OldPath_NotRewritten(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: loop
    type: io.kestra.core.tasks.flows.EachSequential
    value: "{{ inputs.items }}"
    tasks:
      - id: log
        type: io.kestra.plugin.core.log.Log
`
	out, warnings := applyWithWarnings(t, in)
	if !warningsContain(warnings, "Loop") {
		t.Errorf("expected a Loop-migration warning; got warnings: %v", warnings)
	}
	if strings.Contains(out, "io.kestra.plugin.core.flow.ForEach") {
		t.Errorf("EachSequential must no longer be rewritten to ForEach; got:\n%s", out)
	}
	if !strings.Contains(out, "io.kestra.core.tasks.flows.EachSequential") {
		t.Errorf("EachSequential should be left intact (warning-only); got:\n%s", out)
	}
}

func TestApply_EachParallel_NotRewritten(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: loop
    type: io.kestra.plugin.core.flow.EachParallel
    value: "{{ inputs.items }}"
    tasks:
      - id: log
        type: io.kestra.plugin.core.log.Log
`
	out, warnings := applyWithWarnings(t, in)
	if !warningsContain(warnings, "Loop") {
		t.Errorf("expected a Loop-migration warning; got warnings: %v", warnings)
	}
	if strings.Contains(out, "io.kestra.plugin.core.flow.ForEach") {
		t.Errorf("EachParallel must no longer be rewritten to ForEach; got:\n%s", out)
	}
	if !strings.Contains(out, "io.kestra.plugin.core.flow.EachParallel") {
		t.Errorf("EachParallel should be left intact (warning-only); got:\n%s", out)
	}
}

func TestApply_RenameTypes_StateToKV(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: get-state
    type: io.kestra.plugin.core.state.Get
    name: my-state
  - id: set-state
    type: io.kestra.plugin.core.state.Set
    name: my-state
  - id: del-state
    type: io.kestra.plugin.core.state.Delete
    name: my-state
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.core.state.") {
		t.Error("output still contains state.* types")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.kv.Get") {
		t.Error("output missing kv.Get")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.kv.Set") {
		t.Error("output missing kv.Set")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.kv.Delete") {
		t.Error("output missing kv.Delete")
	}
}

func TestApply_RenameTypes_StorageAliases(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: purge
    type: io.kestra.plugin.core.storage.Purge
  - id: purge-exec
    type: io.kestra.plugin.core.storage.PurgeExecution
`
	out := apply(t, in)
	// PurgeExecutions lives in the `execution` subpackage —
	// io.kestra.plugin.core.storage.PurgeExecutions never existed.
	if !strings.Contains(out, "io.kestra.plugin.core.execution.PurgeExecutions") {
		t.Error("output missing execution.PurgeExecutions")
	}
	if strings.Contains(out, "io.kestra.plugin.core.storage.PurgeExecutions") {
		t.Error("output contains nonexistent storage.PurgeExecutions type")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.storage.PurgeCurrentExecutionFiles") {
		t.Error("output missing PurgeCurrentExecutionFiles")
	}
}

func TestApply_RenameTypes_OldCoreTrigger(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
triggers:
  - id: sched
    type: io.kestra.core.models.triggers.types.Schedule
    cron: "0 9 * * *"
`
	out := apply(t, in)
	if !strings.Contains(out, "io.kestra.plugin.core.trigger.Schedule") {
		t.Error("output missing new Schedule trigger type")
	}
}

func TestApply_OldCoreConditionType_RewrittenToWhen(t *testing.T) {
	// Old-path condition types (io.kestra.core.models.conditions.types.*) are
	// recognised by the rewriter and produce a `when:` expression, the same as
	// their new-path equivalents.
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
triggers:
  - id: sched
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 9 * * *"
    conditions:
      - type: io.kestra.core.models.conditions.types.DayWeekCondition
        dayOfWeek: MONDAY
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "io.kestra.core.models.conditions.types.DayWeekCondition") {
		t.Error("old-path DayWeekCondition should have been consumed by the rewrite")
	}
	if !strings.Contains(out, `when: "{{ dayOfWeek(trigger.date) == 'MONDAY' }}"`) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_RenameTypes_LocalFilesToWorkingDirectory(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: files
    type: io.kestra.plugin.core.storage.LocalFiles
    inputs:
      data.csv: "{{ outputs.extract.uri }}"
`
	out := apply(t, in)
	if strings.Contains(out, "LocalFiles") {
		t.Error("output still contains LocalFiles")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.flow.WorkingDirectory") {
		t.Error("output missing WorkingDirectory type")
	}
}

// ── Trigger conditions subsystem rewrite (Schedule/Webhook) ──────────────────

func TestApply_RewriteScheduleConditions_ConditionSuffixVariants(t *testing.T) {
	// `io.kestra.plugin.core.condition.*` types with a trailing `Condition`
	// suffix are the same conditions — must still be rewritten.
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
triggers:
  - id: sched
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 9 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.WeekendCondition
      - type: io.kestra.plugin.core.condition.HasRetryAttemptCondition
`
	out, warnings := applyWithWarnings(t, in)
	want := `when: "{{ (isWeekend(trigger.date)) and (hasRetryAttempt == true) }}"`
	if !strings.Contains(out, want) {
		t.Errorf("missing expected `when:` expression, got:\n%s", out)
	}
	if strings.Contains(out, "WeekendCondition") || strings.Contains(out, "HasRetryAttemptCondition") {
		t.Error("old condition types should have been consumed by the rewrite")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// ── Rule: removeDeprecatedProperties ──────────────────────────────────────────

func TestApply_RemoveSubflowOutputs(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: sub
    type: io.kestra.plugin.core.flow.Subflow
    flowId: child-flow
    namespace: company.team
    outputs:
      result: "{{ outputs.child-task.value }}"
`
	out := apply(t, in)
	if strings.Contains(out, "outputs:") {
		t.Error("output still contains 'outputs:' on Subflow")
	}
}

func TestApply_RemoveScheduleBackfills(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 9 * * *"
    backfills:
      - start: "2024-01-01T00:00:00Z"
`
	out := apply(t, in)
	if strings.Contains(out, "backfills:") {
		t.Error("output still contains 'backfills:' on Schedule")
	}
}

func TestApply_MigratePurgeKVExpiredOnly_True(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: purge
    type: io.kestra.plugin.core.kv.PurgeKV
    namespace: company.team
    expiredOnly: true
`
	out := apply(t, in)
	if !strings.Contains(out, "behavior:") {
		t.Errorf("output missing 'behavior:' on PurgeKV, got:\n%s", out)
	}
	if !strings.Contains(out, "type: key") {
		t.Errorf("output missing 'type: key' inside behavior, got:\n%s", out)
	}
	if !strings.Contains(out, "expiredOnly: true") {
		t.Errorf("output missing 'expiredOnly: true' inside behavior, got:\n%s", out)
	}
}

func TestApply_MigratePurgeKVExpiredOnly_FalsePreserved(t *testing.T) {
	// Deleting `expiredOnly: false` would silently flip the task to v2's
	// expired-only default — the value must be carried into behavior.
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: purge
    type: io.kestra.plugin.core.kv.PurgeKV
    namespace: company.team
    expiredOnly: false
`
	out := apply(t, in)
	if !strings.Contains(out, "behavior:") {
		t.Errorf("output missing 'behavior:' on PurgeKV, got:\n%s", out)
	}
	if !strings.Contains(out, "expiredOnly: false") {
		t.Errorf("output missing 'expiredOnly: false' inside behavior, got:\n%s", out)
	}
}

func TestApply_MigratePurgeKVExpiredOnly_ExistingBehaviorUntouched(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: purge
    type: io.kestra.plugin.core.kv.PurgeKV
    namespace: company.team
    expiredOnly: false
    behavior:
      type: version
`
	out := apply(t, in)
	if !strings.Contains(out, "expiredOnly: false") {
		t.Errorf("explicit behavior present — deprecated expiredOnly must be left untouched, got:\n%s", out)
	}
	if !strings.Contains(out, "type: version") {
		t.Errorf("existing behavior must be preserved, got:\n%s", out)
	}
}

func TestApply_MigratePurgeKVExpiredOnly_StayV1Preserves(t *testing.T) {
	// behavior only exists on v1.3.28+, so the conversion is gated to the v2
	// path; the deprecated property still parses on v2 either way.
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: purge
    type: io.kestra.plugin.core.kv.PurgeKV
    namespace: company.team
    expiredOnly: false
`
	out, _, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if !strings.Contains(string(out), "expiredOnly: false") {
		t.Errorf("StayV1Compatible should preserve expiredOnly, got:\n%s", out)
	}
	if strings.Contains(string(out), "behavior:") {
		t.Errorf("StayV1Compatible should not emit behavior, got:\n%s", out)
	}
}

func TestApply_RemoveTriggerMinLogLevel(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 9 * * *"
    minLogLevel: WARN
`
	out := apply(t, in)
	if strings.Contains(out, "minLogLevel:") {
		t.Error("output still contains 'minLogLevel:' on trigger")
	}
}

// ── Rule: renameExitCanceled ──────────────────────────────────────────────────

func TestApply_RenameExitCanceled(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: exit
    type: io.kestra.plugin.core.execution.Exit
    state: CANCELED
`
	out := apply(t, in)
	// CANCELED (single L) maps to CANCELLED — the same state under its v2-only
	// spelling. NOT KILLED, which would stop sibling running tasks.
	if strings.Contains(out, "state: CANCELED\n") {
		t.Error("output still contains 'state: CANCELED'")
	}
	if !strings.Contains(out, "state: CANCELLED") {
		t.Error("output missing 'state: CANCELLED'")
	}
}

func TestApply_RenameExitCanceled_OnlyOnExit(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: other
    type: io.kestra.plugin.core.log.Log
    state: CANCELED
`
	out := apply(t, in)
	if !strings.Contains(out, "state: CANCELED") {
		t.Error("CANCELED on non-Exit task should be preserved")
	}
}

// ── Rule: migrateWorkerGroupToWorkerSelector ─────────────────────────────────

func TestApply_WorkerGroupToWorkerSelector(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: heavy
    type: io.kestra.plugin.core.log.Log
    message: hello
    workerGroup:
      key: gpu
      fallback: CANCEL
`
	out, warnings := applyWithWarnings(t, in)
	if strings.Contains(out, "workerGroup:") {
		t.Errorf("output still contains 'workerGroup:', got:\n%s", out)
	}
	if !strings.Contains(out, "workerSelector:") {
		t.Errorf("output missing 'workerSelector:', got:\n%s", out)
	}
	if !strings.Contains(out, "- gpu") {
		t.Errorf("output missing tags entry 'gpu', got:\n%s", out)
	}
	if !strings.Contains(out, "fallback: CANCEL") {
		t.Errorf("explicit fallback must be carried over, got:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_WorkerGroupToWorkerSelector_PinsWaitFallback(t *testing.T) {
	// v1 waited by default when no worker was available; v2's workerSelector
	// defaults to FAIL — WAIT must be pinned to preserve behavior.
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: heavy
    type: io.kestra.plugin.core.log.Log
    message: hello
    workerGroup:
      key: etl-workers
`
	out := apply(t, in)
	if !strings.Contains(out, "fallback: WAIT") {
		t.Errorf("fallback must be pinned to WAIT when v1 omitted it, got:\n%s", out)
	}
}

func TestApply_WorkerGroupToWorkerSelector_NonRFC1123KeyWarns(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: heavy
    type: io.kestra.plugin.core.log.Log
    message: hello
    workerGroup:
      key: GPU_Workers
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, "workerGroup:") {
		t.Errorf("non-compliant key must be left untouched, got:\n%s", out)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "RFC 1123") {
		t.Errorf("expected one RFC 1123 warning, got: %v", warnings)
	}
}

func TestApply_WorkerGroupToWorkerSelector_TemplatedKeyWarns(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: heavy
    type: io.kestra.plugin.core.log.Log
    message: hello
    workerGroup:
      key: "{{ inputs.group }}"
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, "workerGroup:") {
		t.Errorf("templated key must be left untouched, got:\n%s", out)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "templated") {
		t.Errorf("expected one templated-key warning, got: %v", warnings)
	}
}

func TestApply_WorkerGroupToWorkerSelector_FallbackWithoutKeyWarns(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: heavy
    type: io.kestra.plugin.core.log.Log
    message: hello
    workerGroup:
      fallback: WAIT
`
	out, warnings := applyWithWarnings(t, in)
	if !strings.Contains(out, "workerGroup:") {
		t.Errorf("workerGroup without key must be left untouched, got:\n%s", out)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "without a key") {
		t.Errorf("expected one no-key warning, got: %v", warnings)
	}
}

func TestApply_WorkerGroupToWorkerSelector_StayV1Preserves(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: heavy
    type: io.kestra.plugin.core.log.Log
    message: hello
    workerGroup:
      key: gpu
`
	out, _, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if !strings.Contains(string(out), "workerGroup:") {
		t.Errorf("StayV1Compatible should preserve workerGroup, got:\n%s", out)
	}
	if strings.Contains(string(out), "workerSelector:") {
		t.Errorf("StayV1Compatible should not emit workerSelector, got:\n%s", out)
	}
}

// ── Detection: detectPebbleVersionArg ────────────────────────────────────────

func TestApply_DetectPebbleVersionArg(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: show
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(namespace.files.config, version=2) }}"
`
	_, warnings := applyWithWarnings(t, in)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "revision") {
		t.Errorf("expected one version=→revision warning, got: %v", warnings)
	}
}

func TestApply_DetectPebbleVersionArg_FileURI(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: show
    type: io.kestra.plugin.core.log.Log
    message: "{{ fileURI(namespace.files.config, version = 3) }}"
`
	_, warnings := applyWithWarnings(t, in)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "revision") {
		t.Errorf("expected one version=→revision warning, got: %v", warnings)
	}
}

func TestApply_DetectPebbleVersionArg_NoFalsePositives(t *testing.T) {
	// `revision=` is the fixed form; a plain `version:` property and a read()
	// without the named arg must not warn.
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: show
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(namespace.files.config, revision=2) }}"
  - id: other
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(namespace.files.config) }}"
    version: 2
`
	_, warnings := applyWithWarnings(t, in)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestApply_DetectPebbleVersionArg_StayV1Skips(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: show
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(namespace.files.config, version=2) }}"
`
	_, warnings, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings under StayV1Compatible, got: %v", warnings)
	}
}

// ── Rule: renameMultiselectOptions ────────────────────────────────────────────

func TestApply_RenameMultiselectOptions(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: tags
    type: MULTISELECT
    options:
      - alpha
      - beta
      - gamma
`
	out := apply(t, in)
	if strings.Contains(out, "options:") {
		t.Error("output still contains 'options:' on MULTISELECT input")
	}
	if !strings.Contains(out, "values:") {
		t.Error("output missing 'values:' on MULTISELECT input")
	}
}

func TestApply_RenameMultiselectOptions_OnlyOnMultiselect(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: choice
    type: SELECT
    options:
      - A
      - B
`
	out := apply(t, in)
	// SELECT inputs should keep options (only MULTISELECT renames)
	if !strings.Contains(out, "options:") {
		t.Error("options on SELECT input should be preserved")
	}
}

// ── Rule: renameTypes (third-party plugin renames) ───────────────────────────

func TestApply_RenameTypes_NotificationsSlack(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: notify
    type: io.kestra.plugin.notifications.slack.SlackIncomingWebhook
    url: https://hooks.slack.com/services/xxx
    payload: "{}"
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.notifications.slack") {
		t.Error("output still contains notifications.slack type")
	}
	if !strings.Contains(out, "io.kestra.plugin.slack.notifications.SlackIncomingWebhook") {
		t.Error("output missing slack.notifications.SlackIncomingWebhook")
	}
}

func TestApply_RenameTypes_NotificationsSlackExecution(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: notify
    type: io.kestra.plugin.notifications.slack.SlackExecution
    url: https://hooks.slack.com/services/xxx
`
	out := apply(t, in)
	if !strings.Contains(out, "io.kestra.plugin.slack.notifications.SlackExecution") {
		t.Error("output missing slack.notifications.SlackExecution")
	}
}

func TestApply_RenameTypes_SlackInternalRestructure(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: webhook
    type: io.kestra.plugin.slack.SlackIncomingWebhook
    url: https://hooks.slack.com/services/xxx
  - id: exec
    type: io.kestra.plugin.slack.SlackExecution
    url: https://hooks.slack.com/services/xxx
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.slack.SlackIncomingWebhook") {
		t.Error("output still contains slack.SlackIncomingWebhook")
	}
	if strings.Contains(out, "io.kestra.plugin.slack.SlackExecution") && !strings.Contains(out, "io.kestra.plugin.slack.notifications.SlackExecution") {
		t.Error("output still contains slack.SlackExecution without notifications subpackage")
	}
	if !strings.Contains(out, "io.kestra.plugin.slack.notifications.SlackIncomingWebhook") {
		t.Error("output missing slack.notifications.SlackIncomingWebhook")
	}
}

func TestApply_RenameTypes_NotificationsMail(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: email
    type: io.kestra.plugin.notifications.mail.MailSend
    to: test@example.com
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.notifications.mail") {
		t.Error("output still contains notifications.mail type")
	}
	if !strings.Contains(out, "io.kestra.plugin.email.MailSend") {
		t.Error("output missing email.MailSend")
	}
}

func TestApply_RenameTypes_NotificationsDiscord(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: alert
    type: io.kestra.plugin.notifications.discord.DiscordExecution
    url: https://discord.com/api/webhooks/xxx
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.notifications.discord") {
		t.Error("output still contains notifications.discord type")
	}
	if !strings.Contains(out, "io.kestra.plugin.discord.DiscordExecution") {
		t.Error("output missing discord.DiscordExecution")
	}
}

func TestApply_RenameTypes_KubernetesCoreSubpackage(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: pod
    type: io.kestra.plugin.kubernetes.PodCreate
    namespace: default
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.kubernetes.PodCreate") && !strings.Contains(out, "io.kestra.plugin.kubernetes.core.PodCreate") {
		t.Error("output still contains kubernetes.PodCreate without core subpackage")
	}
	if !strings.Contains(out, "io.kestra.plugin.kubernetes.core.PodCreate") {
		t.Error("output missing kubernetes.core.PodCreate")
	}
}

func TestApply_RenameTypes_DatagenCoreSubpackage(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: gen
    type: io.kestra.plugin.datagen.Generate
    count: 10
`
	out := apply(t, in)
	if !strings.Contains(out, "io.kestra.plugin.datagen.core.Generate") {
		t.Error("output missing datagen.core.Generate")
	}
}

func TestApply_RenameTypes_AstraDBToCassandra(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: query
    type: io.kestra.plugin.astradb.Query
    cql: "SELECT * FROM table"
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.astradb.Query") {
		t.Error("output still contains astradb.Query")
	}
	if !strings.Contains(out, "io.kestra.plugin.cassandra.astradb.Query") {
		t.Error("output missing cassandra.astradb.Query")
	}
}

func TestApply_RenameTypes_FsHTTPToCoreHTTP(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: req
    type: io.kestra.plugin.fs.http.Request
    uri: https://example.com
  - id: dl
    type: io.kestra.plugin.fs.http.Download
    uri: https://example.com/file.csv
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.fs.http.Request") {
		t.Error("output still contains fs.http.Request")
	}
	if strings.Contains(out, "io.kestra.plugin.fs.http.Download") {
		t.Error("output still contains fs.http.Download")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.http.Request") {
		t.Error("output missing core.http.Request")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.http.Download") {
		t.Error("output missing core.http.Download")
	}
}

func TestApply_RenameTypes_LogFetchToKestraLogs(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: fetch_logs
    type: io.kestra.plugin.core.log.Fetch
    level: INFO
`
	out := apply(t, in)
	if strings.Contains(out, "io.kestra.plugin.core.log.Fetch") {
		t.Error("output still contains io.kestra.plugin.core.log.Fetch")
	}
	if !strings.Contains(out, "io.kestra.plugin.kestra.logs.Fetch") {
		t.Error("output missing io.kestra.plugin.kestra.logs.Fetch")
	}
}

// ── Combined / integration ────────────────────────────────────────────────────

func TestApply_CombinedMigration(t *testing.T) {
	in := `
id: legacy-flow
namespace: company.team
taskDefaults:
  - type: io.kestra.plugin.core.http.Request
    values:
      retry:
        maxAttempt: 3
        type: constant
inputs:
  - name: enabled
    type: BOOLEAN
  - name: mode
    type: ENUM
    values:
      - fast
      - slow
tasks:
  - id: echo
    type: io.kestra.plugin.core.debug.Echo
    format: "{{ inputs.mode }}"
  - id: each
    type: io.kestra.plugin.core.flow.EachSequential
    value: "[1,2,3]"
    tasks:
      - id: log
        type: io.kestra.plugin.core.log.Log
triggers:
  - id: sched
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 9 * * *"
    scheduleConditions:
      - type: io.kestra.plugin.core.condition.DayWeekCondition
        dayOfWeek: MONDAY
`
	out := apply(t, in)

	// Input name → id
	if strings.Contains(out, "name: enabled") {
		t.Error("input name not renamed to id")
	}
	// BOOLEAN → BOOL
	if strings.Contains(out, "type: BOOLEAN") {
		t.Error("BOOLEAN not renamed to BOOL")
	}
	// ENUM → SELECT
	if strings.Contains(out, "type: ENUM") {
		t.Error("ENUM not renamed to SELECT")
	}
	// taskDefaults is left intact on the v2 path and flagged for manual rewrite —
	// pluginDefaults, the key it used to be renamed into, is itself removed in v2.
	if !strings.Contains(out, "taskDefaults:") {
		t.Error("taskDefaults should be left intact (warning-only)")
	}
	// maxAttempt → maxAttempts (still rewritten inside the untouched block)
	if strings.Contains(out, "maxAttempt:") {
		t.Error("maxAttempt not renamed")
	}
	// Echo → Log
	if strings.Contains(out, "debug.Echo") {
		t.Error("Echo not renamed to Log")
	}
	// EachSequential is no longer rewritten to ForEach (ForEach is itself removed
	// in v2); it is left intact and flagged for manual Loop migration.
	if strings.Contains(out, "io.kestra.plugin.core.flow.ForEach") {
		t.Error("EachSequential must not be rewritten to ForEach")
	}
	if !strings.Contains(out, "io.kestra.plugin.core.flow.EachSequential") {
		t.Error("EachSequential should be left intact (warning-only)")
	}
	// scheduleConditions and DayWeekCondition are now left for manual rewrite;
	// conditions subsystem was fully removed in v2 (see detection tests).
}

// ── Rule: migrateHTTPBasicAuth ────────────────────────────────────────────────

func TestApply_MigrateHTTPBasicAuth(t *testing.T) {
	in := `
id: test
namespace: test
tasks:
  - id: req
    type: io.kestra.plugin.core.http.Request
    uri: https://example.com
    options:
      basicAuthUser: myuser
      basicAuthPassword: mypass
`
	out := apply(t, in)
	if strings.Contains(out, "basicAuthUser") {
		t.Error("basicAuthUser not removed")
	}
	if strings.Contains(out, "basicAuthPassword") {
		t.Error("basicAuthPassword not removed")
	}
	if !strings.Contains(out, "username: myuser") {
		t.Error("expected auth.username")
	}
	if !strings.Contains(out, "password: mypass") {
		t.Error("expected auth.password")
	}
	if !strings.Contains(out, "type: BASIC") {
		t.Error("expected auth.type BASIC")
	}
}

func TestApply_MigrateHTTPBasicAuth_NoBasicAuth(t *testing.T) {
	in := `
id: test
namespace: test
tasks:
  - id: req
    type: io.kestra.plugin.core.http.Request
    uri: https://example.com
    options:
      followRedirects: true
`
	out := apply(t, in)
	if strings.Contains(out, "auth") {
		t.Error("auth block should not be added when no basicAuth present")
	}
}

// ── Rule: removeDeprecatedHTTPOptions ─────────────────────────────────────────

func TestApply_RemoveConnectionPoolIdleTimeout(t *testing.T) {
	in := `
id: test
namespace: test
tasks:
  - id: req
    type: io.kestra.plugin.core.http.Request
    uri: https://example.com
    options:
      connectionPoolIdleTimeout: PT1M
`
	out := apply(t, in)
	if strings.Contains(out, "connectionPoolIdleTimeout") {
		t.Error("connectionPoolIdleTimeout not removed")
	}
}

// ── Rule: removeDeprecatedProperties (backfill singular) ──────────────────────

func TestApply_RemoveScheduleBackfillSingular(t *testing.T) {
	in := `
id: test
namespace: test
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: hello
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: 0 8 1 * *
    backfill:
      start: 2023-01-01T00:00:00Z
`
	out := apply(t, in)
	if strings.Contains(out, "backfill") {
		t.Error("backfill (singular) not removed from Schedule")
	}
}

// ── Rule: renameConditionSuffix (MultipleCondition exclusion) ─────────────────

func TestApply_RenameConditionSuffix_MultipleConditionPreserved(t *testing.T) {
	in := `
id: test
namespace: test
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: hello
triggers:
  - id: flow
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - id: multi
        type: io.kestra.plugin.core.condition.MultipleCondition
        window: P1D
        windowAdvance: PT0S
`
	out := apply(t, in)
	if !strings.Contains(out, "MultipleCondition") {
		t.Error("MultipleCondition should not be renamed")
	}
}

// ── Sanity checks: Apply must not error on real-world v2 flows ────────────────

func TestApply_SanityChecks(t *testing.T) {
	root := filepath.Join("..", "..", "input-flows", "sanitychecks")
	var count int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read file: %v", err)
			}
			_, _, err = Apply(data)
			if err != nil {
				t.Errorf("Apply failed: %v", err)
			}
		})
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if count == 0 {
		t.Fatal("no YAML files found in sanitychecks directory")
	}
	t.Logf("sanity-checked %d flows", count)
}

// ── Rule: removeRequiredFalseWithDefaults ────────────────────────────────────

func TestApply_RemoveRequiredFalseWithDefaults(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: user
    type: STRING
    defaults: student
    required: false
  - id: age
    type: INT
    defaults: 42
    required: false
`
	out := apply(t, in)
	if strings.Contains(out, "required: false") {
		t.Error("output still contains 'required: false' on inputs with defaults")
	}
	if !strings.Contains(out, "defaults: student") || !strings.Contains(out, "defaults: 42") {
		t.Error("defaults values should be preserved")
	}
}

func TestApply_RemoveRequiredFalseWithDefaults_KeepsRequiredFalseWithoutDefaults(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: name
    type: STRING
    required: false
`
	out := apply(t, in)
	if !strings.Contains(out, "required: false") {
		t.Error("required: false without defaults should be preserved")
	}
}

func TestApply_RemoveRequiredFalseWithDefaults_IgnoresRequiredTrue(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
inputs:
  - id: user
    type: STRING
    defaults: admin
    required: true
`
	out := apply(t, in)
	if !strings.Contains(out, "required: true") {
		t.Error("required: true should be preserved")
	}
}

// ── Rule: renameReservedFlowIDs ─────────────────────────────────────────────

func TestApply_RenameReservedFlowIDs(t *testing.T) {
	for _, reserved := range []string{"pause", "resume", "force-run", "change-status", "kill", "executions", "search", "source", "disable", "enable"} {
		t.Run(reserved, func(t *testing.T) {
			in := "id: " + reserved + "\nnamespace: company.team\ntasks:\n  - id: hello\n    type: io.kestra.plugin.core.log.Log\n    message: hello\n"
			out := apply(t, in)
			if strings.Contains(out, "id: "+reserved+"\n") {
				t.Errorf("output still contains reserved flow id %q", reserved)
			}
			if !strings.Contains(out, "id: "+reserved+"-flow") {
				t.Errorf("expected flow id %q but not found in output", reserved+"-flow")
			}
		})
	}
}

func TestApply_RenameReservedFlowIDs_NonReservedUntouched(t *testing.T) {
	in := `
id: my-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hello
`
	out := apply(t, in)
	if !strings.Contains(out, "id: my-flow") {
		t.Error("non-reserved flow id should remain unchanged")
	}
}

func TestApply_RenameReservedFlowIDs_TaskIDsUntouched(t *testing.T) {
	in := `
id: my-flow
namespace: company.team
tasks:
  - id: pause
    type: io.kestra.plugin.core.log.Log
    message: hello
  - id: search
    type: io.kestra.plugin.core.log.Log
    message: world
`
	out := apply(t, in)
	if !strings.Contains(out, "id: my-flow") {
		t.Error("flow id should remain unchanged")
	}
	// Task IDs named "pause"/"search" must NOT be renamed
	if !strings.Contains(out, "- id: pause") {
		t.Error("task id 'pause' should not be renamed")
	}
	if !strings.Contains(out, "- id: search") {
		t.Error("task id 'search' should not be renamed")
	}
}

// ── Removed type detection ──────────────────────────────────────────────────

func TestApply_DetectsRemovedType_MultipleCondition(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
triggers:
  - id: flow_trigger
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - id: multi
        type: io.kestra.plugin.core.condition.MultipleCondition
        conditions:
          cond_a:
            type: io.kestra.plugin.core.condition.ExecutionStatus
            in:
              - SUCCESS
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hello
`
	_, warnings := applyWithWarnings(t, in)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for MultipleCondition but got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "MultipleCondition") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about MultipleCondition, got: %v", warnings)
	}
}

func TestApply_DetectsRemovedType_NashornEval(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: script
    type: io.kestra.plugin.scripts.nashorn.Eval
    script: "1 + 1"
`
	_, warnings := applyWithWarnings(t, in)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for nashorn.Eval but got none")
	}
	if !strings.Contains(warnings[0], "nashorn.Eval") {
		t.Errorf("expected warning about nashorn.Eval, got: %s", warnings[0])
	}
}

func TestApply_DetectsRemovedType_Multiple(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: count_execs
    type: io.kestra.plugin.core.execution.Count
  - id: resume_exec
    type: io.kestra.plugin.core.execution.Resume
`
	_, warnings := applyWithWarnings(t, in)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

// ── Rule: migrateDbtBuildToDbtCLI ─────────────────────────────────────────────

func TestApply_MigrateDbtBuildToDbtCLI(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: dbt_build
    type: io.kestra.plugin.dbt.cli.Build
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
    containerImage: ghcr.io/kestra-io/dbt-bigquery:latest
    dbtPath: /usr/local/bin/dbt
`
	out, warnings := applyWithWarnings(t, in)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings after automated migration, got: %v", warnings)
	}
	if strings.Contains(out, "io.kestra.plugin.dbt.cli.Build") {
		t.Error("output still contains deprecated dbt.cli.Build type")
	}
	if !strings.Contains(out, "io.kestra.plugin.dbt.cli.DbtCLI") {
		t.Error("output missing dbt.cli.DbtCLI replacement")
	}
	if !strings.Contains(out, "commands:") || !strings.Contains(out, "- dbt build") {
		t.Errorf("output missing commands: [dbt build], got:\n%s", out)
	}
	if strings.Contains(out, "dbtPath") {
		t.Errorf("dbtPath should be removed (not a DbtCLI property), got:\n%s", out)
	}
}

func TestApply_MigrateDbtBuildToDbtCLI_PromotesDockerOptionsImage(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: dbt_build
    type: io.kestra.plugin.dbt.cli.Build
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
    dockerOptions:
      image: ghcr.io/kestra-io/dbt-bigquery:latest
`
	out := apply(t, in)
	if strings.Contains(out, "dockerOptions") {
		t.Errorf("dockerOptions should be removed (not a DbtCLI property), got:\n%s", out)
	}
	if !strings.Contains(out, "containerImage: ghcr.io/kestra-io/dbt-bigquery:latest") {
		t.Errorf("dockerOptions.image should be promoted to containerImage, got:\n%s", out)
	}
}

func TestApply_MigrateDbtBuildToDbtCLI_KeepsExistingContainerImage(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: dbt_build
    type: io.kestra.plugin.dbt.cli.Build
    containerImage: ghcr.io/user/custom-dbt:v1
    dockerOptions:
      image: ghcr.io/kestra-io/dbt-bigquery:latest
`
	out := apply(t, in)
	if !strings.Contains(out, "containerImage: ghcr.io/user/custom-dbt:v1") {
		t.Errorf("existing containerImage should be kept, got:\n%s", out)
	}
	if strings.Contains(out, "dbt-bigquery:latest") {
		t.Errorf("should not overwrite existing containerImage with dockerOptions.image, got:\n%s", out)
	}
}

func TestApply_MigrateDbtBuildToDbtCLI_PreservesExistingCommands(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: dbt_build
    type: io.kestra.plugin.dbt.cli.Build
    commands:
      - dbt deps
      - dbt test
`
	out := apply(t, in)
	if !strings.Contains(out, "io.kestra.plugin.dbt.cli.DbtCLI") {
		t.Error("output missing dbt.cli.DbtCLI replacement")
	}
	if !strings.Contains(out, "- dbt deps") || !strings.Contains(out, "- dbt test") {
		t.Errorf("existing commands list was not preserved, got:\n%s", out)
	}
	if strings.Contains(out, "- dbt build") {
		t.Errorf("should not add `dbt build` when commands already set, got:\n%s", out)
	}
}

func TestApply_NoWarningsForCleanFlow(t *testing.T) {
	in := `
id: test-flow
namespace: company.team
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hello
`
	_, warnings := applyWithWarnings(t, in)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for clean flow, got: %v", warnings)
	}
}

// ── Blank line preservation ──────────────────────────────────────────────────

func TestApply_PreservesBlankLinesBetweenTopLevelSections(t *testing.T) {
	in := `id: test-flow
namespace: company.team

inputs:
  - id: foo
    type: BOOLEAN

tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hello
`
	out := apply(t, in)
	// BOOLEAN → BOOL triggers a migration, so this hits the restore path.
	if !strings.Contains(out, "BOOL") {
		t.Fatalf("expected BOOL rename to fire; got:\n%s", out)
	}
	if !strings.Contains(out, "company.team\n\ninputs:") {
		t.Errorf("expected blank line before inputs:; got:\n%s", out)
	}
	if !strings.Contains(out, "type: BOOL\n\ntasks:") {
		t.Errorf("expected blank line before tasks:; got:\n%s", out)
	}
}

func TestApply_PreservesBlankLinesBetweenInputs(t *testing.T) {
	in := `id: test-flow
namespace: company.team
inputs:
  - id: foo
    type: BOOLEAN

  - id: bar
    type: STRING
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hello
`
	out := apply(t, in)
	if !strings.Contains(out, "type: BOOL\n\n  - id: bar") {
		t.Errorf("expected blank line between input items preserved; got:\n%s", out)
	}
}

func TestApply_BlankLineBeforeRewrittenAnchorIsDropped(t *testing.T) {
	// The blank line before `name:` would anchor on `name:`, which gets
	// rewritten to `id:`. The blank line is silently dropped — correct, since
	// the original context no longer exists.
	in := `id: test-flow
namespace: company.team
inputs:

  - name: foo
    type: STRING
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hello
`
	out := apply(t, in)
	if !strings.Contains(out, "- id: foo") {
		t.Fatalf("expected name→id rename; got:\n%s", out)
	}
	// Sanity: output should be valid YAML-ish — no crash, correct structure.
	if strings.Contains(out, "- name: foo") {
		t.Errorf("name: should have been renamed to id:; got:\n%s", out)
	}
}

// ── restoreUnchangedBlocks ────────────────────────────────────────────────────

// Verifies that a literal (|) block scalar with trailing whitespace on a line
// — which yaml.v3 would otherwise mangle into a double-quoted scalar with \n
// escapes — is preserved verbatim through migration.
func TestApply_PreservesLiteralBlockScalar(t *testing.T) {
	// Literal block scalar whose first content line has a trailing space —
	// without restoration, yaml.v3 would fall back to a double-quoted scalar
	// with \n escapes to preserve that whitespace.
	in := "id: test-flow\n" +
		"namespace: company.team\n" +
		"retry:\n" +
		"  maxAttempt: 3\n" +
		"tasks:\n" +
		"  - id: notify\n" +
		"    type: io.kestra.plugin.core.log.Log\n" +
		"    message: |\n" +
		"      Line one with trailing space \n" +
		"      Line two\n"
	out := apply(t, in)
	if !strings.Contains(out, "maxAttempts: 3") {
		t.Fatalf("expected maxAttempt rename; got:\n%s", out)
	}
	if !strings.Contains(out, "message: |\n      Line one with trailing space \n      Line two\n") {
		t.Errorf("literal block scalar was mangled; got:\n%s", out)
	}
	if strings.Contains(out, `\n`) {
		t.Errorf("output contains escaped \\n (yaml.v3 fell back to quoted); got:\n%s", out)
	}
}

// Verifies that a folded (>) block scalar containing non-ASCII characters is
// preserved verbatim — yaml.v3 would otherwise emit \U…-escaped codepoints
// inside a double-quoted scalar.
func TestApply_PreservesFoldedScalarWithUnicode(t *testing.T) {
	in := `id: test-flow
namespace: company.team
inputs:
  - id: host
    type: BOOLEAN
    defaults: false
tasks:
  - id: notify
    type: io.kestra.plugin.core.log.Log
    message: >
      Status: 🧹 cleanup complete
      for host {{ inputs.host }}
`
	out := apply(t, in)
	if !strings.Contains(out, "type: BOOL") {
		t.Fatalf("expected BOOLEAN→BOOL rename; got:\n%s", out)
	}
	if !strings.Contains(out, "🧹") {
		t.Errorf("emoji was escaped instead of preserved; got:\n%s", out)
	}
	if strings.Contains(out, `\U`) {
		t.Errorf("output contains \\U escape; got:\n%s", out)
	}
	if !strings.Contains(out, "message: >\n") {
		t.Errorf("folded scalar style was lost; got:\n%s", out)
	}
}

// Verifies that compact sequence indentation (`- id:` at the same column as
// the parent key) is preserved when the sequence's content is unchanged.
func TestApply_PreservesCompactSequenceIndent(t *testing.T) {
	in := `id: test-flow
namespace: company.team
retry:
  maxAttempt: 3
tasks:
- id: first
  type: io.kestra.plugin.core.log.Log
  message: hello
- id: second
  type: io.kestra.plugin.core.log.Log
  message: world
`
	out := apply(t, in)
	if !strings.Contains(out, "maxAttempts: 3") {
		t.Fatalf("expected maxAttempt rename; got:\n%s", out)
	}
	if !strings.Contains(out, "\ntasks:\n- id: first\n") {
		t.Errorf("compact sequence indent was lost; got:\n%s", out)
	}
}

// Under StayV1Compatible, v1 trigger `conditions:` must be left intact (v1.3
// still parses it) while v2-only `when:` / `dependsOn:` rewrites are skipped.
// Other v1→v2 renames that were aliased in v1.3 (e.g. maxAttempt → maxAttempts)
// still run.
func TestApply_StayV1Compatible_SkipsTriggerConditionRewrite(t *testing.T) {
	in := `id: test-flow
namespace: company.team
retry:
  maxAttempt: 3
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hi
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 11 * * *"
    conditions:
      - type: io.kestra.plugin.core.condition.Weekend
`
	out, _, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "when:") {
		t.Errorf("StayV1Compatible should not emit `when:`; got:\n%s", got)
	}
	if !strings.Contains(got, "conditions:") {
		t.Errorf("StayV1Compatible should preserve `conditions:`; got:\n%s", got)
	}
	if !strings.Contains(got, "maxAttempts: 3") {
		t.Errorf("non-v2-only rules must still run under StayV1Compatible; got:\n%s", got)
	}

	// Sanity: without the option, the rewrite emits `when:`.
	defaultOut := apply(t, in)
	if !strings.Contains(defaultOut, "when:") {
		t.Errorf("default Apply should emit `when:` for Weekend condition; got:\n%s", defaultOut)
	}
}

// warningsContain reports whether any warning contains substr.
func warningsContain(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// ── Rule: stripPluginDefaultsForced (v1-compatible path only) ────────────────
//
// On the v2 path the whole `pluginDefaults` block is warning-only, so these
// tests exercise Apply with StayV1Compatible, where the block is still valid.

func applyV1Compatible(t *testing.T, in string) string {
	t.Helper()
	out, _, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	return string(out)
}

func TestApply_StripPluginDefaultsForced(t *testing.T) {
	in := `id: test-flow
namespace: company.team
pluginDefaults:
  - type: io.kestra.plugin.scripts.runner.docker.Docker
    forced: true
    values:
      pullPolicy: NEVER
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hi
`
	out := applyV1Compatible(t, in)
	if strings.Contains(out, "forced") {
		t.Errorf("output still contains `forced`; got:\n%s", out)
	}
	if !strings.Contains(out, "pullPolicy: NEVER") {
		t.Errorf("output dropped the values block; got:\n%s", out)
	}
	if !strings.Contains(out, "type: io.kestra.plugin.scripts.runner.docker.Docker") {
		t.Errorf("output dropped the plugin default type; got:\n%s", out)
	}
}

// Regression: when `forced` is the FIRST key of a pluginDefaults entry it holds
// the block-sequence `- ` marker. Removing it must not drop the marker — the
// output must remain valid YAML (the entry stays a sequence item).
func TestApply_StripPluginDefaultsForced_FirstKeyKeepsSeqMarker(t *testing.T) {
	in := `id: test-flow
namespace: company.team
pluginDefaults:
  - forced: false
    type: io.kestra.plugin.docker.Run
    values:
      containerImage: alpine:latest
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hi
`
	out := applyV1Compatible(t, in)
	if strings.Contains(out, "forced") {
		t.Errorf("output still contains `forced`; got:\n%s", out)
	}
	if !strings.Contains(out, "  - type: io.kestra.plugin.docker.Run") {
		t.Errorf("sequence `- ` marker lost after removing first key; got:\n%s", out)
	}
	// Must still parse as valid YAML with pluginDefaults as a 1-item sequence.
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, out)
	}
	root := docRoot(&doc)
	pd := mappingValue(root, "pluginDefaults")
	if pd == nil || pd.Kind != yaml.SequenceNode || len(pd.Content) != 1 {
		t.Errorf("pluginDefaults is not a 1-item sequence; got:\n%s", out)
	}
}

// taskDefaults is renamed to pluginDefaults first, then forced is stripped.
func TestApply_StripPluginDefaultsForced_FromTaskDefaults(t *testing.T) {
	in := `id: test-flow
namespace: company.team
taskDefaults:
  - type: io.kestra.plugin.scripts.runner.docker.Docker
    forced: true
    values:
      pullPolicy: NEVER
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hi
`
	out := applyV1Compatible(t, in)
	if !strings.Contains(out, "pluginDefaults:") {
		t.Errorf("taskDefaults not renamed to pluginDefaults; got:\n%s", out)
	}
	if strings.Contains(out, "taskDefaults") {
		t.Errorf("output still contains `taskDefaults`; got:\n%s", out)
	}
	if strings.Contains(out, "forced") {
		t.Errorf("output still contains `forced`; got:\n%s", out)
	}
}

// ── Rule: setLocalDeleteRecursive ────────────────────────────────────────────

func TestApply_SetLocalDeleteRecursive_AddsWhenAbsent(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: cleanup
    type: io.kestra.plugin.fs.local.Delete
    from: /data/uploads/processed/
`
	out := apply(t, in)
	if !strings.Contains(out, "recursive: true") {
		t.Errorf("expected `recursive: true` to be added; got:\n%s", out)
	}
}

func TestApply_SetLocalDeleteRecursive_PreservesExplicitFalse(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: cleanup
    type: io.kestra.plugin.fs.local.Delete
    from: /data/file.txt
    recursive: false
`
	out := apply(t, in)
	if !strings.Contains(out, "recursive: false") {
		t.Errorf("explicit `recursive: false` should be preserved; got:\n%s", out)
	}
	if strings.Contains(out, "recursive: true") {
		t.Errorf("must not overwrite explicit `recursive: false`; got:\n%s", out)
	}
}

// ── Rule: renameChecksCondition ──────────────────────────────────────────────

func TestApply_RenameChecksCondition(t *testing.T) {
	in := `id: test-flow
namespace: company.team
checks:
  - condition: "{{ inputs.environment == 'production' }}"
    message: "prod only"
    behavior: BLOCK_EXECUTION
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hi
`
	out := apply(t, in)
	if !strings.Contains(out, `when: "{{ inputs.environment == 'production' }}"`) {
		t.Errorf("checks `condition` not renamed to `when`; got:\n%s", out)
	}
	if strings.Contains(out, "condition:") {
		t.Errorf("checks still contains `condition:`; got:\n%s", out)
	}
}

// The rule is scoped to top-level checks; task-level `condition` (If/Fail/etc.)
// must be left untouched.
func TestApply_RenameChecksCondition_LeavesTaskConditionUntouched(t *testing.T) {
	in := `id: test-flow
namespace: company.team
checks:
  - condition: "{{ inputs.env == 'prod' }}"
    message: x
tasks:
  - id: branch
    type: io.kestra.plugin.core.flow.If
    condition: "{{ inputs.flag }}"
    then:
      - id: hello
        type: io.kestra.plugin.core.log.Log
        message: hi
`
	out := apply(t, in)
	if !strings.Contains(out, `condition: "{{ inputs.flag }}"`) {
		t.Errorf("task-level `condition` must be preserved; got:\n%s", out)
	}
	if !strings.Contains(out, `when: "{{ inputs.env == 'prod' }}"`) {
		t.Errorf("checks `condition` not renamed to `when`; got:\n%s", out)
	}
}

// Under StayV1Compatible the checks rename is skipped (when on checks is v2-only).
func TestApply_RenameChecksCondition_StayV1Preserves(t *testing.T) {
	in := `id: test-flow
namespace: company.team
checks:
  - condition: "{{ inputs.env == 'prod' }}"
    message: x
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: hi
`
	out, _, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "condition:") {
		t.Errorf("StayV1Compatible should preserve checks `condition:`; got:\n%s", got)
	}
	if strings.Contains(got, "when:") {
		t.Errorf("StayV1Compatible should not emit `when:` on checks; got:\n%s", got)
	}
}

// ── ForEach / Loop: warning-only, no auto-transform ──────────────────────────

func TestApply_ForEach_WarnsAndDoesNotRewrite(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: each_loop
    type: io.kestra.plugin.core.flow.ForEach
    values: ["a", "b"]
    tasks:
      - id: log
        type: io.kestra.plugin.core.log.Log
        message: "{{ taskrun.value }}"
`
	out, warnings := applyWithWarnings(t, in)
	if !warningsContain(warnings, "Loop") {
		t.Errorf("expected a Loop-migration warning; got warnings: %v", warnings)
	}
	if !strings.Contains(out, "io.kestra.plugin.core.flow.ForEach") {
		t.Errorf("ForEach should be left intact (warning-only); got:\n%s", out)
	}
}

func TestApply_EachSequential_WarnsAndDoesNotRewriteToForEach(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: each_loop
    type: io.kestra.plugin.core.flow.EachSequential
    value: ["a", "b"]
    tasks:
      - id: log
        type: io.kestra.plugin.core.log.Log
        message: "{{ taskrun.value }}"
`
	out, warnings := applyWithWarnings(t, in)
	if !warningsContain(warnings, "Loop") {
		t.Errorf("expected a Loop-migration warning; got warnings: %v", warnings)
	}
	if strings.Contains(out, "io.kestra.plugin.core.flow.ForEach") {
		t.Errorf("EachSequential must no longer be rewritten to ForEach; got:\n%s", out)
	}
	if !strings.Contains(out, "io.kestra.plugin.core.flow.EachSequential") {
		t.Errorf("EachSequential should be left intact (warning-only); got:\n%s", out)
	}
}

// A Schedule trigger with no `inputs:` and a flow input that has no `defaults`
// (prefill / required:false do not count) is rejected by v2 — warn.
func TestApply_MissingTriggerInputs_NoTriggerInputs_Warns(t *testing.T) {
	in := `id: advanced-scheduling
namespace: company.team
inputs:
  - id: date
    type: DATETIME
    required: false
    prefill: 2023-12-22T14:00:00.000Z
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ inputs.date }}"
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 14 25 12 *"
`
	_, warnings := applyWithWarnings(t, in)
	if !warningsContain(warnings, "does not supply input 'date'") {
		t.Errorf("expected missing-trigger-input warning for 'date'; got: %v", warnings)
	}
}

// The v1 verbose trigger-input form `inputs: {name: <id>, value: <v>}` supplies
// keys literally named `name`/`value`, so it never provides the real input id.
func TestApply_MissingTriggerInputs_VerboseNameValueForm_Warns(t *testing.T) {
	in := `id: parametrized-flow-with-multiple-schedules
namespace: company.team
inputs:
  - id: user
    type: STRING
    prefill: Data Engineer
    required: false
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: Hello {{ inputs.user }} from Kestra!
triggers:
  - id: every_minute
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "*/1 * * * *"
    inputs:
      name: user
      value: custom value
`
	_, warnings := applyWithWarnings(t, in)
	if !warningsContain(warnings, "does not supply input 'user'") {
		t.Errorf("expected missing-trigger-input warning for 'user'; got: %v", warnings)
	}
}

// An input with a `defaults` is resolvable at scheduled runtime — no warning.
func TestApply_MissingTriggerInputs_InputHasDefaults_NoWarn(t *testing.T) {
	in := `id: test-flow
namespace: company.team
inputs:
  - id: country
    type: STRING
    defaults: US
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ inputs.country }}"
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 14 * * *"
`
	_, warnings := applyWithWarnings(t, in)
	if warningsContain(warnings, "does not supply input") {
		t.Errorf("input with defaults must not warn; got: %v", warnings)
	}
}

// A trigger that supplies the input via the v2 id→value map is satisfied.
func TestApply_MissingTriggerInputs_TriggerSuppliesInput_NoWarn(t *testing.T) {
	in := `id: test-flow
namespace: company.team
inputs:
  - id: user
    type: STRING
    required: false
tasks:
  - id: hello
    type: io.kestra.plugin.core.log.Log
    message: Hello {{ inputs.user }}
triggers:
  - id: every_minute
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "*/1 * * * *"
    inputs:
      user: custom value
`
	_, warnings := applyWithWarnings(t, in)
	if warningsContain(warnings, "does not supply input") {
		t.Errorf("trigger supplying the input must not warn; got: %v", warnings)
	}
}

// Inputs gated by a `dependsOn` are only required when their condition holds,
// so they must not be flagged (avoids false positives on conditional inputs).
func TestApply_MissingTriggerInputs_DependsOnInput_NoWarn(t *testing.T) {
	in := `id: test-flow
namespace: company.team
inputs:
  - id: make_new_release
    type: BOOL
    defaults: false
  - id: tag_name
    type: STRING
    required: true
    dependsOn:
      inputs:
        - make_new_release
      condition: "{{ inputs.make_new_release }}"
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ inputs.tag_name }}"
triggers:
  - id: weekly
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 0 * * 0"
`
	_, warnings := applyWithWarnings(t, in)
	if warningsContain(warnings, "does not supply input") {
		t.Errorf("dependsOn (conditional) inputs must not warn; got: %v", warnings)
	}
}

// The check is a v2-only validation concern — skipped under StayV1Compatible,
// since a v1-compatible flow deploys fine on v1.3 regardless.
func TestApply_MissingTriggerInputs_StayV1Compatible_NoWarn(t *testing.T) {
	in := `id: advanced-scheduling
namespace: company.team
inputs:
  - id: date
    type: DATETIME
    required: false
    prefill: 2023-12-22T14:00:00.000Z
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ inputs.date }}"
triggers:
  - id: schedule
    type: io.kestra.plugin.core.trigger.Schedule
    cron: "0 14 25 12 *"
`
	_, warnings, err := Apply([]byte(in), StayV1Compatible())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if warningsContain(warningMessages(warnings), "does not supply input") {
		t.Errorf("StayV1Compatible must not emit the v2-only trigger-input warning; got: %v", warnings)
	}
}
