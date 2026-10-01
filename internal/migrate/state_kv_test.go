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
	if len(warnings) != 1 || warnings[0].V2Incompatible || warnings[0].Code != CodeStateToKV {
		t.Fatalf("expected one advisory state-to-kv warning, got %+v", warnings)
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

// The Get-output rewrite only touches Pebble code: text outside {{ }} / {% %}
// (here a Python identifier) is not an expression and stays as is.
func TestApply_StateToKV_GetOutputsOnlyInsidePebble(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: get
    type: io.kestra.plugin.core.state.Get
    name: s
  - id: py
    type: io.kestra.plugin.scripts.python.Script
    script: |
      outputs = {"get": {"data": 1}}
      print(outputs.get.data, "{{ outputs.get.data }}")
`
	out := apply(t, in)
	if !strings.Contains(out, "print(outputs.get.data, ") {
		t.Errorf("text outside Pebble delimiters must be left alone, got:\n%s", out)
	}
	if !strings.Contains(out, `"{{ fromJson((outputs.get.value ?? '{}')`) {
		t.Errorf("the expression inside {{ }} must be rewritten, got:\n%s", out)
	}
}

// state.Set's `data` is optional on v1.3 (an empty Set just rewrote the
// current state), but kv.Set requires `value`: 2.0 rejects the result.
func TestApply_StateToKV_SetWithoutData(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: touch
    type: io.kestra.plugin.core.state.Set
    name: s
`
	_, warnings := applyWithWarningDetails(t, in)
	found := false
	for _, w := range warnings {
		if w.Code == CodeStateToKV && w.V2Incompatible && strings.Contains(w.Message, "touch") && strings.Contains(w.Message, "value") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a v2-incompatible warning for a Set without data, got %+v", warnings)
	}
}

// kv.Set has no outputs, so references to state.Set's `key` / `count` fail
// at run time on 2.0.
func TestApply_StateToKV_SetOutputsReferenced(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: set
    type: io.kestra.plugin.core.state.Set
    name: s
    data:
      a: 1
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ outputs.set.key }} / {{ outputs['set'].count }}"
`
	_, warnings := applyWithWarningDetails(t, in)
	n := 0
	for _, w := range warnings {
		if w.Code == CodeStateToKV && !w.V2Incompatible && strings.Contains(w.Message, "outputs") && strings.Contains(w.Message, "`set`") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expected one advisory about the Set's outputs, got %+v", warnings)
	}
}
