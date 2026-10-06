package migrate

import "gopkg.in/yaml.v3"

// migrateEEGitClone rewrites io.kestra.plugin.ee.git.Clone to the OSS
// io.kestra.plugin.git.Clone it duplicated. The EE copy was declared
// `@Plugin(internal = true)` and deleted in plugin-ee-git 2.2.1
// (plugin-ee-git#172, kestra-ee#7117), so EE 2.0 rejects it as an unknown
// type. Every property it had exists on git.Clone under the same name
// (`auth` becomes optional, `depth` defaults to 1 on both), with one
// differing default: the EE copy verified SSH host keys
// (`strictHostKeyChecking` defaulted to true) while git.Clone does not, so
// `strictHostKeyChecking: true` is pinned when absent — the same idea as
// pinning `fallback: WAIT` on converted worker groups.
//
// Gated post-step in Apply, skipped under --stay-v1-compatible: the EE type
// still exists on v1.3 (plugin-ee-git 1.7.2), whose git.Clone
// (plugin-git 2.0.12) has no `strictHostKeyChecking` property.
// (flows-changes.md: io.kestra.plugin.ee.git.Clone removed)
func migrateEEGitClone(doc *yaml.Node) {
	walkMappings(doc, func(m *yaml.Node) {
		if stringValue(m, "type") != "io.kestra.plugin.ee.git.Clone" {
			return
		}
		setStringValue(m, "type", "io.kestra.plugin.git.Clone")
		if mappingValue(m, "strictHostKeyChecking") != nil {
			return
		}
		pair := []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "strictHostKeyChecking"},
			{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
		}
		at := len(m.Content)
		if i := keyIndex(m, "url"); i >= 0 {
			at = i + 2
		}
		m.Content = append(m.Content[:at], append(pair, m.Content[at:]...)...)
	})
}
