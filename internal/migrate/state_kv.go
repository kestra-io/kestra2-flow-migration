package migrate

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// stateTaskKinds maps every v1 State Store task type to its kind.
var stateTaskKinds = map[string]string{
	"io.kestra.plugin.core.state.Get":    "Get",
	"io.kestra.plugin.core.state.Set":    "Set",
	"io.kestra.plugin.core.state.Delete": "Delete",
	"io.kestra.core.tasks.states.Get":    "Get",
	"io.kestra.core.tasks.states.Set":    "Set",
	"io.kestra.core.tasks.states.Delete": "Delete",
}

// slugUnsafe is what io.kestra.core.utils.Slugify strips from a flow id
// (Java `[^\w-]`, ASCII \w). Flow ids are `[a-zA-Z0-9._-]`, so no whitespace
// or accents can occur and only the dots go.
var slugUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// slugify mirrors io.kestra.core.utils.Slugify.of for a flow id.
func slugify(s string) string {
	s = strings.ReplaceAll(slugUnsafe.ReplaceAllString(s, ""), "_", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.ToLower(strings.Trim(s, "-"))
}

// migrateStateToKV rewrites the properties of the State Store tasks so the
// kv.* tasks renameTypes turns them into are valid: `name` → `key`, `data` →
// `value`, and the `namespace` (scope flag) / `taskrunValue` booleans, which
// have no kv.* equivalent, are dropped. A type-only rename is rejected on save
// (`Unrecognized field "name" (class io.kestra.plugin.core.kv.Set)`).
//
// On v1.3 the State Store already lived in the namespace KV store, under
// `<slugified flow id>_states_tasks-states_<name>` (no flow prefix with
// `namespace: true`; StateStore.statePrefix), holding the state map as JSON
// bytes. The rewrite targets that key, so state written before the upgrade is
// still read after it.
//
// kv.Get returns a string where state.Get returned the map (`data`) and its
// size (`count`), `{}` / 0 when missing. A value the v1.3 State Store wrote is
// raw bytes and reads back base64-encoded on 2.0; one kv.Set wrote is plain
// JSON. Base64 never contains `{`, so references to a Get's outputs become
//
//	fromJson((v ?? '{}') contains '{' ? (v ?? '{}') : (v | base64decode))
//
// with v = outputs.<id>.value, which reads both (verified on an EE instance
// upgraded in place from v1.3.41 to v2.0.4).
//
// state.Set deep-merged `data` into the stored state; kv.Set replaces the
// value. That cannot be expressed in the flow, so each Set gets an advisory.
//
// Runs before renameTypes (it keys off the state types) and before
// renameReservedFlowIDs (the key embeds the v1 flow id). Runs under
// --stay-v1-compatible too: the kv.* properties, fromJson and `??` all exist
// on v1.3. Per-iteration isolation (`taskrunValue`, which suffixed the key
// with the ForEach value) is not reproduced; ForEach is itself flagged.
// (flows-changes.md: State Store tasks removed)
func migrateStateToKV(doc *yaml.Node) []Warning {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	flowSlug := slugify(stringValue(root, "id"))
	var getIDs, setIDs []string
	var warnings []Warning
	walkMappings(doc, func(m *yaml.Node) {
		kind, ok := stateTaskKinds[stringValue(m, "type")]
		if !ok {
			return
		}
		name := "default"
		if n := mappingValue(m, "name"); n != nil && n.Kind == yaml.ScalarNode {
			name = n.Value
		}
		prefix := flowSlug + "_"
		if ns := mappingValue(m, "namespace"); ns != nil && ns.Value == "true" {
			prefix = ""
		}
		key := prefix + "states_tasks-states_" + name
		removeKey(m, "namespace")
		removeKey(m, "taskrunValue")
		if mappingValue(m, "name") != nil {
			for i := 0; i+1 < len(m.Content); i += 2 {
				if m.Content[i].Value == "name" {
					m.Content[i].Value = "key"
					m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
				}
			}
		} else {
			insertAfterKey(m, "type", "key", key)
		}
		id := stringValue(m, "id")
		switch kind {
		case "Set":
			if id != "" {
				setIDs = append(setIDs, id)
			}
			if mappingValue(m, "data") == nil {
				// v1.3 merged nothing and rewrote the current state; kv.Set
				// requires `value`, so 2.0 rejects the task.
				warnings = append(warnings, stateWarning(true, "task `%s`: state.Set without `data` only rewrote the current state; kv.Set requires a `value` — remove the task or set one", id))
				break
			}
			renameKey(m, "data", "value")
			warnings = append(warnings, stateWarning(false, "task `%s`: state.Set merged `data` into the stored state, kv.Set replaces the value — if several Set tasks or executions each update part of the state, combine the fields yourself before setting", id))
		case "Get":
			if id != "" {
				getIDs = append(getIDs, id)
			}
		}
	})
	if len(getIDs) > 0 {
		rewriteStateGetOutputs(doc, getIDs)
	}
	for _, id := range setIDs {
		if referencesOutputs(doc, id, "key", "count") {
			warnings = append(warnings, stateWarning(false, "task `%s`: its `key` / `count` outputs are referenced, but kv.Set has no outputs — use the key literally, or a kv.Get after the Set", id))
		}
	}
	return warnings
}

func stateWarning(incompatible bool, format string, id string) Warning {
	return Warning{
		Message:        fmt.Sprintf(format, id),
		V2Incompatible: incompatible,
		DocURL:         DocMigrationGuide,
		Code:           CodeStateToKV,
		Subject:        "io.kestra.plugin.core.state.Set",
	}
}

// outputsRef matches `outputs.<id>.<field>` / `outputs['<id>'].<field>` for
// the given ids and fields. Group 1 is the id accessor, group 2 the field.
func outputsRef(ids []string, fields ...string) *regexp.Regexp {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = regexp.QuoteMeta(id)
	}
	alt := strings.Join(quoted, "|")
	return regexp.MustCompile(`outputs(\.(?:` + alt + `)|\[\s*['"](?:` + alt + `)['"]\s*\])\.(` + strings.Join(fields, "|") + `)\b`)
}

