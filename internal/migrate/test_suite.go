package migrate

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FlowCorpus maps flowId to a set of task IDs that are defined inside a ForEach
// (ForEach, ForEachItem, EachSequential, EachParallel) in that flow.
type FlowCorpus map[string]map[string]bool

var forEachTypes = map[string]bool{
	"io.kestra.plugin.core.flow.ForEach":        true,
	"io.kestra.plugin.core.flow.ForEachItem":    true,
	"io.kestra.plugin.core.flow.EachSequential": true,
	"io.kestra.plugin.core.flow.EachParallel":   true,
	"io.kestra.core.tasks.flows.EachSequential": true,
	"io.kestra.core.tasks.flows.EachParallel":   true,
	"io.kestra.core.tasks.flows.ForEachItem":    true,
}

func isForEachType(typ string) bool {
	if forEachTypes[typ] {
		return true
	}
	return strings.HasSuffix(typ, ".ForEach") ||
		strings.HasSuffix(typ, ".ForEachItem") ||
		strings.HasSuffix(typ, ".EachSequential") ||
		strings.HasSuffix(typ, ".EachParallel")
}

// ExtractFlowCorpus inspects a collection of YAML file contents and builds a
// mapping from flowId to the set of task IDs declared inside any ForEach task.
func ExtractFlowCorpus(contents [][]byte) FlowCorpus {
	corpus := make(FlowCorpus)
	for _, raw := range contents {
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		root := docRoot(&doc)
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		// If it has flowId and testCases, it is a test suite, not a flow definition.
		if stringValue(root, "flowId") != "" && mappingValue(root, "testCases") != nil {
			continue
		}
		flowID := stringValue(root, "id")
		if flowID == "" {
			continue
		}

		innerTasks := make(map[string]bool)
		findForEachInnerTasks(root, innerTasks)
		if len(innerTasks) > 0 {
			if corpus[flowID] == nil {
				corpus[flowID] = make(map[string]bool)
			}
			for tid := range innerTasks {
				corpus[flowID][tid] = true
			}
		}
	}
	return corpus
}

func findForEachInnerTasks(node *yaml.Node, innerTasks map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		typ := stringValue(node, "type")
		if isForEachType(typ) {
			collectTaskIDs(mappingValue(node, "tasks"), innerTasks)
			collectTaskIDs(mappingValue(node, "errors"), innerTasks)
			return
		}
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

var reKeyedOutput = regexp.MustCompile(`outputs\.([a-zA-Z0-9_-]+)\[['"]([^'"]+)['"]\]`)

// detectTestSuiteWarnings detects test fixtures and assertions that rely on
// ForEach semantics which broke with the rewrite to Loop in v2.
func detectTestSuiteWarnings(doc *yaml.Node, corpus FlowCorpus) []Warning {
	root := docRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	flowID := stringValue(root, "flowId")
	testCasesNode := mappingValue(root, "testCases")
	if flowID == "" || testCasesNode == nil || testCasesNode.Kind != yaml.SequenceNode {
		return nil
	}

	var innerTasks map[string]bool
	if corpus != nil {
		innerTasks = corpus[flowID]
	}

	var warnings []Warning
	seen := make(map[string]bool)

	addWarning := func(w Warning) {
		if !seen[w.Message] {
			seen[w.Message] = true
			warnings = append(warnings, w)
		}
	}

	for _, tcNode := range testCasesNode.Content {
		if tcNode.Kind != yaml.MappingNode {
			continue
		}
		tcID := stringValue(tcNode, "id")
		if tcID == "" {
			tcID = "(unnamed)"
		}

		// 1. Check fixtures
		fixturesNode := mappingValue(tcNode, "fixtures")
		if fixturesNode != nil && fixturesNode.Kind == yaml.MappingNode {
			tasksNode := mappingValue(fixturesNode, "tasks")
			if tasksNode != nil && tasksNode.Kind == yaml.SequenceNode {
				for _, fixNode := range tasksNode.Content {
					if fixNode.Kind != yaml.MappingNode {
						continue
					}
					taskID := stringValue(fixNode, "id")
					valNode := mappingValue(fixNode, "value")
					hasValue := valNode != nil && valNode.Value != ""
					isInner := innerTasks != nil && innerTasks[taskID]

					if hasValue {
						addWarning(Warning{
							Message: fmt.Sprintf("test case '%s' fixture on task '%s' uses 'value' (%s): fixtures on tasks inside a ForEach do not apply to Loop iterations in v2; the real task will run", tcID, taskID, valNode.Value),
							DocURL:  docForEachLoop,
							Code:    CodeTestSuiteForEach,
							Subject: taskID,
						})
					} else if isInner {
						addWarning(Warning{
							Message: fmt.Sprintf("test case '%s' fixture targets task '%s' inside a ForEach: fixtures on tasks inside a ForEach do not apply to Loop iterations in v2; the real task will run", tcID, taskID),
							DocURL:  docForEachLoop,
							Code:    CodeTestSuiteForEach,
							Subject: taskID,
						})
					}
				}
			}
		}

		// 2. Check assertions
		assertionsNode := mappingValue(tcNode, "assertions")
		if assertionsNode != nil && assertionsNode.Kind == yaml.SequenceNode {
			for _, assertNode := range assertionsNode.Content {
				if assertNode.Kind != yaml.MappingNode {
					continue
				}
				var exprs []string
				walkScalarStrings(assertNode, func(s string) {
					exprs = append(exprs, s)
				})

				for _, expr := range exprs {
					matches := reKeyedOutput.FindAllStringSubmatch(expr, -1)
					keyedTasks := make(map[string]bool)
					for _, m := range matches {
						taskName := m[1]
						keyedTasks[taskName] = true
						addWarning(Warning{
							Message: fmt.Sprintf("test case '%s' assertion reads '%s': in v2 Loop outputs are not keyed by iteration value and must be accessed via loopOutputs()", tcID, m[0]),
							DocURL:  docForEachLoop,
							Code:    CodeTestSuiteForEach,
							Subject: taskName,
						})
					}

					if innerTasks != nil {
						for t := range innerTasks {
							if keyedTasks[t] {
								continue
							}
							if strings.Contains(expr, "outputs."+t) {
								addWarning(Warning{
									Message: fmt.Sprintf("test case '%s' assertion reads output of task '%s' inside a ForEach: in v2 Loop outputs are not exposed on outputs.<taskId> and must be accessed via loopOutputs()", tcID, t),
									DocURL:  docForEachLoop,
									Code:    CodeTestSuiteForEach,
									Subject: t,
								})
							}
						}
					}
				}
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
