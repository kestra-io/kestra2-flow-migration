package migrate

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// incompatibleFlow uses a removed type (EachSequential) and so carries a
// v2-incompatible warning.
const incompatibleFlow = `id: nightly-rollup
namespace: company.team
description: |
  Runs the nightly rollup.
labels:
  owner: data-team

tasks:
  - id: each
    type: io.kestra.plugin.core.flow.EachSequential
    value: ["a", "b"]
    tasks:
      - id: log
        type: io.kestra.plugin.core.log.Log
        message: "{{ taskrun.value }}"

triggers:
  - id: daily
    type: io.kestra.plugin.core.trigger.Schedule
    cron: 0 9 * * *
`

// disabledFlow is the parsed shape of a disabled flow — enough of it to assert
// that Kestra would accept the result.
type disabledFlow struct {
	ID          string            `yaml:"id"`
	Namespace   string            `yaml:"namespace"`
	Disabled    bool              `yaml:"disabled"`
	Labels      map[string]string `yaml:"labels"`
	Description string            `yaml:"description"`
	Tasks       []struct {
		ID           string `yaml:"id"`
		Type         string `yaml:"type"`
		ErrorMessage string `yaml:"errorMessage"`
	} `yaml:"tasks"`
	Triggers []any `yaml:"triggers"`
	Inputs   []any `yaml:"inputs"`
}

func parseDisabled(t *testing.T, out string) disabledFlow {
	t.Helper()
	var f disabledFlow
	if err := yaml.Unmarshal([]byte(out), &f); err != nil {
		t.Fatalf("disabled flow is not valid YAML: %v\n%s", err, out)
	}
	return f
}

func TestDisableV2IncompatibleRewritesFlow(t *testing.T) {
	out, warnings := applyWithWarningDetails(t, incompatibleFlow, DisableV2Incompatible())
	if !HasV2Incompatible(warnings) {
		t.Fatalf("expected a v2-incompatible warning, got: %v", warnings)
	}
	f := parseDisabled(t, out)

	if f.ID != "nightly-rollup" || f.Namespace != "company.team" {
		t.Errorf("identity must be preserved, got id=%q namespace=%q", f.ID, f.Namespace)
	}
	if !f.Disabled {
		t.Error("flow must be disabled")
	}
	if got := f.Labels[migrationLabelKey]; got != migrationLabelValue {
		t.Errorf("missing migration label, got labels: %v", f.Labels)
	}
	if f.Labels["owner"] != "data-team" {
		t.Errorf("existing labels must be preserved, got: %v", f.Labels)
	}
	// The body must be commented out, not left live: a removed type would make
	// the flow unparseable on 2.0.
	if len(f.Tasks) != 1 || f.Tasks[0].ID != stubTaskID || f.Tasks[0].Type != stubTaskType {
		t.Errorf("expected the single placeholder task, got: %+v", f.Tasks)
	}
	// A Fail, so that re-enabling the flow before rewriting it ends in FAILED
	// rather than a green execution that did nothing.
	if f.Tasks[0].ErrorMessage != stubTaskMessage {
		t.Errorf("the placeholder Fail task must explain itself, got: %q", f.Tasks[0].ErrorMessage)
	}
	if f.Triggers != nil || f.Inputs != nil {
		t.Errorf("triggers and inputs must be commented out, got triggers=%v inputs=%v", f.Triggers, f.Inputs)
	}
	if strings.Contains(out, "\nEachSequential") || !strings.Contains(out, "#     type: io.kestra.plugin.core.flow.EachSequential") {
		t.Errorf("the original definition must survive as comments, got:\n%s", out)
	}
}

func TestDisableV2IncompatibleDescription(t *testing.T) {
	out, _ := applyWithWarningDetails(t, incompatibleFlow, DisableV2Incompatible())
	f := parseDisabled(t, out)

	if !strings.HasPrefix(f.Description, disabledMarker) {
		t.Errorf("description must open with the marker, got:\n%s", f.Description)
	}
	if !strings.Contains(f.Description, "EachSequential") {
		t.Errorf("description must state why the flow was disabled, got:\n%s", f.Description)
	}
	if !strings.Contains(f.Description, originalDescriptionSeparator) ||
		!strings.Contains(f.Description, "Runs the nightly rollup.") {
		t.Errorf("the flow's own description must be kept, got:\n%s", f.Description)
	}
	// A literal block scalar, not a `\n`-escaped double-quoted string.
	if !strings.Contains(out, "description: |") {
		t.Errorf("description should stay a literal block scalar, got:\n%s", out)
	}
}

