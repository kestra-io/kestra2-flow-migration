package migrate

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ionRead matches a Pebble read() call on a task output, optionally already
// wrapped in fromIon(). Group 1 is the fromIon( prefix, group 2 / 3 the task
// id in dot / bracket notation.
var ionRead = regexp.MustCompile(`(fromIon\s*\(\s*)?\bread\s*\(\s*outputs\s*(?:\.\s*([A-Za-z0-9_]+)|\[\s*['"]([^'"]+)['"]\s*\])`)

// detectIonRead flags Pebble read() calls on the output file of a task that
// writes ION, when the call is not wrapped in fromIon(). ION task outputs are
// binary on 2.0, so read() returns byte[] instead of a String and string
// operations on it (`contains`, embedding in a message, comparisons) silently
// stop working. fromIon() accepts both forms, so the fix is safe on v1.3 too,
// but it changes the expression's type (a row map, or a list of rows with
// allRows=true), so rewriting it is a judgement call: warning only.
//
// ION writers: serdes `*ToIon` tasks, `FileTransform` tasks, query tasks that
// store their result (`fetchType: STORE`, or `store: true` after
// normalizeFetchType) and core `Write` / `Concat` with an `.ion` extension.
// (flows-changes.md: ION output files are now binary)
func detectIonRead(doc *yaml.Node) []string {
	producers := map[string]string{}
	walkMappings(doc, func(m *yaml.Node) {
		if id, typ := stringValue(m, "id"), stringValue(m, "type"); id != "" && writesIon(m, typ) {
			producers[id] = typ
		}
	})
	if len(producers) == 0 {
		return nil
	}
	var warnings []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode {
			for _, m := range ionRead.FindAllStringSubmatch(n.Value, -1) {
				id := m[2] + m[3]
				typ, ok := producers[id]
				if m[1] != "" || !ok {
					continue
				}
				warnings = append(warnings, fmt.Sprintf("line %d: `read(outputs.%s…)` reads the output of `%s` (%s), an ION file that is binary on 2.0 — read() returns bytes, not text, so string operations on it no longer work; wrap it: `fromIon(read(…))` for the first row, `fromIon(read(…), allRows=true)` for all rows", n.Line, id, id, typ))
			}
			return
		}
		for i, c := range n.Content {
			if n.Kind == yaml.MappingNode && i%2 == 0 {
				continue
			}
			walk(c)
		}
	}
	walk(doc)
	return warnings
}

// writesIon reports whether task m of type typ writes its output file as ION.
func writesIon(m *yaml.Node, typ string) bool {
	switch {
	case strings.HasSuffix(typ, "ToIon"), strings.HasSuffix(typ, ".FileTransform"):
		return true
	case stringValue(m, "fetchType") == "STORE", stringValue(m, "store") == "true":
		return true
	case typ == "io.kestra.plugin.core.storage.Write", typ == "io.kestra.plugin.core.storage.Concat":
		return stringValue(m, "extension") == ".ion"
	}
	return false
}
