package migrate

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Constants describing the shape of a disabled flow. See
// migration-documentation/flows-changes.md "v2-incompatible flows".
const (
	// migrationLabelKey / migrationLabelValue must be a key/value pair, not a
	// single `key:value` string: v2 validates label keys against
	// `^[\p{Ll}][\p{L}0-9._-]*$` (Label.java), which excludes `:`.
	migrationLabelKey   = "v2-migration"
	migrationLabelValue = "needs-manual-rewrite"

	// disabledMarker opens the generated flow description. The description is
	// emitted as a `|` literal scalar, which yaml.v3 downgrades to a
	// double-quoted string full of `\n` escapes as soon as any line carries
	// trailing whitespace — hence trimLineEnds below.
	disabledMarker = "[kestra-migrate] NEEDS MANUAL REWRITE"

	// originalDescriptionSeparator precedes the flow's own description, which
	// is kept below the generated notice rather than replaced.
	originalDescriptionSeparator = "--- original description ---"

	// stubTaskID is the placeholder task's id. A task is required because
	// `Flow.tasks` is `@NotEmpty` — a flow whose body is entirely commented out
	// does not parse, so its disabled/labelled state would never be visible.
	stubTaskID   = "needs_manual_rewrite"
	stubTaskType = "io.kestra.plugin.core.log.Log"
)

// disableFlow rewrites a flow that Kestra 2.0 would reject into a deployable
// placeholder: only `id`, `namespace`, `disabled`, `labels`, `description` and
// a stub task remain live, and the migrated definition is appended as a
// comment block. Nothing is lost — Kestra stores flow source verbatim, so the
// commented definition survives UI edits and Git sync.
//
// Re-running the migrator over its own output is a no-op: a disabled flow has
// no live v2-incompatible construct left, so it produces no warning and never
// reaches this function.
func disableFlow(migrated []byte, warnings []Warning) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(migrated, &doc); err != nil {
		return nil, err
	}
	root := docRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cannot disable flow: expected a top-level YAML mapping")
	}

	head := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	// `id` and `namespace` are the flow's identity and must stay live. They are
	// copied rather than required: a flow missing them is already invalid, and
	// failing here would be a worse error message than the one Kestra gives.
	for _, key := range []string{"id", "namespace"} {
		if v := stringValue(root, key); v != "" {
			addStringKey(head, key, v)
		}
	}
	addBoolKey(head, "disabled", true)
	appendKey(head, "labels", labelsWithMigrationLabel(mappingValue(root, "labels")))
	appendKey(head, "description", &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Style: yaml.LiteralStyle,
		Value: disabledDescription(stringValue(root, "description"), warnings),
	})
	appendKey(head, "tasks", stubTasks())

	out, err := marshalYAML(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{head}})
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(out)
	buf.WriteString(commentedDefinition(migrated))
	return buf.Bytes(), nil
}

// disabledDescription builds the flow description: the generated notice, then
// the flow's own description below a separator.
func disabledDescription(original string, warnings []Warning) string {
	var b strings.Builder
	b.WriteString(disabledMarker + "\n\n")
	b.WriteString("This flow is not compatible with Kestra 2.0 and was disabled by\n")
	b.WriteString("kestra-migrate. Kestra 2.0 rejects it because:\n")
	for _, w := range warnings {
		if w.V2Incompatible {
			b.WriteString("  - " + strings.Join(strings.Fields(w.Message), " ") + "\n")
		}
	}
	b.WriteString("\nThe original definition is preserved as comments at the end of this file.\n")
	b.WriteString("To restore the flow: rewrite that definition, delete the `" + stubTaskID + "`\n")
	b.WriteString("task, remove `disabled: true`, and drop this notice.\n")
	if original = strings.TrimSpace(original); original != "" {
		b.WriteString("\n" + originalDescriptionSeparator + "\n" + original + "\n")
	}
	// Trailing whitespace on any line forces yaml.v3 out of the literal block
	// style, so normalize before handing the scalar over.
	return trimLineEnds(b.String())
}

// commentedDefinition renders the migrated flow as a comment block.
func commentedDefinition(migrated []byte) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("# ---------------------------------------------------------------------------\n")
	b.WriteString("# Original definition below, migrated as far as kestra-migrate could take it.\n")
	b.WriteString("# Rewrite it manually, then replace the placeholder flow above.\n")
	b.WriteString("# ---------------------------------------------------------------------------\n")
	for _, line := range strings.Split(strings.TrimRight(string(migrated), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("#\n")
			continue
		}
		b.WriteString("# " + line + "\n")
	}
	return b.String()
}

// labelsWithMigrationLabel returns the flow's labels with the migration label
// added, mirroring whichever shape the flow already uses — v2 accepts both a
// map (`labels: {k: v}`) and a list of `key`/`value` pairs
// (ListOrMapOfLabelDeserializer). Appending a mapping entry to a sequence node
// would produce labels that no longer deserialize, so the shape matters.
func labelsWithMigrationLabel(existing *yaml.Node) *yaml.Node {
	if existing != nil && existing.Kind == yaml.SequenceNode {
		for _, item := range existing.Content {
			if item.Kind == yaml.MappingNode && stringValue(item, "key") == migrationLabelKey {
				setStringValue(item, "value", migrationLabelValue)
				return existing
			}
		}
		pair := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		addStringKey(pair, "key", migrationLabelKey)
		addStringKey(pair, "value", migrationLabelValue)
		existing.Content = append(existing.Content, pair)
		return existing
	}
	if existing == nil || existing.Kind != yaml.MappingNode {
		existing = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if mappingValue(existing, migrationLabelKey) != nil {
		setStringValue(existing, migrationLabelKey, migrationLabelValue)
		return existing
	}
	addStringKey(existing, migrationLabelKey, migrationLabelValue)
	return existing
}

// stubTasks builds the single-task `tasks:` sequence of a disabled flow.
func stubTasks() *yaml.Node {
	task := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addStringKey(task, "id", stubTaskID)
	addStringKey(task, "type", stubTaskType)
	addStringKey(task, "message", "This flow was not migrated to Kestra 2.0 automatically. See the flow description.")
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{task}}
}

// appendKey appends a key/value entry to mapping m.
func appendKey(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// addStringKey appends a string key/value entry to mapping m.
func addStringKey(m *yaml.Node, key, value string) {
	appendKey(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// trimLineEnds strips trailing whitespace from every line.
func trimLineEnds(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}
