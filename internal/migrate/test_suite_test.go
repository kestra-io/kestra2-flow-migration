package migrate

import (
	"strings"
	"testing"
)

func TestTestSuite_FixtureUsesValue_WarnsWithoutCorpus(t *testing.T) {
	suiteYAML := `
id: s_foreach
namespace: qa.migr
flowId: foreach_flow
testCases:
  - id: mock_one_iteration
    type: io.kestra.core.tests.flow.UnitTest
    fixtures:
      tasks:
        - id: inner
          value: x
          outputs:
            value: mocked-x
    assertions:
      - value: "{{ outputs.last.value }}"
        equalTo: "mocked-x|inner-y"
`
	migrated, warnings, err := Apply([]byte(suiteYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(migrated) != suiteYAML {
		t.Errorf("test suite should not be mutated, expected unchanged YAML")
	}

	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}

	w := warnings[0]
	if w.Code != CodeTestSuiteForEach {
		t.Errorf("expected code %s, got %s", CodeTestSuiteForEach, w.Code)
	}
	if w.V2Incompatible {
		t.Errorf("expected advisory warning (V2Incompatible=false), got true")
	}
	if w.DocURL != docForEachLoop {
		t.Errorf("expected doc URL %s, got %s", docForEachLoop, w.DocURL)
	}
	if !strings.Contains(w.Message, "uses 'value' (x)") {
		t.Errorf("expected message to mention 'value' (x), got: %s", w.Message)
	}
}

func TestTestSuite_FixtureTargetsForEachTask_WarnsWithCorpus(t *testing.T) {
	flowYAML := `
id: foreach_flow
namespace: qa.migr
tasks:
  - id: each
    type: io.kestra.plugin.core.flow.ForEach
    values: ["x", "y"]
    tasks:
      - id: inner
        type: io.kestra.plugin.core.debug.Return
        format: "inner-{{ taskrun.value }}"
`
	suiteYAML := `
id: s_foreach
namespace: qa.migr
flowId: foreach_flow
testCases:
  - id: mock_iteration
    type: io.kestra.core.tests.flow.UnitTest
    fixtures:
      tasks:
        - id: inner
          outputs:
            value: mocked-x
`
	corpus := ExtractFlowCorpus([][]byte{[]byte(flowYAML), []byte(suiteYAML)})
	if !corpus["foreach_flow"]["inner"] {
		t.Fatalf("expected inner to be recorded in corpus for foreach_flow")
	}

	_, warnings, err := Apply([]byte(suiteYAML), WithCorpus(corpus))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}

	if !strings.Contains(warnings[0].Message, "targets task 'inner' inside a ForEach") {
		t.Errorf("expected message to mention targeting task inside a ForEach, got: %s", warnings[0].Message)
	}
}

func TestTestSuite_AssertionKeyedOutput_WarnsWithoutCorpus(t *testing.T) {
	suiteYAML := `
id: s_foreach
namespace: qa.migr
flowId: foreach_flow
testCases:
  - id: check_output
    type: io.kestra.core.tests.flow.UnitTest
    assertions:
      - value: "{{ outputs.inner['x'].value }}"
        equalTo: "inner-x"
`
	_, warnings, err := Apply([]byte(suiteYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}

	w := warnings[0]
	if w.Code != CodeTestSuiteForEach {
		t.Errorf("expected code %s, got %s", CodeTestSuiteForEach, w.Code)
	}
	if !strings.Contains(w.Message, "outputs.inner['x']") {
		t.Errorf("expected message to mention outputs.inner['x'], got: %s", w.Message)
	}
}

func TestTestSuite_AssertionForEachOutput_WarnsWithCorpus(t *testing.T) {
	flowYAML := `
id: foreach_flow
namespace: qa.migr
tasks:
  - id: each
    type: io.kestra.plugin.core.flow.ForEach
    values: ["x", "y"]
    tasks:
      - id: inner
        type: io.kestra.plugin.core.debug.Return
`
	suiteYAML := `
id: s_foreach
namespace: qa.migr
flowId: foreach_flow
testCases:
  - id: check_output
    type: io.kestra.core.tests.flow.UnitTest
    assertions:
      - value: "{{ outputs.inner.value }}"
        equalTo: "test"
`
	corpus := ExtractFlowCorpus([][]byte{[]byte(flowYAML)})
	_, warnings, err := Apply([]byte(suiteYAML), WithCorpus(corpus))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}

	if !strings.Contains(warnings[0].Message, "reads output of task 'inner' inside a ForEach") {
		t.Errorf("expected message to mention reading output of inner inside ForEach, got: %s", warnings[0].Message)
	}
}

