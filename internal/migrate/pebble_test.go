package migrate

import (
	"strings"
	"testing"
)

func TestRewritePebbleJSON(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"filter", "{{ trigger.modifications | json }}", "{{ trigger.modifications | toJson }}"},
		{"filter no space", "{{ x|json }}", "{{ x|toJson }}"},
		{"filter chained", "{{ x | json | upper }}", "{{ x | toJson | upper }}"},
		{"function", "{{ json(taskrun.value).Email }}", "{{ fromJson(taskrun.value).Email }}"},
		{"function with space", "{{ json (x) }}", "{{ fromJson (x) }}"},
		{"function in tag", "{% for e in json(outputs.a['body']).results %}{{ e }}{% endfor %}", "{% for e in fromJson(outputs.a['body']).results %}{{ e }}{% endfor %}"},
		{"function nested", "{{ json(json(x).inner).y }}", "{{ fromJson(fromJson(x).inner).y }}"},
		{"function as argument", "{{ length(json(x)) }}", "{{ length(fromJson(x)) }}"},
		{"many expressions", "a {{ json(x).a }} b {{ y | json }} c", "a {{ fromJson(x).a }} b {{ y | toJson }} c"},
		{"whitespace control", "{{- json(x) -}}", "{{- fromJson(x) -}}"},
		{"map literal", "{{ {'a': json(x)} }}", "{{ {'a': fromJson(x)} }}"},
		{"map literal closing brace", "{{ {'a': 1}}} | {{ x | json }}", "{{ {'a': 1}}} | {{ x | toJson }}"},

		{"is test", "{% if x is json %}y{% endif %}", "{% if x is json %}y{% endif %}"},
		{"is not test", "{% if x is not json %}y{% endif %}", "{% if x is not json %}y{% endif %}"},
		{"toJson already", "{{ x | toJson }}", "{{ x | toJson }}"},
		{"fromJson already", "{{ fromJson(x) }}", "{{ fromJson(x) }}"},
		{"longer filter name", "{{ x | jsonPath('$.a') }}", "{{ x | jsonPath('$.a') }}"},
		{"member call", "{{ x.json(1) }}", "{{ x.json(1) }}"},
		{"longer function name", "{{ my_json(x) }}", "{{ my_json(x) }}"},
		{"attribute", "{{ outputs.json }}", "{{ outputs.json }}"},
		{"string literal", `{{ "x | json" ~ 'json(y)' }}`, `{{ "x | json" ~ 'json(y)' }}`},
		{"string with close delimiter", "{{ 'a}}' ~ json(x) }}", "{{ 'a}}' ~ fromJson(x) }}"},
		{"escaped quote", `{{ 'it\'s json(' ~ json(x) }}`, `{{ 'it\'s json(' ~ fromJson(x) }}`},

		{"outside delimiters", "curl -s $URL | json .id && python -c 'json(x)'", "curl -s $URL | json .id && python -c 'json(x)'"},
		{"python json module", "data = json.loads('{{ x | json }}')", "data = json.loads('{{ x | toJson }}')"},
		{"comment", "{# x | json #}{{ y | json }}", "{# x | json #}{{ y | toJson }}"},
		{"raw", "{% raw %}{{ x | json }}{% endraw %} {{ y | json }}", "{% raw %}{{ x | json }}{% endraw %} {{ y | toJson }}"},
		{"raw whitespace control", "{%- raw -%}{{ json(x) }}{%- endraw -%}", "{%- raw -%}{{ json(x) }}{%- endraw -%}"},
		{"verbatim", "{% verbatim %}{{ json(x) }}{% endverbatim %}", "{% verbatim %}{{ json(x) }}{% endverbatim %}"},
		{"unterminated raw", "{% raw %}{{ json(x) }}", "{% raw %}{{ json(x) }}"},
		{"unterminated expression", "{{ json(x) ", "{{ json(x) "},
		{"lone brace", "{ json(x) } {{ json(y) }}", "{ json(x) } {{ fromJson(y) }}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rewritePebbleJSON(c.in); got != c.want {
				t.Errorf("rewritePebbleJSON(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}

func TestApply_RenamePebbleJSON(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ json(taskrun.value).Email }} {{ trigger.modifications | json }}"
`
	want := `id: test-flow
namespace: company.team
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ fromJson(taskrun.value).Email }} {{ trigger.modifications | toJson }}"
`
	out, warnings := applyWithWarnings(t, in)
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

// The rename is behavior-identical on v1.3 (JsonFilter/JsonFunction are
// subclasses of ToJsonFilter/FromJsonFunction), so it runs on that path too.
func TestApply_RenamePebbleJSON_StayV1Compatible(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ json(x).a }}"
`
	out := applyV1Compatible(t, in)
	if !strings.Contains(out, "{{ fromJson(x).a }}") {
		t.Errorf("expected fromJson rewrite under --stay-v1-compatible, got:\n%s", out)
	}
}

func TestApply_RenamePebbleJSON_BlockScalarScript(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: script
    type: io.kestra.plugin.scripts.python.Script
    script: |
      import json
      data = json.loads('{{ outputs.fetch.body | json }}')
      print(json.dumps(data))
`
	want := `id: test-flow
namespace: company.team
tasks:
  - id: script
    type: io.kestra.plugin.scripts.python.Script
    script: |
      import json
      data = json.loads('{{ outputs.fetch.body | toJson }}')
      print(json.dumps(data))
`
	if out := apply(t, in); out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestApply_RenamePebbleJSON_NoPebbleUnchanged(t *testing.T) {
	in := `id: test-flow
namespace: company.team
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    commands:
      - curl -s https://example.com | json .id
`
	if out := apply(t, in); out != in {
		t.Errorf("expected unchanged output, got:\n%s", out)
	}
}

// kestra-ee#11387: the reproducer from the issue — both forms must be
// rewritten, so --check no longer reports the flow as v2-compatible.
func TestApply_RenamePebbleJSON_Issue11387(t *testing.T) {
	in := `id: json_function
namespace: company.team

tasks:
  - id: payload
    type: io.kestra.plugin.core.debug.Return
    format: '{"id": 42}'

  - id: parse
    type: io.kestra.plugin.core.log.Log
    message: "{{ json(outputs.payload.value).id }}"

  - id: serialize
    type: io.kestra.plugin.core.log.Log
    message: "{{ {'env': 'dev'} | json }}"
`
	want := `id: json_function
namespace: company.team

tasks:
  - id: payload
    type: io.kestra.plugin.core.debug.Return
    format: '{"id": 42}'

  - id: parse
    type: io.kestra.plugin.core.log.Log
    message: "{{ fromJson(outputs.payload.value).id }}"

  - id: serialize
    type: io.kestra.plugin.core.log.Log
    message: "{{ {'env': 'dev'} | toJson }}"
`
	if out := apply(t, in); out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