// referencesOutputs reports whether a Pebble expression in doc references
// one of task id's given output fields.
func referencesOutputs(doc *yaml.Node, id string, fields ...string) bool {
	re := outputsRef([]string{id}, fields...)
	found := false
	walkScalarValues(doc, func(n *yaml.Node) {
		if !found && strings.Contains(n.Value, "outputs") {
			rewritePebbleBodies(n.Value, func(body string) string {
				found = found || re.MatchString(body)
				return body
			})
		}
	})
	return found
}

// walkScalarValues calls fn on every scalar under n that is not a mapping key.
func walkScalarValues(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode {
		fn(n)
		return
	}
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			continue
		}
		walkScalarValues(c, fn)
	}
}

// insertAfterKey inserts `key: value` right after after, or at the end.
func insertAfterKey(m *yaml.Node, after, key, value string) {
	pair := []*yaml.Node{scalarNode(key), scalarNode(value)}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == after {
			m.Content = append(m.Content[:i+2], append(pair, m.Content[i+2:]...)...)
			return
		}
	}
	m.Content = append(m.Content, pair...)
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// rewriteStateGetOutputs rewrites `outputs.<id>.data` / `.count` (dot or
// bracket access) for every former state.Get task id, inside Pebble
// expressions and tags only.
func rewriteStateGetOutputs(doc *yaml.Node, ids []string) {
	re := outputsRef(ids, "data", "count")
	rewrite := func(body string) string {
		return re.ReplaceAllStringFunc(body, func(s string) string {
			sm := re.FindStringSubmatch(s)
			v := "outputs" + sm[1] + ".value"
			value := "fromJson((" + v + " ?? '{}') contains '{' ? (" + v + " ?? '{}') : (" + v + " | base64decode))"
			if sm[2] == "count" {
				return "(" + value + " | length)"
			}
			return value
		})
	}
	walkScalarValues(doc, func(n *yaml.Node) {
		if strings.Contains(n.Value, "outputs") {
			n.Value = rewritePebbleBodies(n.Value, rewrite)
		}
	})
}