func TestTestSuite_NormalFlowAndTestSuite_NoWarnings(t *testing.T) {
	flowYAML := `
id: normal_flow
namespace: qa.migr
tasks:
  - id: regular_task
    type: io.kestra.plugin.core.log.Log
    message: "hello"
`
	suiteYAML := `
id: s_normal
namespace: qa.migr
flowId: normal_flow
testCases:
  - id: test_normal
    type: io.kestra.core.tests.flow.UnitTest
    fixtures:
      tasks:
        - id: regular_task
          outputs:
            value: mocked
    assertions:
      - value: "{{ outputs.regular_task.value }}"
        equalTo: "mocked"
`
	corpus := ExtractFlowCorpus([][]byte{[]byte(flowYAML)})
	_, warnings, err := Apply([]byte(suiteYAML), WithCorpus(corpus))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for normal test suite, got: %v", warnings)
	}
}

func TestTestSuite_ReproductionIssue55(t *testing.T) {
	flowYAML := `id: foreach_flow
namespace: qa.migr
tasks:
  - id: each
    type: io.kestra.plugin.core.flow.ForEach
    values: ["x", "y"]
    tasks:
      - id: inner
        type: io.kestra.plugin.core.debug.Return
        format: "inner-{{ taskrun.value }}"
  - id: last
    type: io.kestra.plugin.core.debug.Return
    format: "{{ outputs.inner['x'].value }}|{{ outputs.inner['y'].value }}"
`
	suiteYAML := `id: s_foreach
namespace: qa.migr
flowId: foreach_flow
testCases:
  - id: mock_one_iteration
    type: io.kestra.core.tests.flow.UnitTest
    fixtures:
      tasks:
        - id: inner
          value: x
          outputs: {value: mocked-x}
    assertions:
      - value: "{{ outputs.last.value }}"
        equalTo: "mocked-x|inner-y"
`
	corpus := ExtractFlowCorpus([][]byte{[]byte(flowYAML), []byte(suiteYAML)})

	// 1. Flow gets the ForEach warning
	_, flowWarnings, err := Apply([]byte(flowYAML), WithCorpus(corpus))
	if err != nil {
		t.Fatalf("unexpected error on flow: %v", err)
	}
	hasForEach := false
	for _, w := range flowWarnings {
		if w.Code == CodeForEachLoop {
			hasForEach = true
			break
		}
	}
	if !hasForEach {
		t.Errorf("expected flow to have CodeForEachLoop warning")
	}

	// 2. Test suite gets the test suite warning
	migratedSuite, suiteWarnings, err := Apply([]byte(suiteYAML), WithCorpus(corpus))
	if err != nil {
		t.Fatalf("unexpected error on suite: %v", err)
	}
	if string(migratedSuite) != suiteYAML {
		t.Errorf("test suite should not be modified")
	}
	if len(suiteWarnings) != 1 {
		t.Fatalf("expected 1 suite warning, got %d: %v", len(suiteWarnings), suiteWarnings)
	}
	if suiteWarnings[0].Code != CodeTestSuiteForEach {
		t.Errorf("expected CodeTestSuiteForEach, got %s", suiteWarnings[0].Code)
	}
	if !strings.Contains(suiteWarnings[0].Message, "mock_one_iteration") || !strings.Contains(suiteWarnings[0].Message, "inner") {
		t.Errorf("expected warning to mention mock_one_iteration and inner, got: %s", suiteWarnings[0].Message)
	}
}
