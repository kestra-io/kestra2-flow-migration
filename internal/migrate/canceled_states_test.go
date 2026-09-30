package migrate

import (
	"strings"
	"testing"
)

// kestra-ee#11383: the reproducer from the issue. State.Type has no single-L
// CANCELED on v1.3 or v2 (only Exit.ExitState had that alias), so the purge
// fails at run time with `Unrecognized token 'CANCELED'` on both.
func TestApply_CanceledStates_Issue11383(t *testing.T) {
	in := `id: canceled_state
namespace: company.team

tasks:
  - id: purge
    type: io.kestra.plugin.core.execution.PurgeExecutions
    states: [CANCELED]
    endDate: "{{ now() }}"
`
	want := `id: canceled_state
namespace: company.team

tasks:
  - id: purge
    type: io.kestra.plugin.core.execution.PurgeExecutions
    states: [CANCELLED]
    endDate: "{{ now() }}"
`
	if out := apply(t, in); out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestApply_CanceledStates(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: purge
    type: io.kestra.plugin.core.execution.PurgeExecutions
    states:
      - SUCCESS
      - CANCELED
triggers:
  - id: upstream
    type: io.kestra.plugin.core.trigger.Flow
    states: [FAILED, CANCELED]
    dependsOn:
      - flowId: a
        namespace: n
        states: [CANCELED]
`
	want := `id: f
namespace: n
tasks:
  - id: purge
    type: io.kestra.plugin.core.execution.PurgeExecutions
    states:
      - SUCCESS
      - CANCELLED
triggers:
  - id: upstream
    type: io.kestra.plugin.core.trigger.Flow
    states: [FAILED, CANCELLED]
    dependsOn:
      - flowId: a
        namespace: n
        states: [CANCELLED]
`
	if out := apply(t, in); out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// A v1 ExecutionStatus condition carries its states under in / notIn; they
// feed the dependsOn `states` the trigger rewrite builds.
func TestApply_CanceledStates_ExecutionStatusCondition(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: hi
triggers:
  - id: upstream
    type: io.kestra.plugin.core.trigger.Flow
    conditions:
      - type: io.kestra.plugin.core.condition.ExecutionStatus
        in:
          - CANCELED
`
	out := apply(t, in)
	if strings.Contains(out, "- CANCELED") || strings.Contains(out, "[CANCELED]") || !strings.Contains(out, "CANCELLED") {
		t.Errorf("expected CANCELED → CANCELLED in the converted trigger, got:\n%s", out)
	}
}

// Only state positions are touched: a CANCELED value elsewhere (a label, an
// input default, script text) is data and stays.
func TestApply_CanceledStates_OtherValuesUntouched(t *testing.T) {
	in := `id: f
namespace: n
labels:
  status: CANCELED
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: CANCELED
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    commands:
      - echo CANCELED
`
	if out := apply(t, in); out != in {
		t.Errorf("expected unchanged output, got:\n%s", out)
	}
}

// CANCELLED is the only spelling State.Type ever had, so the rewrite is valid
// on v1.3 and runs under --stay-v1-compatible too.
func TestApply_CanceledStates_StayV1Compatible(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: purge
    type: io.kestra.plugin.core.execution.PurgeExecutions
    states: [CANCELED]
`
	if out := applyV1Compatible(t, in); !strings.Contains(out, "states: [CANCELLED]") {
		t.Errorf("expected the rewrite under --stay-v1-compatible, got:\n%s", out)
	}
}