func TestDisableV2IncompatibleWithoutDescription(t *testing.T) {
	in := `id: f
namespace: ns
tasks:
  - id: count
    type: io.kestra.plugin.core.execution.Count
`
	out, _ := applyWithWarningDetails(t, in, DisableV2Incompatible())
	f := parseDisabled(t, out)
	if strings.Contains(f.Description, originalDescriptionSeparator) {
		t.Errorf("no separator expected when the flow had no description, got:\n%s", f.Description)
	}
}

func TestDisableV2IncompatibleAddsLabelsWhenAbsent(t *testing.T) {
	in := `id: f
namespace: ns
tasks:
  - id: count
    type: io.kestra.plugin.core.execution.Count
`
	out, _ := applyWithWarningDetails(t, in, DisableV2Incompatible())
	f := parseDisabled(t, out)
	if f.Labels[migrationLabelKey] != migrationLabelValue {
		t.Errorf("expected a labels block to be created, got: %v", f.Labels)
	}
}

func TestDisableV2IncompatibleKeepsListLabelShape(t *testing.T) {
	in := `id: f
namespace: ns
labels:
  - key: owner
    value: data-team
tasks:
  - id: count
    type: io.kestra.plugin.core.execution.Count
`
	out, _ := applyWithWarningDetails(t, in, DisableV2Incompatible())

	var f struct {
		Labels []struct {
			Key   string `yaml:"key"`
			Value string `yaml:"value"`
		} `yaml:"labels"`
	}
	if err := yaml.Unmarshal([]byte(out), &f); err != nil {
		t.Fatalf("labels must stay a list of key/value pairs: %v\n%s", err, out)
	}
	if len(f.Labels) != 2 || f.Labels[0].Key != "owner" ||
		f.Labels[1].Key != migrationLabelKey || f.Labels[1].Value != migrationLabelValue {
		t.Errorf("expected the migration label appended in list shape, got: %+v", f.Labels)
	}
}

func TestDisableV2IncompatibleOverwritesExistingMigrationLabel(t *testing.T) {
	in := `id: f
namespace: ns
labels:
  v2-migration: done
tasks:
  - id: count
    type: io.kestra.plugin.core.execution.Count
`
	out, _ := applyWithWarningDetails(t, in, DisableV2Incompatible())
	f := parseDisabled(t, out)
	if f.Labels[migrationLabelKey] != migrationLabelValue {
		t.Errorf("existing migration label must be overwritten, got: %v", f.Labels)
	}
	if strings.Count(out[:strings.Index(out, "description:")], migrationLabelKey) != 1 {
		t.Errorf("the migration label must not be duplicated, got:\n%s", out)
	}
}

func TestDisableV2IncompatibleLeavesAdvisoryOnlyFlows(t *testing.T) {
	// `version=` in a read() call is advisory: 2.0 accepts the flow and breaks
	// at run time, so the flow must not be disabled.
	in := `id: f
namespace: ns
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read('flow.py', version=3) }}"
`
	out, warnings := applyWithWarningDetails(t, in, DisableV2Incompatible())
	if len(warnings) == 0 {
		t.Fatal("expected the advisory version= warning")
	}
	if HasV2Incompatible(warnings) {
		t.Fatalf("version= must be advisory, got: %v", warnings)
	}
	if strings.Contains(out, "disabled: true") {
		t.Errorf("advisory-only flows must not be disabled, got:\n%s", out)
	}
}

func TestDisableV2IncompatibleIsOptIn(t *testing.T) {
	out, warnings := applyWithWarningDetails(t, incompatibleFlow)
	if !HasV2Incompatible(warnings) {
		t.Fatal("expected a v2-incompatible warning")
	}
	if strings.Contains(out, "disabled: true") {
		t.Errorf("flows must only be disabled when the option is set, got:\n%s", out)
	}
}

func TestDisableV2IncompatibleIsIdempotent(t *testing.T) {
	once, _ := applyWithWarningDetails(t, incompatibleFlow, DisableV2Incompatible())
	twice, warnings := applyWithWarningDetails(t, once, DisableV2Incompatible())
	if len(warnings) != 0 {
		t.Errorf("a disabled flow has no live incompatible construct left, got: %v", warnings)
	}
	if twice != once {
		t.Errorf("re-running over a disabled flow must be a no-op, got:\n%s", twice)
	}
}
