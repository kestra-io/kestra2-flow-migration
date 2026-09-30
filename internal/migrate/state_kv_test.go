package migrate

import (
	"strings"
	"testing"
)

// kestra-ee#11388: the reproducer from the issue. kv.Set has no `name` /
// `data`, so the type-only rename is rejected on save. The key is the one the
// v1.3 state store wrote to (StateStore.statePrefix), so state accumulated
// before the upgrade is still found.
func TestApply_StateToKV_Issue11388(t *testing.T) {
	in := `id: state_set
namespace: company.team

tasks:
  - id: set_state
    type: io.kestra.plugin.core.state.Set
    name: demo
    data:
      label: hello
`
	want := `id: state_set
namespace: company.team

tasks:
  - id: set_state
    type: io.kestra.plugin.core.kv.Set
    key: state-set_states_tasks-states_demo
    value:
      label: hello
`
	out, warnings := applyWithWarningDetails(t, in)
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if len(warnings) != 1 || warnings[0].V2Incompatible || warnings[0].Code != CodeStateMerge {
		t.Fatalf("expected one advisory state-merge warning, got %+v", warnings)
	}
	if !strings.Contains(warnings[0].Message, "set_state") || !strings.Contains(warnings[0].Message, "merge") {
		t.Errorf("warning should name the task and the lost merge semantics: %q", warnings[0].Message)
	}
}

func TestApply_StateToKV(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			// namespace: true dropped the flow prefix from the key; kv.*
			// `namespace` is a namespace name, not a scope flag, so it goes.
			name: "namespace-scoped state, taskrunValue dropped",
			in: `id: My_Flow.v2
namespace: n
tasks:
  - id: del
    type: io.kestra.plugin.core.state.Delete
    name: shared
    namespace: true
    taskrunValue: false
    errorOnMissing: true
`,
			want: `id: My_Flow.v2
namespace: n
tasks:
  - id: del
    type: io.kestra.plugin.core.kv.Delete
    key: states_tasks-states_shared
    errorOnMissing: true
`,
		},
		{
			// `name` defaulted to "default"; the flow id is slugified like
			// io.kestra.core.utils.Slugify (dots dropped, `_` → `-`, lower-case).
			name: "default name, slugified flow id, old core path",
			in: `id: My_Flow.v2
namespace: n
tasks:
  - id: get
    type: io.kestra.core.tasks.states.Get
`,
			want: `id: My_Flow.v2
namespace: n
tasks:
  - id: get
    type: io.kestra.plugin.core.kv.Get
    key: my-flowv2_states_tasks-states_default
`,
		},
		{
			// kv.Get returns the stored JSON *string* — base64-encoded for a
			// value the v1.3 State Store wrote (raw bytes), plain for one
			// kv.Set wrote; base64 never contains `{`. state.Get returned the
			// map (`data`) and its size (`count`), `{}` / 0 when missing.
			name: "Get outputs rewritten",
			in: `id: f
namespace: n
tasks:
  - id: get
    type: io.kestra.plugin.core.state.Get
    name: "{{ inputs.name }}"
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ outputs.get.data.label }} {{ outputs['get'].data }} {{ outputs.get.count > 0 }} {{ outputs.getter.data }}"
`,
			want: `id: f
namespace: n
tasks:
  - id: get
    type: io.kestra.plugin.core.kv.Get
    key: f_states_tasks-states_{{ inputs.name }}
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ fromJson((outputs.get.value ?? '{}') contains '{' ? (outputs.get.value ?? '{}') : (outputs.get.value | base64decode)).label }} {{ fromJson((outputs['get'].value ?? '{}') contains '{' ? (outputs['get'].value ?? '{}') : (outputs['get'].value | base64decode)) }} {{ (fromJson((outputs.get.value ?? '{}') contains '{' ? (outputs.get.value ?? '{}') : (outputs.get.value | base64decode)) | length) > 0 }} {{ outputs.getter.data }}"
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out := apply(t, c.in); out != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", out, c.want)
			}
		})
	}
}

// The key is derived from the v1 flow id, so it must be computed before
// renameReservedFlowIDs appends `-flow`.
func TestApply_StateToKV_KeyUsesOriginalFlowID(t *testing.T) {
	in := `id: pause
namespace: n
tasks:
  - id: get
    type: io.kestra.plugin.core.state.Get
    name: s
`
	out := apply(t, in)
	if !strings.Contains(out, "key: pause_states_tasks-states_s") {
		t.Errorf("expected the key to use the v1 flow id, got:\n%s", out)
	}
}

// kv.* tasks with key/value exist on v1.3, and fromJson / ?? too.
func TestApply_StateToKV_StayV1Compatible(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: set
    type: io.kestra.plugin.core.state.Set
    name: s
    data:
      a: 1
`
	out := applyV1Compatible(t, in)
	if !strings.Contains(out, "type: io.kestra.plugin.core.kv.Set") || !strings.Contains(out, "key: f_states_tasks-states_s") || strings.Contains(out, "name:") {
		t.Errorf("expected the kv rewrite under --stay-v1-compatible, got:\n%s", out)
	}
}
