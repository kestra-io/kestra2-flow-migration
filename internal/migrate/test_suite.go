package migrate

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FlowCorpus records, for every flow processed in the same run, the IDs of the
// tasks declared inside a ForEach / EachSequential / EachParallel. It is keyed
// by "namespace/flowId" so same-named flows in different namespaces stay
// apart. A flow with no ForEach is still present, with an empty set: that lets
// a test suite targeting it be checked precisely instead of heuristically.
type FlowCorpus map[string]map[string]bool

func corpusKey(namespace, flowID string) string {
	return namespace + "/" + flowID
}

// isForEachType reports whether typ is a flow-iteration task whose child tasks
// run once per value (removed in v2 in favor of Loop). ForEachItem is left
// out on purpose: it runs a subflow and declares no inline tasks.
func isForEachType(typ string) bool {
	return strings.HasSuffix(typ, ".ForEach") ||
		strings.HasSuffix(typ, ".EachSequential") ||
		strings.HasSuffix(typ, ".EachParallel")
}

// ExtractFlowCorpus inspects a collection of YAML file contents and builds the
// FlowCorpus for the flow definitions among them. Test suites and unparsable
// files are skipped.
func ExtractFlowCorpus(contents [][]byte) FlowCorpus {
	corpus := make(FlowCorpus)
	for _, raw := range contents {
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		root := docRoot(&doc)
		if root == nil || root.Kind != yaml.MappingNode || isTestSuite(&doc) {
			continue
		}
		flowID := stringValue(root, "id")
		if flowID == "" {
			continue
		}
		key := corpusKey(stringValue(root, "namespace"), flowID)
		if corpus[key] == nil {
			corpus[key] = make(map[string]bool)
		}
		findForEachInnerTasks(root, corpus[key])
	}
	return corpus
}

func findForEachInnerTasks(node *yaml.Node, innerTasks map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode && isForEachType(stringValue(node, "type")) {
		collectTaskIDs(mappingValue(node, "tasks"), innerTasks)
		collectTaskIDs(mappingValue(node, "errors"), innerTasks)
		return
	}
	for _, child := range node.Content {
		findForEachInnerTasks(child, innerTasks)
	}
}

func collectTaskIDs(node *yaml.Node, taskIDs map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		if id := stringValue(node, "id"); id != "" {
			taskIDs[id] = true
		}
	}
	for _, child := range node.Content {
		collectTaskIDs(child, taskIDs)
	}
}

// isTestSuite returns true if the document describes a Kestra unit test suite.
// Test suites are identified by a top-level flowId and a testCases sequence.
func isTestSuite(doc *yaml.Node) bool {
	root := docRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return false
	}
	return stringValue(root, "flowId") != "" && mappingValue(root, "testCases") != nil
}

// reOutputRef matches an outputs.<taskId> reference, optionally followed by a
// literal key access (outputs.<taskId>['x']). The task ID is captured whole,
// so `inner` never matches a reference to `inner_two`.
var reOutputRef = regexp.MustCompile(`\boutputs\.([A-Za-z0-9_-]+)(\[\s*['"][^'"]*['"]\s*\])?`)

// detectTestSuiteWarnings detects test fixtures and assertions that rely on
// ForEach semantics, which break once the flow is rewritten to Loop in v2.
//
// When the suite's flow is in the corpus, only tasks actually declared inside
// a ForEach are reported. Otherwise it falls back to heuristics (a fixture
// `value`, or a key access on a task's outputs) and the message says the
// finding depends on the task running inside a ForEach.
func detectTestSuiteWarnings(doc *yaml.Node, corpus FlowCorpus) []Warning {
	if !isTestSuite(doc) {
		return nil
	}
	root := docRoot(doc)
	testCasesNode := mappingValue(root, "testCases")
	if testCasesNode.Kind != yaml.SequenceNode {
		return nil
	}

	innerTasks, known := corpus[corpusKey(stringValue(root, "namespace"), stringValue(root, "flowId"))]

	var warnings []Warning
	seen := make(map[string]bool)
	add := func(subject, message string) {
		if seen[message] {
			return
		}
		seen[message] = true
		warnings = append(warnings, Warning{
			Message: message,
			DocURL:  docForEachLoop,
			Code:    CodeTestSuiteForEach,
			Subject: subject,
		})
	}

	for _, tcNode := range testCasesNode.Content {
		if tcNode.Kind != yaml.MappingNode {
			continue
		}
		tcID := stringValue(tcNode, "id")
		if tcID == "" {
			tcID = "(unnamed)"
		}

		// 1. Fixtures
		if fixtures := mappingValue(tcNode, "fixtures"); fixtures != nil && fixtures.Kind == yaml.MappingNode {
			if tasks := mappingValue(fixtures, "tasks"); tasks != nil && tasks.Kind == yaml.SequenceNode {
				for _, fix := range tasks.Content {
					if fix.Kind != yaml.MappingNode {
						continue
					}
					taskID := stringValue(fix, "id")
					value := ""
					if v := mappingValue(fix, "value"); v != nil {
						value = v.Value
					}
					const consequence = "fixtures on tasks inside a ForEach do not apply to Loop iterations in v2; the real task will run"
					switch {
					case known && innerTasks[taskID] && value != "":
						add(taskID, fmt.Sprintf("test case '%s' fixture targets task '%s' inside a ForEach and uses 'value' (%s): %s", tcID, taskID, value, consequence))
					case known && innerTasks[taskID]:
						add(taskID, fmt.Sprintf("test case '%s' fixture targets task '%s' inside a ForEach: %s", tcID, taskID, consequence))
					case !known && value != "":
						add(taskID, fmt.Sprintf("test case '%s' fixture on task '%s' uses 'value' (%s), so the task likely runs inside a ForEach (its flow was not part of this run): %s", tcID, taskID, value, consequence))
					}
				}
			}
		}

		// 2. Assertions
		if assertions := mappingValue(tcNode, "assertions"); assertions != nil && assertions.Kind == yaml.SequenceNode {
			for _, assertNode := range assertions.Content {
				if assertNode.Kind != yaml.MappingNode {
					continue
				}
				walkScalarStrings(assertNode, func(expr string) {
					for _, m := range reOutputRef.FindAllStringSubmatch(expr, -1) {
						ref, taskID, keyed := m[0], m[1], m[2] != ""
						switch {
						case known && innerTasks[taskID]:
							add(taskID, fmt.Sprintf("test case '%s' assertion reads output of task '%s' inside a ForEach ('%s'): in v2 Loop outputs are not exposed on outputs.<taskId> and must be accessed via loopOutputs()", tcID, taskID, ref))
						case !known && keyed:
							add(taskID, fmt.Sprintf("test case '%s' assertion reads '%s': if task '%s' runs inside a ForEach (its flow was not part of this run), v2 Loop outputs are not keyed by iteration value and must be accessed via loopOutputs()", tcID, ref, taskID))
						}
					}
				})
			}
		}
	}

	return warnings
}

func walkScalarStrings(node *yaml.Node, fn func(string)) {
	if node == nil {
		return
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		fn(node.Value)
	}
	for _, child := range node.Content {
		walkScalarStrings(child, fn)
	}
}
