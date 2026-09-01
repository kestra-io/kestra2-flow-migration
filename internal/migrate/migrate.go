package migrate

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// rule is a function that transforms a parsed YAML document in-place.
type rule func(*yaml.Node) error

var rules = []rule{
	renameInputNameToID,
	renameInputTypes,
	renameMaxAttemptToMaxAttempts,
	renamePauseDelayToPauseDuration,
	normalizeFetchType,
	migrateDbtBuildToDbtCLI,
	renameTypes,
	removeDeprecatedProperties,
	renameExitCanceled,
	renameMultiselectOptions,
	migrateHTTPBasicAuth,
	removeDeprecatedHTTPOptions,
	setLocalDeleteRecursive,
	removeRequiredFalseWithDefaults,
	renameReservedFlowIDs,
}

// Option configures Apply.
type Option func(*applyOptions)

type applyOptions struct {
	stayV1Compatible      bool
	disableV2Incompatible bool
}

// StayV1Compatible skips migration rules whose output is not valid on a v1.3
// Kestra instance. See migration-documentation/flows-changes.md
// "v2-only compatible changes" for the list of gated rules.
func StayV1Compatible() Option {
	return func(o *applyOptions) { o.stayV1Compatible = true }
}

// DisableV2Incompatible rewrites flows carrying at least one v2-incompatible
// warning into a deployable placeholder: the original definition is commented
// out, `disabled: true` is set, and the flow is labelled
// `v2-migration: needs-manual-rewrite`. See
// migration-documentation/flows-changes.md "v2-incompatible flows".
func DisableV2Incompatible() Option {
	return func(o *applyOptions) { o.disableV2Incompatible = true }
}

// Warning describes a construct the migrator could not rewrite automatically.
type Warning struct {
	Message string

	// V2Incompatible reports whether Kestra 2.0 rejects the flow outright
	// because of this warning (unknown type or property, or a FlowValidator
	// violation) as opposed to accepting the flow and breaking at run time.
	V2Incompatible bool
}

func (w Warning) String() string { return w.Message }

// v2Incompatible tags detector output as "2.0 refuses to save this flow".
func v2Incompatible(messages []string) []Warning {
	return warningsOf(messages, true)
}

// advisory tags detector output as "2.0 saves this flow, but it misbehaves".
func advisory(messages []string) []Warning {
	return warningsOf(messages, false)
}

func warningsOf(messages []string, incompatible bool) []Warning {
	out := make([]Warning, 0, len(messages))
	for _, m := range messages {
		out = append(out, Warning{Message: m, V2Incompatible: incompatible})
	}
	return out
}

// HasV2Incompatible reports whether any warning blocks deployment to 2.0.
func HasV2Incompatible(warnings []Warning) bool {
	for _, w := range warnings {
		if w.V2Incompatible {
			return true
		}
	}
	return false
}

// Apply applies v1→v2 migration rules to a flow's raw YAML content.
// The YAML is round-tripped via yaml.v3 nodes to preserve comments.
// Returns: migrated content, validation warnings (for constructs needing
// manual rewrite), and any processing error.
func Apply(content []byte, opts ...Option) ([]byte, []Warning, error) {
	var o applyOptions
	for _, fn := range opts {
		fn(&o)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, nil, err
	}

	// Snapshot before rules so we can detect whether anything changed.
	before, err := marshalYAML(&doc)
	if err != nil {
		return nil, nil, err
	}

	for _, r := range rules {
		if err := r(&doc); err != nil {
			return nil, nil, err
		}
	}

	// Rewrite v1 trigger conditions → v2 `when:` / `dependsOn:` first. Mutates
	// doc. Returns warnings for shapes the rewriter cannot safely handle.
	// Runs before detectRemovedTypes so successfully-rewritten types (e.g.
	// MultipleCondition consumed into a dependsOn fan-out) don't produce a
	// stale removed-type warning.
	// Skipped under StayV1Compatible — the rewrite emits v2-only `when:` /
	// `dependsOn:` constructs. Unrewritten `MultipleCondition` etc. will still
	// surface via detectRemovedTypes so the user knows manual work is pending.
	var warnings []Warning
	if !o.stayV1Compatible {
		warnings = v2Incompatible(rewriteTriggerConditions(&doc))
		// `when` on flow-level `checks` is a v2-only construct (v1.3 uses
		// `condition`), so this rename is gated alongside the trigger rewrite.
		renameChecksCondition(&doc)
		// `behavior` exists on v2 and v1.3.28 but not earlier 1.3.x patches;
		// under --stay-v1-compatible the deprecated `expiredOnly` is left in
		// place (it still parses on v2).
		migratePurgeKVExpiredOnly(&doc)
		// `workerSelector` does not exist on v1.3 (EE worker routing).
		warnings = append(warnings, v2Incompatible(migrateWorkerGroupToWorkerSelector(&doc))...)
		// v2-only validation: Schedule triggers must supply every input lacking
		// a `defaults`. Warning-only (values can't be invented); a v1-compatible
		// flow is unaffected, so this is gated to the v2 path.
		warnings = append(warnings, v2Incompatible(detectMissingTriggerInputs(&doc))...)
		// read()/fileURI() `version=` → `revision=` is a v2 hard break the tool
		// cannot rewrite safely (expressions may be embedded in script bodies).
		warnings = append(warnings, advisory(detectPebbleVersionArg(&doc))...)
		// Tasks calling the Kestra API need credentials on v2; advisory because
		// they may already be configured at namespace/tenant or server level.
		warnings = append(warnings, advisory(detectSdkAuth(&doc))...)
		// `pluginDefaults` / `taskDefaults` are removed outright in v2 with no
		// mechanical replacement — warning-only, like the flow-iteration types.
		warnings = append(warnings, v2Incompatible(detectPluginDefaults(&doc))...)
	} else {
		// v1.3 still accepts `pluginDefaults`, so under --stay-v1-compatible the
		// pre-v2 normalization is kept: rename the deprecated `taskDefaults`
		// alias and drop `forced` (both outputs remain valid on v1.3). On the v2
		// path these are skipped in favor of the warning above — renaming into a
		// keyword that no longer exists would only produce a flow that fails to
		// parse under a different key.
		if err := renameTaskDefaults(&doc); err != nil {
			return nil, nil, err
		}
		if err := stripPluginDefaultsForced(&doc); err != nil {
			return nil, nil, err
		}
	}
	// Detect removed types after all rename/rewrite rules have run.
	warnings = append(warnings, v2Incompatible(detectRemovedTypes(&doc))...)

	after, err := marshalYAML(&doc)
	if err != nil {
		return nil, nil, err
	}

	// No rules modified the document — return original bytes to preserve formatting.
	if bytes.Equal(before, after) {
		return finish(content, warnings, o)
	}
	// Replace byte ranges for subtrees that are semantically unchanged with the
	// original bytes. This preserves block-scalar styles (|, >), non-ASCII
	// characters, and compact sequence indentation that yaml.v3 remaps.
	after = restoreUnchangedBlocks(content, after)
	// yaml.v3 strips blank lines on round-trip. Re-insert them wherever the
	// surrounding context lines still match the original.
	after = restoreBlankLines(content, after)
	return finish(after, warnings, o)
}

// finish applies the post-migration output transforms that depend on the
// warnings the run produced, and returns Apply's result triple.
func finish(migrated []byte, warnings []Warning, o applyOptions) ([]byte, []Warning, error) {
	if !o.disableV2Incompatible || !HasV2Incompatible(warnings) {
		return migrated, warnings, nil
	}
	disabled, err := disableFlow(migrated, warnings)
	if err != nil {
		return nil, warnings, err
	}
	return disabled, warnings, nil
}

// restoreBlankLines re-inserts blank lines from original into migrated at
// positions where the surrounding non-blank context lines still match.
// Blank lines whose anchors were rewritten or removed by migration are
// silently dropped — that's the correct behavior, since the section they
// anchored no longer exists.
func restoreBlankLines(original, migrated []byte) []byte {
	origLines := strings.Split(string(original), "\n")
	migLines := strings.Split(string(migrated), "\n")

	// Each run of blank lines in the original is characterized by the
	// following non-blank line (the "anchor") and the count of blanks. We
	// intentionally don't match against the preceding line — migration often
	// rewrites it (e.g. BOOLEAN → BOOL), which would otherwise drop the blank.
	type insert struct {
		anchor string
		count  int
	}
	var inserts []insert

	i := 0
	for i < len(origLines) {
		if strings.TrimSpace(origLines[i]) != "" {
			i++
			continue
		}
		start := i
		for i < len(origLines) && strings.TrimSpace(origLines[i]) == "" {
			i++
		}
		if i >= len(origLines) {
			break // trailing blanks — leave trailing-newline handling to yaml.v3
		}
		inserts = append(inserts, insert{
			anchor: origLines[i],
			count:  i - start,
		})
	}

	if len(inserts) == 0 {
		return migrated
	}

	// Assign each insert (in original order) to the earliest migrated line
	// index at or after `cursor` whose text equals the insert's anchor. Inserts
	// whose anchor never appears are silently dropped (their anchor was
	// rewritten or removed by migration). This position-ordered assignment
	// avoids a duplicate anchor (e.g. `for item in roadmap:` appearing in two
	// tasks' script blocks) from consuming an insert meant for the later
	// occurrence.
	anchorPos := make(map[string][]int)
	for idx, line := range migLines {
		anchorPos[line] = append(anchorPos[line], idx)
	}

	type assignment struct {
		migIdx int
		count  int
	}
	assignments := make([]assignment, 0, len(inserts))
	cursor := 0
	for _, ins := range inserts {
		positions := anchorPos[ins.anchor]
		pos := -1
		for _, p := range positions {
			if p >= cursor {
				pos = p
				break
			}
		}
		if pos < 0 {
			continue
		}
		assignments = append(assignments, assignment{migIdx: pos, count: ins.count})
		cursor = pos + 1
	}

	if len(assignments) == 0 {
		return migrated
	}

	result := make([]string, 0, len(migLines)+len(inserts))
	aIdx := 0
	for idx, line := range migLines {
		if aIdx < len(assignments) && assignments[aIdx].migIdx == idx {
			existing := 0
			for p := len(result) - 1; p >= 0 && strings.TrimSpace(result[p]) == ""; p-- {
				existing++
			}
			for m := existing; m < assignments[aIdx].count; m++ {
				result = append(result, "")
			}
			aIdx++
		}
		result = append(result, line)
	}

	return []byte(strings.Join(result, "\n"))
}

// marshalYAML encodes a yaml.Node using 2-space indent (Kestra convention).
func marshalYAML(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ── Rules ────────────────────────────────────────────────────────────────────

// renameInputNameToID renames the `name:` field to `id:` on each item in the
// top-level `inputs:` sequence. (flows-changes.md: Inputs `name` removed)
func renameInputNameToID(doc *yaml.Node) error {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	inputs := mappingValue(root, "inputs")
	if inputs == nil || inputs.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range inputs.Content {
		if item.Kind == yaml.MappingNode {
			renameKey(item, "name", "id")
		}
	}
	return nil
}

// renameInputTypes renames deprecated input type values:
// BOOLEAN → BOOL, ENUM → SELECT. (flows-changes.md: Input type BOOLEAN/ENUM removed)
func renameInputTypes(doc *yaml.Node) error {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	inputs := mappingValue(root, "inputs")
	if inputs == nil || inputs.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range inputs.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		switch stringValue(item, "type") {
		case "BOOLEAN":
			setStringValue(item, "type", "BOOL")
		case "ENUM":
			setStringValue(item, "type", "SELECT")
		}
	}
	return nil
}

// renameMaxAttemptToMaxAttempts renames `maxAttempt` → `maxAttempts` everywhere
// in the document. (flows-changes.md: Retry property renamed)
func renameMaxAttemptToMaxAttempts(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		renameKey(m, "maxAttempt", "maxAttempts")
	})
	return nil
}

// renamePauseDelayToPauseDuration renames `delay` → `pauseDuration` on task
// mappings whose `type` is `io.kestra.plugin.core.flow.Pause`.
// (flows-changes.md: Pause.delay removed)
func renamePauseDelayToPauseDuration(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		if stringValue(m, "type") == "io.kestra.plugin.core.flow.Pause" {
			renameKey(m, "delay", "pauseDuration")
		}
	})
	return nil
}

var fetchTypePlugins = map[string]bool{
	"io.kestra.plugin.datagen.core.Generate":       true,
	"io.kestra.plugin.googleworkspace.sheets.Read": true,
}

// normalizeFetchType converts `fetchType: STORE` → `store: true` and
// `fetchType: FETCH` → `fetch: true` for the specific plugin types listed in
// fetchTypePlugins.
func normalizeFetchType(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		if !fetchTypePlugins[stringValue(m, "type")] {
			return
		}
		val := stringValue(m, "fetchType")
		switch val {
		case "STORE":
			removeKey(m, "fetchType")
			addBoolKey(m, "store", true)
		case "FETCH":
			removeKey(m, "fetchType")
			addBoolKey(m, "fetch", true)
		}
	})
	return nil
}

// renameTaskDefaults renames the top-level `taskDefaults` key to `pluginDefaults`.
// (flows-changes.md: Task defaults removed)
func renameTaskDefaults(doc *yaml.Node) error {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	renameKey(root, "taskDefaults", "pluginDefaults")
	return nil
}

// stripPluginDefaultsForced removes the `forced` property from each flow-level
// `pluginDefaults` entry. `forced: true` is removed in v2 and causes a hard
// parse failure; enforcement of non-overridable defaults moves to the namespace
// or global configuration level. Runs after renameTaskDefaults so the key is
// already named `pluginDefaults`. `forced` is a direct sibling of `type:` on
// each entry, not nested under `values:`.
func stripPluginDefaultsForced(doc *yaml.Node) error {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	pluginDefaults := mappingValue(root, "pluginDefaults")
	if pluginDefaults == nil || pluginDefaults.Kind != yaml.SequenceNode {
		return nil
	}
	for _, entry := range pluginDefaults.Content {
		if entry.Kind == yaml.MappingNode {
			removeKey(entry, "forced")
		}
	}
	return nil
}

// setLocalDeleteRecursive preserves v1 behavior for io.kestra.plugin.fs.local.Delete,
// whose `recursive` property now defaults to `false` in v2 (was `true`). When
// `recursive` is not set explicitly, add `recursive: true`. This is safe in all
// cases: on directory targets it preserves the old recursive deletion, and on
// file targets `recursive` has no effect.
func setLocalDeleteRecursive(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		if stringValue(m, "type") != "io.kestra.plugin.fs.local.Delete" {
			return
		}
		if mappingValue(m, "recursive") == nil {
			addBoolKey(m, "recursive", true)
		}
	})
	return nil
}

// renameChecksCondition renames `condition` → `when` inside the top-level
// `checks:` list only. v2 unifies all conditional expressions under `when`; the
// `condition` alias still parses in 2.0 but is the legacy form. This is scoped
// strictly to the root `checks:` sequence — it must NOT touch task-level
// `condition` (on If/Fail/LoopUntil/Switch), so it does not use walkMappings.
// Called as a gated post-step in Apply (skipped under StayV1Compatible) because
// `when` on `checks` is not recognized by v1.3.
func renameChecksCondition(doc *yaml.Node) {
	root := docRoot(doc)
	if root == nil {
		return
	}
	checks := mappingValue(root, "checks")
	if checks == nil || checks.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range checks.Content {
		if item.Kind == yaml.MappingNode {
			renameKey(item, "condition", "when")
		}
	}
}

// typeRenames maps deprecated type strings to their v2 replacements.
var typeRenames = map[string]string{
	// ── Old io.kestra.core.* paths ──────────────────────────────────────────
	// Flows
	"io.kestra.core.tasks.flows.Template":         "io.kestra.plugin.core.flow.Subflow",
	"io.kestra.core.tasks.flows.Pause":            "io.kestra.plugin.core.flow.Pause",
	"io.kestra.core.tasks.flows.Subflow":          "io.kestra.plugin.core.flow.Subflow",
	"io.kestra.core.tasks.flows.Flow":             "io.kestra.plugin.core.flow.Subflow",
	"io.kestra.core.tasks.flows.Switch":           "io.kestra.plugin.core.flow.Switch",
	"io.kestra.core.tasks.flows.If":               "io.kestra.plugin.core.flow.If",
	"io.kestra.core.tasks.flows.Parallel":         "io.kestra.plugin.core.flow.Parallel",
	"io.kestra.core.tasks.flows.Sequential":       "io.kestra.plugin.core.flow.Sequential",
	"io.kestra.core.tasks.flows.WorkingDirectory": "io.kestra.plugin.core.flow.WorkingDirectory",
	"io.kestra.core.tasks.flows.Dag":              "io.kestra.plugin.core.flow.Dag",
	// Debug / Log
	"io.kestra.core.tasks.debugs.Echo":   "io.kestra.plugin.core.log.Log",
	"io.kestra.core.tasks.debugs.Return": "io.kestra.plugin.core.debug.Return",
	// States → KV
	"io.kestra.core.tasks.states.Get":    "io.kestra.plugin.core.kv.Get",
	"io.kestra.core.tasks.states.Set":    "io.kestra.plugin.core.kv.Set",
	"io.kestra.core.tasks.states.Delete": "io.kestra.plugin.core.kv.Delete",
	// Storage
	// NOTE: PurgeExecutions lives in the `execution` subpackage — v1.3.28 carries
	// the aliases on io.kestra.plugin.core.execution.PurgeExecutions, and
	// io.kestra.plugin.core.storage.PurgeExecutions never existed.
	"io.kestra.core.tasks.storages.Purge":          "io.kestra.plugin.core.execution.PurgeExecutions",
	"io.kestra.core.tasks.storages.LocalFiles":     "io.kestra.plugin.core.flow.WorkingDirectory",
	"io.kestra.core.tasks.storages.PurgeExecution": "io.kestra.plugin.core.storage.PurgeCurrentExecutionFiles",
	// Execution
	"io.kestra.core.tasks.executions.Fail": "io.kestra.plugin.core.execution.Fail",
	// Triggers
	"io.kestra.core.models.triggers.types.Schedule": "io.kestra.plugin.core.trigger.Schedule",
	"io.kestra.core.models.triggers.types.Flow":     "io.kestra.plugin.core.trigger.Flow",
	"io.kestra.core.models.triggers.types.Webhook":  "io.kestra.plugin.core.trigger.Webhook",
	// NOTE: old-path condition types (io.kestra.core.models.conditions.types.*)
	// are intentionally not in this map. The entire trigger-conditions subsystem
	// was redesigned in v2 (conditions → when / dependsOn). Detection of those
	// old paths is handled by detectRemovedTriggerConditions instead.

	// ── New io.kestra.plugin.core.* but still deprecated ────────────────────
	// Tasks
	"io.kestra.plugin.core.flow.Template": "io.kestra.plugin.core.flow.Subflow",
	"io.kestra.plugin.core.debug.Echo":    "io.kestra.plugin.core.log.Log",
	// State → KV
	"io.kestra.plugin.core.state.Get":    "io.kestra.plugin.core.kv.Get",
	"io.kestra.plugin.core.state.Set":    "io.kestra.plugin.core.kv.Set",
	"io.kestra.plugin.core.state.Delete": "io.kestra.plugin.core.kv.Delete",
	// Storage aliases (PurgeExecutions is in the `execution` subpackage, see above)
	"io.kestra.plugin.core.storage.Purge":          "io.kestra.plugin.core.execution.PurgeExecutions",
	"io.kestra.plugin.core.storage.PurgeExecution": "io.kestra.plugin.core.storage.PurgeCurrentExecutionFiles",
	// LocalFiles
	"io.kestra.plugin.core.storage.LocalFiles": "io.kestra.plugin.core.flow.WorkingDirectory",
	// Log Fetch → kestra.logs
	"io.kestra.plugin.core.log.Fetch": "io.kestra.plugin.kestra.logs.Fetch",

	// ── Third-party plugin renames ─────────────────────────────────────────
	// Notifications → per-service plugins
	"io.kestra.plugin.notifications.slack.SlackIncomingWebhook": "io.kestra.plugin.slack.notifications.SlackIncomingWebhook",
	"io.kestra.plugin.notifications.slack.SlackExecution":       "io.kestra.plugin.slack.notifications.SlackExecution",
	"io.kestra.plugin.notifications.mail.MailSend":              "io.kestra.plugin.email.MailSend",
	"io.kestra.plugin.notifications.discord.DiscordExecution":   "io.kestra.plugin.discord.DiscordExecution",
	// Slack plugin internal restructure
	"io.kestra.plugin.slack.SlackIncomingWebhook": "io.kestra.plugin.slack.notifications.SlackIncomingWebhook",
	"io.kestra.plugin.slack.SlackExecution":       "io.kestra.plugin.slack.notifications.SlackExecution",
	// Plugin core subpackage additions
	"io.kestra.plugin.kubernetes.PodCreate": "io.kestra.plugin.kubernetes.core.PodCreate",
	"io.kestra.plugin.datagen.Generate":     "io.kestra.plugin.datagen.core.Generate",
	// AstraDB → Cassandra
	"io.kestra.plugin.astradb.Query": "io.kestra.plugin.cassandra.astradb.Query",
	// fs.http → core HTTP
	"io.kestra.plugin.fs.http.Request":  "io.kestra.plugin.core.http.Request",
	"io.kestra.plugin.fs.http.Download": "io.kestra.plugin.core.http.Download",
}

// templateTypes is the set of old Template type names for which we also
// rename templateId → flowId.
var templateTypes = map[string]bool{
	"io.kestra.plugin.core.flow.Template": true,
	"io.kestra.core.tasks.flows.Template": true,
}

// renameTypes applies all type renames from the typeRenames map.
// For Template → Subflow it also renames templateId → flowId.
// (flows-changes.md: multiple rules)
func renameTypes(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		typ := stringValue(m, "type")
		if templateTypes[typ] {
			renameKey(m, "templateId", "flowId")
		}
		if newType, ok := typeRenames[typ]; ok {
			setStringValue(m, "type", newType)
		}
	})
	return nil
}

// migrateDbtBuildToDbtCLI renames the deprecated io.kestra.plugin.dbt.cli.Build
// task to io.kestra.plugin.dbt.cli.DbtCLI and reshapes properties that do not
// exist on DbtCLI:
//   - Adds `commands: [dbt build]` when not already set (Build ran this implicitly).
//   - Drops `dbtPath` (DbtCLI expects dbt on PATH inside the container image).
//   - Promotes `dockerOptions.image` → `containerImage` and drops `dockerOptions`
//     (not valid on DbtCLI).
//
// (flows-changes.md: dbt plugin Build task deprecated)
func migrateDbtBuildToDbtCLI(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		if stringValue(m, "type") != "io.kestra.plugin.dbt.cli.Build" {
			return
		}
		setStringValue(m, "type", "io.kestra.plugin.dbt.cli.DbtCLI")

		removeKey(m, "dbtPath")

		if dockerOpts := mappingValue(m, "dockerOptions"); dockerOpts != nil && dockerOpts.Kind == yaml.MappingNode {
			image := stringValue(dockerOpts, "image")
			if image != "" && mappingValue(m, "containerImage") == nil {
				m.Content = append(m.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "containerImage", Tag: "!!str"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: image, Tag: "!!str"},
				)
			}
			removeKey(m, "dockerOptions")
		}

		if mappingValue(m, "commands") != nil {
			return
		}
		commands := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		commands.Content = append(commands.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "dbt build", Tag: "!!str"},
		)
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "commands", Tag: "!!str"},
			commands,
		)
	})
	return nil
}

// removeDeprecatedProperties removes properties that no longer exist in v2:
//   - outputs on Subflow tasks
//   - backfills on Schedule triggers
//   - minLogLevel on any trigger
//
// PurgeKV.expiredOnly is NOT removed here — it still parses on v2 (deprecated)
// and blind removal is lossy for `expiredOnly: false`; it is converted to
// `behavior` by migratePurgeKVExpiredOnly on the v2 path instead.
// (flows-changes.md: multiple rules)
func removeDeprecatedProperties(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		switch stringValue(m, "type") {
		case "io.kestra.plugin.core.flow.Subflow":
			removeKey(m, "outputs")
		case "io.kestra.plugin.core.trigger.Schedule":
			removeKey(m, "backfills")
			removeKey(m, "backfill")
		}
	})
	// Remove minLogLevel from top-level triggers
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	triggers := mappingValue(root, "triggers")
	if triggers == nil || triggers.Kind != yaml.SequenceNode {
		return nil
	}
	for _, t := range triggers.Content {
		if t.Kind == yaml.MappingNode {
			removeKey(t, "minLogLevel")
		}
	}
	return nil
}

// renameExitCanceled renames Exit task state value `CANCELED` → `CANCELLED`.
// The v2 ExitState enum is SUCCESS, WARNING, KILLED, FAILED, CANCELLED; the
// single-L spelling was a deprecated alias for the same CANCELLED state and was
// dropped on develop. Mapping to KILLED would change semantics — KILLED sends
// an out-of-band kill event that stops sibling running tasks.
// (flows-changes.md: Exit.ExitState.CANCELED removed)
func renameExitCanceled(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		if stringValue(m, "type") != "io.kestra.plugin.core.execution.Exit" {
			return
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "state" && m.Content[i+1].Value == "CANCELED" {
				m.Content[i+1].Value = "CANCELLED"
			}
		}
	})
	return nil
}

// migratePurgeKVExpiredOnly converts the deprecated `expiredOnly` property on
// PurgeKV tasks to the v2 `behavior` object:
//
//	expiredOnly: <x>  →  behavior: {type: key, expiredOnly: <x>}
//
// Simply deleting the property would be lossy: v2's `behavior` defaults to
// expired-only purging, so a v1 `expiredOnly: false` (purge everything) would
// silently flip semantics. When an explicit `behavior` is already present the
// task is left untouched — v2 still parses the deprecated property (it
// overrides `behavior`), and reconciling the two is a judgement call.
// Called as a gated post-step in Apply (skipped under StayV1Compatible)
// because `behavior` only exists on v1.3.28+.
// (flows-changes.md: PurgeKV.expiredOnly deprecated → behavior)
func migratePurgeKVExpiredOnly(doc *yaml.Node) {
	walkMappings(doc, func(m *yaml.Node) {
		if stringValue(m, "type") != "io.kestra.plugin.core.kv.PurgeKV" {
			return
		}
		expired := mappingValue(m, "expiredOnly")
		if expired == nil || mappingValue(m, "behavior") != nil {
			return
		}
		behavior := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		behavior.Content = append(behavior.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "type", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "key", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "expiredOnly", Tag: "!!str"},
			expired, // reuse the original value node (preserves bool/expression)
		)
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "expiredOnly" {
				m.Content[i].Value = "behavior"
				m.Content[i+1] = behavior
				return
			}
		}
	})
}

// rfc1123Label matches a valid RFC 1123 label: lowercase alphanumerics and
// hyphens, must start and end with an alphanumeric, max 63 chars.
var rfc1123Label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// migrateWorkerGroupToWorkerSelector converts the removed EE `workerGroup`
// property on tasks and triggers to the v2 `workerSelector`:
//
//	workerGroup: {key: <k>, fallback: <f>}
//	→ workerSelector: {tags: [<k>], fallback: <f or WAIT>}
//
// The fallback default flipped between versions (v1 waited when no worker was
// available; v2 fails), so WAIT is pinned when v1 omitted it. Keys that are
// templated or not valid RFC 1123 labels (v2 tags must be) cannot be mapped
// mechanically and produce a validation warning instead. A workerGroup with a
// fallback but no key is also warning-only: v2 rejects fallback without tags.
// Called as a gated post-step in Apply (skipped under StayV1Compatible)
// because workerSelector does not exist on v1.3.
// (flows-changes.md EE: workerGroup → workerSelector)
func migrateWorkerGroupToWorkerSelector(doc *yaml.Node) []string {
	var warnings []string
	walkMappings(doc, func(m *yaml.Node) {
		wg := mappingValue(m, "workerGroup")
		if wg == nil || wg.Kind != yaml.MappingNode {
			return
		}
		id := stringValue(m, "id")
		if id == "" {
			id = "(unknown)"
		}
		key := stringValue(wg, "key")
		fallback := stringValue(wg, "fallback")
		if key == "" {
			warnings = append(warnings, fmt.Sprintf("%s has a workerGroup without a key; v2's workerSelector rejects fallback without tags — rewrite manually (workerGroup is removed in v2)", id))
			return
		}
		if strings.Contains(key, "{{") {
			warnings = append(warnings, fmt.Sprintf("%s has a templated workerGroup.key (%s); it cannot be mapped to workerSelector.tags mechanically — rewrite manually (workerGroup is removed in v2)", id, key))
			return
		}
		if !rfc1123Label.MatchString(key) {
			warnings = append(warnings, fmt.Sprintf("%s has workerGroup.key %q, which is not an RFC 1123 label (lowercase alphanumerics and hyphens only); v2 workerSelector tags must comply — rename the worker group and rewrite manually", id, key))
			return
		}
		tags := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		tags.Content = append(tags.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		)
		if fallback == "" {
			// v1 waited by default; v2 defaults to FAIL — pin WAIT to preserve behavior.
			fallback = "WAIT"
		}
		sel := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		sel.Content = append(sel.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "tags", Tag: "!!str"},
			tags,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "fallback", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fallback, Tag: "!!str"},
		)
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "workerGroup" {
				m.Content[i].Value = "workerSelector"
				m.Content[i+1] = sel
				return
			}
		}
	})
	return warnings
}

// pebbleVersionArg matches read()/fileURI() Pebble calls using the removed
// `version=` named argument. Heuristic: does not match across nested closing
// parens, which is fine for a warning-only detector.
var pebbleVersionArg = regexp.MustCompile(`\b(?:read|fileURI)\s*\([^)]*\bversion\s*=`)

// detectPebbleVersionArg flags Pebble read()/fileURI() calls that use the
// `version=` named argument, renamed to `revision` in v2 with no alias or
// fallback (kestra PR #16699, first shipped in v2.0.0-rc3). Rewriting inside
// arbitrary expressions risks corrupting embedded script code, so this is
// warning-only.
// (flows-changes.md: Pebble read()/fileURI() named argument version removed)
func detectPebbleVersionArg(doc *yaml.Node) []string {
	var warnings []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode && pebbleVersionArg.MatchString(n.Value) {
			warnings = append(warnings, fmt.Sprintf("line %d: Pebble read()/fileURI() uses the `version=` named argument, renamed to `revision` in v2 with no fallback — update the expression manually", n.Line))
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(doc)
	return warnings
}

// sdkAuthTypes are the exact task types that call the Kestra API through the
// SDK on every run and therefore require credentials in v2. Every
// `io.kestra.plugin.kestra.*` task is covered by the prefix check in
// detectSdkAuth instead. `io.kestra.plugin.git.Push` also calls the API but is
// already reported as a removed type, so it is left out to avoid a double
// warning.
//
// Verified against plugin-git and plugin-ee-git `main` (2026-08-28): each of
// these reaches
// `AbstractCloningTask.kestraClient()` unconditionally in `run()`.
// `io.kestra.plugin.git.SyncNamespaceFiles` is deliberately absent — it moves
// files through `runContext.storage()`, which the worker can reach without the
// API; see sdkAuthConditional.
var sdkAuthTypes = map[string]bool{
	"io.kestra.plugin.git.SyncFlows":      true,
	"io.kestra.plugin.git.SyncFlow":       true,
	"io.kestra.plugin.git.Sync":           true,
	"io.kestra.plugin.git.SyncDashboards": true,
	"io.kestra.plugin.git.PushFlows":      true,
	"io.kestra.plugin.git.PushDashboards": true,
	"io.kestra.plugin.git.NamespaceSync":  true,
	"io.kestra.plugin.git.TenantSync":     true,
	"io.kestra.plugin.ai.KestraFlow":      true,

	// plugin-ee-git. These live under `io.kestra.plugin.ee.git.*` and inherit
	// the same `auth` property (via the EE copy of
	// `io.kestra.plugin.git.AbstractKestraTask`), so the suppression below
	// works on them unchanged. There is no alias between plugin-git and
	// plugin-ee-git — neither repo has ever carried a `@Plugin(aliases = ...)`
	// for these — so the EE types have to be listed explicitly.
	// `io.kestra.plugin.ee.git.Clone` is deliberately absent: it makes no API
	// call. Note `io.kestra.plugin.git.NamespaceSync` / `TenantSync` above are
	// shipped by *both* plugin-git and plugin-ee-git under the identical FQN,
	// so the OSS entries already cover the EE build.
	"io.kestra.plugin.ee.git.SyncApps":       true,
	"io.kestra.plugin.ee.git.SyncBlueprints": true,
	"io.kestra.plugin.ee.git.SyncUnitTests":  true,
	"io.kestra.plugin.ee.git.PushApps":       true,
	"io.kestra.plugin.ee.git.PushBlueprints": true,
	"io.kestra.plugin.ee.git.PushUnitTests":  true,
}

// sdkAuthConditional maps a task type to the property that, when true, is what
// drives it to the Kestra API. `SyncNamespaceFiles` reads and writes namespace
// files via internal storage; only `includeChildNamespaces: true` sends it
// through `descendantNamespaces()` → `client.namespaces().searchNamespaces()`.
// The property defaults to false, so an unset value is not flagged.
var sdkAuthConditional = map[string]string{
	"io.kestra.plugin.git.SyncNamespaceFiles": "includeChildNamespaces",
}

const sdkAuthPrefix = "io.kestra.plugin.kestra."

// detectSdkAuth flags tasks that call the Kestra API internally and carry no
// inline `auth:` block. These calls were unauthenticated on v1.3 and fail with
// 401 on v2 unless credentials are supplied — inline, or via namespace/tenant
// defaults (EE) or the server config, neither of which is visible from the flow
// file. Hence advisory, not v2-incompatible: the flow still deploys.
//
// Note `auth` on a git task is the *Kestra API* credential block
// (`AbstractCloningTask.auth`, "Kestra API authentication"); Git remote
// credentials are the separate `username` / `password` / `privateKey`
// properties on `AbstractGitTask`, so keying off `auth:` does not
// mis-suppress a flow that only authenticates against Git.
// (flows-changes.md: Tasks calling the Kestra API now require SDK authentication)
func detectSdkAuth(doc *yaml.Node) []string {
	var warnings []string
	walkMappings(docRoot(doc), func(m *yaml.Node) {
		t := stringValue(m, "type")
		if t == "" {
			return
		}
		needsAuth := sdkAuthTypes[t] || strings.HasPrefix(t, sdkAuthPrefix)
		if prop, ok := sdkAuthConditional[t]; ok {
			// A templated value can't be resolved statically; flag it, since the
			// task reaches the API whenever it renders true.
			v := stringValue(m, prop)
			needsAuth = v == "true" || strings.Contains(v, "{{")
		}
		if !needsAuth {
			return
		}
		if mappingValue(m, "auth") != nil {
			return
		}
		warnings = append(warnings, fmt.Sprintf("line %d: `%s` calls the Kestra API and requires SDK authentication in v2 — add an `auth:` block, or configure credentials at namespace/tenant or server level", m.Line, t))
	})
	return warnings
}

// renameMultiselectOptions renames `options` → `values` on MULTISELECT inputs.
// (flows-changes.md: MultiselectInput.options removed)
func renameMultiselectOptions(doc *yaml.Node) error {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	inputs := mappingValue(root, "inputs")
	if inputs == nil || inputs.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range inputs.Content {
		if item.Kind == yaml.MappingNode && stringValue(item, "type") == "MULTISELECT" {
			renameKey(item, "options", "values")
		}
	}
	return nil
}

// removeRequiredFalseWithDefaults removes `required: false` from inputs that
// have a `defaults` value. In v2, inputs with defaults must be required (the
// default), since the default is always applied.
// (flows-changes.md: Inputs with defaults must be required)
func removeRequiredFalseWithDefaults(doc *yaml.Node) error {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	inputs := mappingValue(root, "inputs")
	if inputs == nil || inputs.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range inputs.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if mappingValue(item, "defaults") != nil && stringValue(item, "required") == "false" {
			removeKey(item, "required")
		}
	}
	return nil
}

// reservedFlowIDs is the set of flow IDs that are reserved keywords in v2.
var reservedFlowIDs = map[string]bool{
	"pause":         true,
	"resume":        true,
	"force-run":     true,
	"change-status": true,
	"kill":          true,
	"executions":    true,
	"search":        true,
	"source":        true,
	"disable":       true,
	"enable":        true,
}

// renameReservedFlowIDs appends `-flow` to flow IDs that clash with v2
// reserved keywords.
// (flows-changes.md: Reserved flow IDs disallowed)
func renameReservedFlowIDs(doc *yaml.Node) error {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	id := stringValue(root, "id")
	if reservedFlowIDs[id] {
		setStringValue(root, "id", id+"-flow")
	}
	return nil
}

// ── Removed type detection ──────────────────────────────────────────────────

// removedTypes maps type strings that were removed in v2 with no automated
// replacement to a human-readable reason. Flows using these must be rewritten.
var removedTypes = map[string]string{
	"io.kestra.plugin.core.condition.MultipleCondition": "removed; rewrite as `dependsOn` entries on the Flow trigger (one per upstream flow) and move `window` to the trigger-level `window:` block",
	"io.kestra.plugin.core.execution.Count":             "removed; use KV Store or custom logic",
	"io.kestra.plugin.core.execution.Resume":            "removed; use the SDK to manipulate execution states",
	"io.kestra.plugin.core.trigger.Toggle":              "removed; use the API or SDK to enable/disable triggers",
	"io.kestra.plugin.git.Push":                         "deprecated; use io.kestra.plugin.git.SyncFlows or Git API tasks",
	"io.kestra.plugin.scripts.nashorn.Eval":             "removed; migrate to GraalJS or other script tasks",
	"io.kestra.plugin.scripts.nashorn.FileTransform":    "removed; migrate to GraalJS or other script tasks",
	// ForEach / ForEachItem / EachSequential / EachParallel are removed in v2 and
	// replaced by io.kestra.plugin.core.flow.Loop. The rewrite is non-trivial
	// (expression renames, declared outputs, AllowFailure → transmitFailed), so it
	// is warning-only here rather than auto-transformed. Both old and new type
	// paths are flagged.
	"io.kestra.plugin.core.flow.ForEach":        loopReplacementReason,
	"io.kestra.plugin.core.flow.ForEachItem":    loopReplacementReason,
	"io.kestra.plugin.core.flow.EachSequential": loopReplacementReason,
	"io.kestra.plugin.core.flow.EachParallel":   loopReplacementReason,
	"io.kestra.core.tasks.flows.EachSequential": loopReplacementReason,
	"io.kestra.core.tasks.flows.EachParallel":   loopReplacementReason,
	"io.kestra.core.tasks.flows.ForEachItem":    loopReplacementReason,
}

// loopReplacementReason is the shared warning for the flow-iteration task types
// removed in v2 in favor of io.kestra.plugin.core.flow.Loop.
const loopReplacementReason = "removed in v2; rewrite manually as io.kestra.plugin.core.flow.Loop (taskrun.value→item.value, taskrun.iteration→item.index, declare outputs, AllowFailure→transmitFailed)"

// detectRemovedTypes walks the document looking for type values that match
// entries in removedTypes and returns a warning string for each occurrence.
func detectRemovedTypes(doc *yaml.Node) []string {
	var warnings []string
	walkMappings(doc, func(m *yaml.Node) {
		typ := stringValue(m, "type")
		if reason, ok := removedTypes[typ]; ok {
			id := stringValue(m, "id")
			if id == "" {
				id = "(unknown)"
			}
			warnings = append(warnings, fmt.Sprintf("%s uses %s (%s)", id, typ, reason))
		}
	})
	return warnings
}

// detectPluginDefaults flags a flow-level `pluginDefaults` (or its deprecated
// `taskDefaults` alias) block. The keyword is removed in v2 at every scope —
// flow, namespace, tenant, and the global `kestra.plugins.defaults` config — and
// a flow that still carries the block fails to parse. Verified on the shipped
// releases/v2.0.x branch: the flow model has no `pluginDefaults` field and gains
// a `policyRefs` field instead.
//
// Warning-only, like the flow-iteration types: the replacement is an EE Policy
// (`Add` rules, with `forced: true` becoming `override: true`) or, in OSS,
// inlining the values onto each matching task. Neither can be derived
// mechanically — the tool cannot know which tasks a `type:`-prefix default
// applied to, and cannot create a Policy resource from inside a flow file.
// (flows-changes.md: "`taskDefaults` / `pluginDefaults` removed entirely")
func detectPluginDefaults(doc *yaml.Node) []string {
	root := docRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	var warnings []string
	for _, key := range []string{"pluginDefaults", "taskDefaults"} {
		if mappingValue(root, key) == nil {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"flow-level `%s` is removed in v2 and must be rewritten manually "+
				"(EE: a Policy with `Add` rules, referenced via `policyRefs:`; "+
				"OSS: inline the values onto each task or use flow `variables:`)",
			key))
	}
	return warnings
}

// scheduleTriggerType is the v2 canonical Schedule trigger type. After the
// rename rules run, every Schedule trigger uses this path.
const scheduleTriggerType = "io.kestra.plugin.core.trigger.Schedule"

// detectMissingTriggerInputs flags Schedule triggers that fail to supply a value
// for every flow input lacking a `defaults`. In v2 a trigger that launches
// executions non-interactively must be able to resolve every input; a `prefill`
// value and/or `required: false` do NOT satisfy this — `prefill` is only a UI
// hint for manual runs. The migrator cannot invent input values, so this is
// warning-only: fix by adding a `defaults` to the input or supplying it under
// the trigger's `inputs:` map (keyed by input id).
//
// The v1 verbose trigger-input form `inputs: {name: <id>, value: <v>}` provides
// keys literally named `name`/`value`, so it never matches a real input id and
// is correctly flagged here too.
// (flows-changes.md: "Triggers must supply every input lacking a `defaults`")
func detectMissingTriggerInputs(doc *yaml.Node) []string {
	root := docRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	inputs := mappingValue(root, "inputs")
	if inputs == nil || inputs.Kind != yaml.SequenceNode {
		return nil
	}
	// Collect input ids that have no `defaults` — an automatic trigger must
	// supply these for a scheduled execution to resolve. Inputs gated by a
	// `dependsOn` are only required when their condition holds, so we can't
	// statically say a scheduled run needs them — skip them to avoid false
	// positives.
	var needed []string
	for _, in := range inputs.Content {
		if in.Kind != yaml.MappingNode {
			continue
		}
		id := stringValue(in, "id")
		if id == "" || mappingValue(in, "defaults") != nil || mappingValue(in, "dependsOn") != nil {
			continue
		}
		needed = append(needed, id)
	}
	if len(needed) == 0 {
		return nil
	}
	triggers := mappingValue(root, "triggers")
	if triggers == nil || triggers.Kind != yaml.SequenceNode {
		return nil
	}
	var warnings []string
	for _, tr := range triggers.Content {
		if tr.Kind != yaml.MappingNode || stringValue(tr, "type") != scheduleTriggerType {
			continue
		}
		supplied := suppliedTriggerInputs(tr)
		trID := stringValue(tr, "id")
		if trID == "" {
			trID = "(unknown)"
		}
		for _, id := range needed {
			if !supplied[id] {
				warnings = append(warnings, fmt.Sprintf(
					"Schedule trigger '%s' does not supply input '%s' (which has no defaults); "+
						"v2 rejects this with \"Missing inputs for Schedule Trigger\" — add a `defaults` "+
						"to the input or set it under the trigger's `inputs:`",
					trID, id))
			}
		}
	}
	return warnings
}

// suppliedTriggerInputs returns the set of input ids a trigger provides via its
// `inputs:` mapping (the map keys). A missing or non-mapping `inputs:` supplies
// nothing.
func suppliedTriggerInputs(tr *yaml.Node) map[string]bool {
	supplied := map[string]bool{}
	inputs := mappingValue(tr, "inputs")
	if inputs == nil || inputs.Kind != yaml.MappingNode {
		return supplied
	}
	for i := 0; i+1 < len(inputs.Content); i += 2 {
		supplied[inputs.Content[i].Value] = true
	}
	return supplied
}

// removedTriggerConditionReason maps properties on trigger mappings that were
// removed in v2 (conditions redesign) to a human-readable guidance string.
// These are only flagged when they appear on an item of the top-level
// `triggers:` sequence — `conditions` also exists on Assert tasks and Switch
// cases and must not trigger a false positive there.
var removedTriggerConditionProperties = map[string]string{
	"scheduleConditions": "removed; rewrite as a top-level `when` Pebble expression on the trigger",
	"conditions":         "removed; rewrite as a top-level `when` Pebble expression (all triggers) or `dependsOn` entries (Flow trigger)",
	"preconditions":      "removed; rewrite as top-level `dependsOn` + `window` on the Flow trigger",
	"timeWindow":         "removed; rewrite as top-level `window` on the Flow trigger (deadline / from+to / every+offset / lookback)",
}

// conditionTypePrefixes lists the fully-qualified package prefixes under which
// trigger condition types may appear in v1 flows. Stripping one of these and
// the optional `Condition` suffix yields the condition's short base name
// (e.g. `DayWeek`, `Weekend`).
var conditionTypePrefixes = []string{
	"io.kestra.plugin.core.condition.",
	"io.kestra.core.models.conditions.types.",
}

// conditionBaseName returns the short condition name (e.g. "DayWeek") from a
// fully-qualified v1 condition type, or "" if the type is not a recognised
// condition path.
func conditionBaseName(typ string) string {
	for _, p := range conditionTypePrefixes {
		if strings.HasPrefix(typ, p) {
			return strings.TrimSuffix(typ[len(p):], "Condition")
		}
	}
	return ""
}

// conditionConverter converts a single v1 condition mapping into the body of a
// Pebble expression (no surrounding `{{ }}`). Returns ok=false if the shape
// cannot be rewritten safely.
type conditionConverter func(*yaml.Node) (string, bool)

var conditionConverters map[string]conditionConverter

func init() {
	// Declared in init() because Not / Or converters recurse via
	// convertCondition → conditionConverters, which would otherwise form a
	// static initialization cycle.
	conditionConverters = map[string]conditionConverter{
		"Expression":      convertExpressionCondition,
		"Variable":        convertExpressionCondition,
		"DayWeek":         convertDayWeekCondition,
		"DayWeekInMonth":  convertDayWeekInMonthCondition,
		"Weekend":         convertWeekendCondition,
		"PublicHoliday":   convertPublicHolidayCondition,
		"DateTimeBetween": convertDateTimeBetweenCondition,
		"TimeBetween":     convertTimeBetweenCondition,
		"HasRetryAttempt": convertHasRetryAttemptCondition,
		"Not":             convertNotCondition,
		"Or":              convertOrCondition,
	}
}

// convertCondition dispatches a single condition node to its converter.
func convertCondition(c *yaml.Node) (string, bool) {
	if c.Kind != yaml.MappingNode {
		return "", false
	}
	base := conditionBaseName(stringValue(c, "type"))
	if base == "" {
		return "", false
	}
	conv, ok := conditionConverters[base]
	if !ok {
		return "", false
	}
	return conv(c)
}

// unwrapPebble strips a surrounding `{{ }}` pair from a Pebble expression
// string so it can be embedded inside a larger expression.
func unwrapPebble(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		return strings.TrimSpace(s[2 : len(s)-2])
	}
	return s
}

func convertExpressionCondition(c *yaml.Node) (string, bool) {
	expr := stringValue(c, "expression")
	if expr == "" {
		return "", false
	}
	return unwrapPebble(expr), true
}

func convertDayWeekCondition(c *yaml.Node) (string, bool) {
	day := stringValue(c, "dayOfWeek")
	if day == "" {
		return "", false
	}
	return fmt.Sprintf("dayOfWeek(trigger.date) == '%s'", day), true
}

func convertDayWeekInMonthCondition(c *yaml.Node) (string, bool) {
	day := stringValue(c, "dayOfWeek")
	pos := stringValue(c, "dayInMonth")
	if day == "" || pos == "" {
		return "", false
	}
	return fmt.Sprintf("isDayWeekInMonth(trigger.date, '%s', '%s')", day, pos), true
}

func convertWeekendCondition(_ *yaml.Node) (string, bool) {
	return "isWeekend(trigger.date)", true
}

func convertPublicHolidayCondition(c *yaml.Node) (string, bool) {
	country := stringValue(c, "country")
	sub := stringValue(c, "subDivision")
	if country == "" {
		return "isPublicHoliday(trigger.date)", true
	}
	if sub != "" {
		return fmt.Sprintf("isPublicHoliday(trigger.date, '%s', '%s')", country, sub), true
	}
	return fmt.Sprintf("isPublicHoliday(trigger.date, '%s')", country), true
}

func convertDateTimeBetweenCondition(c *yaml.Node) (string, bool) {
	after := stringValue(c, "after")
	before := stringValue(c, "before")
	var parts []string
	if after != "" {
		parts = append(parts, fmt.Sprintf("trigger.date > '%s'", after))
	}
	if before != "" {
		parts = append(parts, fmt.Sprintf("trigger.date < '%s'", before))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " and "), true
}

// convertTimeBetweenCondition maps v1's `TimeBetween` (HH:MM:SS[±TZ] boundaries)
// onto `hourOfDay(trigger.date) >= X and hourOfDay(trigger.date) < Y`, per the
// migration docs. The v2 `hourOfDay()` helper is integer-valued, so we only
// rewrite when both `after` and `before` land on a whole hour — anything with
// a non-zero minute or second would silently lose precision. When only one
// boundary is present we emit only that half of the comparison.
func convertTimeBetweenCondition(c *yaml.Node) (string, bool) {
	after := stringValue(c, "after")
	before := stringValue(c, "before")
	var parts []string
	if after != "" {
		h, ok := parseWholeHour(after)
		if !ok {
			return "", false
		}
		parts = append(parts, fmt.Sprintf("hourOfDay(trigger.date) >= %d", h))
	}
	if before != "" {
		h, ok := parseWholeHour(before)
		if !ok {
			return "", false
		}
		parts = append(parts, fmt.Sprintf("hourOfDay(trigger.date) < %d", h))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " and "), true
}

// parseWholeHour extracts a 0..23 hour from "HH", "HH:MM", or "HH:MM:SS",
// tolerating an optional timezone suffix ("+02:00", "-05:00", "Z"). Returns
// ok=false if the minute or second component is non-zero, since the
// hour-only comparison in v2 can't represent sub-hour boundaries without
// loss.
func parseWholeHour(s string) (int, bool) {
	s = strings.TrimSpace(s)
	for _, suf := range []string{"Z"} {
		s = strings.TrimSuffix(s, suf)
	}
	// Strip trailing ±HH:MM / ±HHMM timezone offset, if any.
	if i := strings.LastIndexAny(s, "+-"); i > 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 1 || len(parts) > 3 {
		return 0, false
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, false
	}
	for _, p := range parts[1:] {
		n, err := strconv.Atoi(p)
		if err != nil || n != 0 {
			return 0, false
		}
	}
	return h, true
}

func convertHasRetryAttemptCondition(_ *yaml.Node) (string, bool) {
	return "hasRetryAttempt == true", true
}

// convertNotCondition rewrites a v1 `Not` wrapper faithfully: Kestra 1.x
// evaluated the inner `conditions` as an AND and negated the result, so
// `Not([A, B])` is exactly `not (A and B)`. Some authors wrote multi-child
// `Not` expecting per-child negation (de Morgan — "not A and not B"); that
// was a v1 bug in their flow, not a migration problem. We preserve v1
// semantics here and let the reader audit the migrated `when:` if they
// expected different behavior.
func convertNotCondition(c *yaml.Node) (string, bool) {
	inner := mappingValue(c, "conditions")
	if inner == nil || inner.Kind != yaml.SequenceNode || len(inner.Content) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(inner.Content))
	for _, child := range inner.Content {
		e, ok := convertCondition(child)
		if !ok {
			return "", false
		}
		parts = append(parts, e)
	}
	if len(parts) == 1 {
		return fmt.Sprintf("not (%s)", parts[0]), true
	}
	wrapped := make([]string, len(parts))
	for i, p := range parts {
		wrapped[i] = "(" + p + ")"
	}
	return fmt.Sprintf("not (%s)", strings.Join(wrapped, " and ")), true
}

func convertOrCondition(c *yaml.Node) (string, bool) {
	inner := mappingValue(c, "conditions")
	if inner == nil || inner.Kind != yaml.SequenceNode || len(inner.Content) == 0 {
		return "", false
	}
	exprs := make([]string, 0, len(inner.Content))
	for _, child := range inner.Content {
		e, ok := convertCondition(child)
		if !ok {
			return "", false
		}
		exprs = append(exprs, e)
	}
	if len(exprs) == 1 {
		return exprs[0], true
	}
	parts := make([]string, len(exprs))
	for i, e := range exprs {
		parts[i] = "(" + e + ")"
	}
	return strings.Join(parts, " or "), true
}

// joinAnd wraps one or more Pebble sub-expressions into a single `{{ … }}`
// block, combining multiple sub-expressions with `and`.
func joinAnd(exprs []string) string {
	if len(exprs) == 1 {
		return "{{ " + exprs[0] + " }}"
	}
	parts := make([]string, len(exprs))
	for i, e := range exprs {
		parts[i] = "(" + e + ")"
	}
	return "{{ " + strings.Join(parts, " and ") + " }}"
}

// rewriteTriggerConditions rewrites v1 `conditions` / `scheduleConditions` on
// non-Flow triggers into a single top-level `when:` Pebble expression. For
// Flow triggers, and for shapes the rewriter cannot safely handle, the original
// YAML is left untouched and a warning is emitted instead.
// (flows-changes.md: Trigger conditions → `when` / `dependsOn`)
func rewriteTriggerConditions(doc *yaml.Node) []string {
	root := docRoot(doc)
	if root == nil {
		return nil
	}
	triggers := mappingValue(root, "triggers")
	if triggers == nil || triggers.Kind != yaml.SequenceNode {
		return nil
	}
	var warnings []string
	for _, t := range triggers.Content {
		if t.Kind != yaml.MappingNode {
			continue
		}
		warnings = append(warnings, rewriteOneTrigger(t)...)
	}
	return warnings
}

func rewriteOneTrigger(t *yaml.Node) []string {
	triggerType := stringValue(t, "type")
	triggerID := stringValue(t, "id")
	if triggerID == "" {
		triggerID = "(unknown)"
	}

	warn := func(key string) string {
		return fmt.Sprintf("%s uses trigger.%s (%s)", triggerID, key, removedTriggerConditionProperties[key])
	}

	var warnings []string

	// For Flow triggers, `conditions` entries and `preconditions` map onto
	// `dependsOn` (plus optional `window:`). rewriteFlowTriggerToDependsOn
	// handles the common shapes in one pass; anything outside that subset —
	// `where` filters, non-deadline timeWindow types, wrappers we don't
	// handle, an existing `dependsOn`, etc. — is left for manual rewrite.
	isFlowTrigger := triggerType == "io.kestra.plugin.core.trigger.Flow"
	if isFlowTrigger {
		if rewriteFlowTriggerToDependsOn(t) {
			return warnings
		}
		for _, key := range []string{"scheduleConditions", "conditions", "preconditions", "timeWindow"} {
			if mappingValue(t, key) != nil {
				warnings = append(warnings, warn(key))
			}
		}
		return warnings
	}

	// Non-Flow triggers: `preconditions` / `timeWindow` are Flow-trigger
	// specific and should not appear here, but if they do, flag and leave.
	for _, key := range []string{"preconditions", "timeWindow"} {
		if mappingValue(t, key) != nil {
			warnings = append(warnings, warn(key))
		}
	}

	// Don't overwrite an existing `when:` — combining with it is risky.
	if mappingValue(t, "when") != nil {
		for _, key := range []string{"scheduleConditions", "conditions"} {
			if mappingValue(t, key) != nil {
				warnings = append(warnings, warn(key))
			}
		}
		return warnings
	}

	// Collect conditions from both possible keys on the trigger.
	type condSource struct {
		key   string
		nodes []*yaml.Node
	}
	var sources []condSource
	for _, key := range []string{"scheduleConditions", "conditions"} {
		v := mappingValue(t, key)
		if v != nil && v.Kind == yaml.SequenceNode && len(v.Content) > 0 {
			sources = append(sources, condSource{key: key, nodes: v.Content})
		}
	}
	if len(sources) == 0 {
		return warnings
	}

	// Flatten all conditions across both source keys and attempt conversion.
	exprs := make([]string, 0)
	for _, src := range sources {
		for _, c := range src.nodes {
			e, ok := convertCondition(c)
			if !ok {
				// At least one condition can't be rewritten — leave YAML as-is
				// and warn for every source key present.
				for _, s := range sources {
					warnings = append(warnings, warn(s.key))
				}
				return warnings
			}
			exprs = append(exprs, e)
		}
	}

	// All conditions converted. Remove old keys, add `when:`.
	for _, src := range sources {
		removeKey(t, src.key)
	}
	t.Content = append(t.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "when", Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: joinAnd(exprs), Tag: "!!str", Style: yaml.DoubleQuotedStyle},
	)
	return warnings
}

// flowConditionToWhenFragment converts a Flow-trigger condition node into a
// Pebble sub-expression body (no surrounding `{{ }}`). Used when the condition
// appears inside a wrapper (`Or`, `Not`) where every inner shape has to
// contribute to a single `when:` clause — i.e. no `flowId:` / `states:` /
// `labels:` field exists to receive per-field values.
//
// Returns ok=false for condition types that have no clean Pebble mapping
// (ExecutionStatus, ExecutionLabels), for unknown condition types, and for
// nested wrappers.
func flowConditionToWhenFragment(c *yaml.Node) (string, bool) {
	if c.Kind != yaml.MappingNode {
		return "", false
	}
	switch conditionBaseName(stringValue(c, "type")) {
	case "ExecutionNamespace":
		ns := stringValue(c, "namespace")
		if ns == "" {
			return "", false
		}
		prefix := stringValue(c, "prefix") == "true" || stringValue(c, "comparison") == "PREFIX"
		suffix := stringValue(c, "comparison") == "SUFFIX"
		switch {
		case prefix:
			return fmt.Sprintf("trigger.namespace startsWith '%s'", ns), true
		case suffix:
			return fmt.Sprintf("trigger.namespace endsWith '%s'", ns), true
		}
		return fmt.Sprintf("trigger.namespace == '%s'", ns), true
	case "ExecutionFlow":
		fid := stringValue(c, "flowId")
		ns := stringValue(c, "namespace")
		if fid == "" || ns == "" {
			return "", false
		}
		return fmt.Sprintf("trigger.flowId == '%s' and trigger.namespace == '%s'", fid, ns), true
	case "ExecutionOutputs":
		expr := stringValue(c, "expression")
		if expr == "" {
			return "", false
		}
		return unwrapPebble(expr), true
	case "HasRetryAttempt":
		return "hasRetryAttempt == true", true
	}
	return "", false
}

// rewriteFlowTriggerToDependsOn attempts to collapse a v1 Flow trigger's
// `conditions` list, `preconditions` block, and any top-level `states:`
// filter into a v2 `dependsOn:` list (plus optional top-level `window:`).
// It succeeds only when every piece maps cleanly onto entry fields with no
// ambiguity; otherwise it mutates nothing and returns false so the caller
// can fall back to a warning.
//
// Supported `conditions` types (AND-combined into a single entry when there
// is no `preconditions`; contributed as a shared `when:` clause on every
// entry when there is):
//
//   - ExecutionStatus     → entry `states:`
//   - ExecutionFlow       → entry `flowId:` + `namespace:`
//   - ExecutionNamespace  → entry `namespace:` (exact), or a when-clause
//     using startsWith/endsWith (prefix/suffix)
//   - ExecutionLabels     → entry `labels:`
//   - ExecutionOutputs    → entry `when:` clause
//   - HasRetryAttempt     → entry `when:` clause `hasRetryAttempt == true`
//   - Not (single child)  → entry `when:` clause `not (<child_when>)`, where
//     the child is one of the when-producing conditions
//
// Supported `preconditions` shape:
//
//   - `flows:`             required; each entry (flowId, namespace, optional
//     states) becomes one dependsOn entry.
//   - `timeWindow:`        optional; only `DAILY_TIME_DEADLINE` is mapped
//     (top-level `window: {deadline: ...}`).
//   - `id:`                dropped.
//   - `resetOnSuccess: true` dropped (v2 always resets); `false` → refuse.
//   - any other key → refuse.
//
// A bare-`ExecutionStatus` trigger (no `preconditions`) produces a
// `states:`-only entry — v1's "fire on any upstream execution with these
// states" maps directly to v2 `dependsOn: [{states: [...]}]`.
//
// We refuse (return false) when: there's nothing to rewrite; a wrapper we
// don't handle (Or / Not with a non-when inner / MultipleCondition) or an
// unsupported condition type is present; an existing `dependsOn:` is on the
// trigger; a top-level `timeWindow:` is on the trigger (rare v1 shape);
// a `preconditions.flows` entry conflicts with `states:` on the trigger or
// any condition; `preconditions.resetOnSuccess` is set to `false`; a condition in `conditions` would set a per-flow field
// (ExecutionFlow / exact ExecutionNamespace / ExecutionLabels) while
// `preconditions.flows` is also present; or any non-flows preconditions
// key other than `id`/`timeWindow` appears.
func rewriteFlowTriggerToDependsOn(t *yaml.Node) bool {
	if mappingValue(t, "dependsOn") != nil || mappingValue(t, "timeWindow") != nil {
		return false
	}

	conds := mappingValue(t, "conditions")
	preconds := mappingValue(t, "preconditions")
	topStates := mappingValue(t, "states")

	// Nothing to migrate.
	if conds == nil && preconds == nil {
		return false
	}

	// Collect fields contributed by `conditions`.
	cc, ok := collectFlowConditionFields(conds, preconds != nil)
	if !ok {
		return false
	}

	// Combined `states` source: at most one of top-level `states:` and
	// ExecutionStatus.in. Both present with different values → ambiguous.
	var sharedStates *yaml.Node
	switch {
	case topStates != nil && cc.statesNode != nil:
		return false
	case cc.statesNode != nil:
		sharedStates = cc.statesNode
	case topStates != nil:
		sharedStates = topStates
	}

	// Build the `dependsOn` entries and optional `window` node.
	var entries []*yaml.Node
	var windowNode *yaml.Node

	switch {
	case preconds != nil:
		pre, ok := parsePreconditions(preconds)
		if !ok {
			return false
		}
		switch {
		case pre.flows != nil:
			entries, ok = buildDependsOnFromFlows(pre.flows, sharedStates, cc.sharedWhenParts)
			if !ok {
				return false
			}
		case len(pre.whereEntries) > 0:
			entries = buildDependsOnFromWhere(pre.whereEntries, sharedStates, cc.sharedWhenParts)
		default:
			return false
		}
		windowNode = pre.windowNode
	case len(cc.mcFlowSources) > 0:
		entries = buildDependsOnFromSources(cc.mcFlowSources, sharedStates, cc.sharedWhenParts)
		windowNode = cc.mcWindowNode
	default:
		entries = []*yaml.Node{buildSingleDependsOnEntry(cc, sharedStates)}
	}

	// Commit: remove source keys, append new ones.
	removeKey(t, "conditions")
	removeKey(t, "preconditions")
	removeKey(t, "states")
	t.Content = append(t.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "dependsOn", Tag: "!!str"},
		&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: entries},
	)
	if windowNode != nil {
		t.Content = append(t.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "window", Tag: "!!str"},
			windowNode,
		)
	}
	return true
}

// flowConditionFields holds the fields a Flow-trigger `conditions` list
// contributes to the dependsOn output. Single-value fields (flowID, namespace,
// labelsNode, statesNode) map onto one entry; sharedWhenParts join with `and`
// into a single `when:` clause on either the single entry (no preconditions)
// or every entry (with preconditions).
type flowConditionFields struct {
	flowID          string
	namespace       string
	statesNode      *yaml.Node
	labelsNode      *yaml.Node
	sharedWhenParts []string

	// From a MultipleCondition wrapper: each inner ExecutionFlow becomes one
	// dependsOn entry, and window/windowAdvance lift to a top-level `window:`.
	mcFlowSources []flowSourceSpec
	mcWindowNode  *yaml.Node
}

// flowSourceSpec is a per-entry spec fanned out from either
// `preconditions.flows` or `MultipleCondition.conditions`. Only the fields
// that make sense per-entry are kept here.
type flowSourceSpec struct {
	flowID, namespace string
}

// collectFlowConditionFields walks a `conditions:` sequence and distributes
// each condition onto the appropriate target field. `preconditionsPresent`
// forbids condition types that would contend with preconditions.flows entries
// for per-flow fields (ExecutionFlow, exact ExecutionNamespace,
// ExecutionLabels, ExecutionStatus) — in that mode only when-contributors are
// allowed.
func collectFlowConditionFields(conds *yaml.Node, preconditionsPresent bool) (flowConditionFields, bool) {
	var cc flowConditionFields
	if conds == nil {
		return cc, true
	}
	if conds.Kind != yaml.SequenceNode || len(conds.Content) == 0 {
		return cc, false
	}
	for _, c := range conds.Content {
		if c.Kind != yaml.MappingNode {
			return cc, false
		}
		switch conditionBaseName(stringValue(c, "type")) {
		case "ExecutionStatus":
			if preconditionsPresent {
				return cc, false
			}
			in := mappingValue(c, "in")
			if in == nil || in.Kind != yaml.SequenceNode || cc.statesNode != nil {
				return cc, false
			}
			cc.statesNode = in
		case "ExecutionFlow":
			if preconditionsPresent {
				return cc, false
			}
			fid := stringValue(c, "flowId")
			ns := stringValue(c, "namespace")
			if fid == "" || ns == "" || cc.flowID != "" || (cc.namespace != "" && cc.namespace != ns) {
				return cc, false
			}
			cc.flowID = fid
			cc.namespace = ns
		case "ExecutionNamespace":
			ns := stringValue(c, "namespace")
			if ns == "" {
				return cc, false
			}
			prefix := stringValue(c, "prefix") == "true" || stringValue(c, "comparison") == "PREFIX"
			suffix := stringValue(c, "comparison") == "SUFFIX"
			switch {
			case prefix:
				cc.sharedWhenParts = append(cc.sharedWhenParts, fmt.Sprintf("trigger.namespace startsWith '%s'", ns))
			case suffix:
				cc.sharedWhenParts = append(cc.sharedWhenParts, fmt.Sprintf("trigger.namespace endsWith '%s'", ns))
			default:
				if preconditionsPresent {
					return cc, false
				}
				if cc.namespace != "" && cc.namespace != ns {
					return cc, false
				}
				cc.namespace = ns
			}
		case "ExecutionLabels":
			if preconditionsPresent {
				return cc, false
			}
			lbls := mappingValue(c, "labels")
			if lbls == nil || lbls.Kind != yaml.MappingNode || cc.labelsNode != nil {
				return cc, false
			}
			cc.labelsNode = lbls
		case "ExecutionOutputs":
			expr := stringValue(c, "expression")
			if expr == "" {
				return cc, false
			}
			cc.sharedWhenParts = append(cc.sharedWhenParts, unwrapPebble(expr))
		case "HasRetryAttempt":
			cc.sharedWhenParts = append(cc.sharedWhenParts, "hasRetryAttempt == true")
		case "Not":
			inner := mappingValue(c, "conditions")
			if inner == nil || inner.Kind != yaml.SequenceNode || len(inner.Content) != 1 {
				return cc, false
			}
			expr, ok := flowConditionToWhenFragment(inner.Content[0])
			if !ok {
				return cc, false
			}
			cc.sharedWhenParts = append(cc.sharedWhenParts, fmt.Sprintf("not (%s)", expr))
		case "Or":
			inner := mappingValue(c, "conditions")
			if inner == nil || inner.Kind != yaml.SequenceNode || len(inner.Content) == 0 {
				return cc, false
			}
			parts := make([]string, 0, len(inner.Content))
			for _, child := range inner.Content {
				expr, ok := flowConditionToWhenFragment(child)
				if !ok {
					return cc, false
				}
				parts = append(parts, expr)
			}
			if len(parts) == 1 {
				cc.sharedWhenParts = append(cc.sharedWhenParts, parts[0])
				break
			}
			wrapped := make([]string, len(parts))
			for i, p := range parts {
				wrapped[i] = "(" + p + ")"
			}
			cc.sharedWhenParts = append(cc.sharedWhenParts, strings.Join(wrapped, " or "))
		case "Multiple": // conditionBaseName strips the trailing `Condition`
			if preconditionsPresent || len(cc.mcFlowSources) > 0 {
				return cc, false
			}
			flows, win, ok := parseMultipleCondition(c)
			if !ok {
				return cc, false
			}
			cc.mcFlowSources = flows
			cc.mcWindowNode = win
		default:
			return cc, false
		}
	}
	// MultipleCondition fans out into per-entry fields; it must not coexist
	// with condition-driven per-entry values from the same list.
	if len(cc.mcFlowSources) > 0 && (cc.flowID != "" || cc.namespace != "" || cc.labelsNode != nil) {
		return cc, false
	}
	return cc, true
}

// parseMultipleCondition validates a `MultipleCondition` node and extracts
// its inner ExecutionFlow entries (fanned out as dependsOn) plus the optional
// `window`/`windowAdvance` pair (lifted to a top-level v2 `window:` mapping).
// Returns ok=false if the inner map contains a non-ExecutionFlow, any inner
// entry is missing `flowId`/`namespace`, or `windowAdvance` is non-zero (we
// don't translate forward-shifted windows).
func parseMultipleCondition(c *yaml.Node) ([]flowSourceSpec, *yaml.Node, bool) {
	inner := mappingValue(c, "conditions")
	if inner == nil || inner.Kind != yaml.MappingNode || len(inner.Content) == 0 {
		return nil, nil, false
	}
	var flows []flowSourceSpec
	for i := 0; i+1 < len(inner.Content); i += 2 {
		cond := inner.Content[i+1]
		if cond.Kind != yaml.MappingNode {
			return nil, nil, false
		}
		if conditionBaseName(stringValue(cond, "type")) != "ExecutionFlow" {
			return nil, nil, false
		}
		fid := stringValue(cond, "flowId")
		ns := stringValue(cond, "namespace")
		if fid == "" || ns == "" {
			return nil, nil, false
		}
		flows = append(flows, flowSourceSpec{flowID: fid, namespace: ns})
	}

	win := stringValue(c, "window")
	winAdv := stringValue(c, "windowAdvance")
	if winAdv != "" && winAdv != "P0D" && winAdv != "PT0S" {
		return nil, nil, false
	}
	var windowNode *yaml.Node
	if win != "" {
		windowNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "lookback", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: win, Tag: "!!str"},
		}}
	}
	return flows, windowNode, true
}

// preconditionsInfo holds the parsed, supported pieces of a v1 Flow trigger
// `preconditions` block. Exactly one of `flows` or `whereEntries` is set.
// `windowNode` is the translated top-level v2 `window:` value (nil if no
// timeWindow was present).
type preconditionsInfo struct {
	flows        *yaml.Node
	whereEntries []string // one compiled Pebble when-fragment per `where` entry
	windowNode   *yaml.Node
}

// parsePreconditions validates and extracts the supported subset of a
// `preconditions:` mapping: exactly one of `flows` (sequence of
// {namespace, flowId, states?}) or `where` (v1 filter-predicate list), plus
// optional `timeWindow` (only DAILY_TIME_DEADLINE is mapped) and `id`
// (dropped), plus `resetOnSuccess: true` (dropped, since v2 always resets after
// the trigger has fired). Any other key — `resetOnSuccess: false`, both `flows`
// and `where` at once, or an unsupported filter shape inside `where` — causes
// refusal.
func parsePreconditions(p *yaml.Node) (preconditionsInfo, bool) {
	var info preconditionsInfo
	if p.Kind != yaml.MappingNode {
		return info, false
	}
	for i := 0; i+1 < len(p.Content); i += 2 {
		key := p.Content[i].Value
		val := p.Content[i+1]
		switch key {
		case "id":
			// Dropped per v2 migration guide.
		case "resetOnSuccess":
			// v2 always resets after the trigger has fired, so an explicit `true` is
			// the new behavior and is dropped. `false` has no equivalent — it needs
			// `mode: ANY` — so the rewrite is refused and left as a warning.
			if val.Kind != yaml.ScalarNode || val.Value != "true" {
				return info, false
			}
		case "flows":
			if val.Kind != yaml.SequenceNode || len(val.Content) == 0 {
				return info, false
			}
			info.flows = val
		case "where":
			entries, ok := parseWhereEntries(val)
			if !ok {
				return info, false
			}
			info.whereEntries = entries
		case "timeWindow":
			if val.Kind != yaml.MappingNode {
				return info, false
			}
			if stringValue(val, "type") != "DAILY_TIME_DEADLINE" {
				return info, false
			}
			deadline := stringValue(val, "deadline")
			if deadline == "" {
				return info, false
			}
			info.windowNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "deadline", Tag: "!!str"},
				{Kind: yaml.ScalarNode, Value: deadline, Tag: "!!str", Style: yaml.DoubleQuotedStyle},
			}}
		default:
			return info, false
		}
	}
	// Exactly one of `flows` / `where` must be present.
	hasFlows := info.flows != nil
	hasWhere := len(info.whereEntries) > 0
	if hasFlows == hasWhere {
		return info, false
	}
	return info, true
}

// parseWhereEntries parses a `preconditions.where` sequence into one
// combined Pebble `when:` fragment per entry (filters inside an entry are
// AND-combined). Each returned string is a sub-expression without the
// surrounding `{{ }}`. Returns ok=false if any filter shape is unsupported.
func parseWhereEntries(seq *yaml.Node) ([]string, bool) {
	if seq.Kind != yaml.SequenceNode || len(seq.Content) == 0 {
		return nil, false
	}
	entries := make([]string, 0, len(seq.Content))
	for _, entry := range seq.Content {
		if entry.Kind != yaml.MappingNode {
			return nil, false
		}
		filters := mappingValue(entry, "filters")
		if filters == nil || filters.Kind != yaml.SequenceNode || len(filters.Content) == 0 {
			return nil, false
		}
		parts := make([]string, 0, len(filters.Content))
		for _, f := range filters.Content {
			expr, ok := whereFilterToWhenFragment(f)
			if !ok {
				return nil, false
			}
			parts = append(parts, expr)
		}
		switch len(parts) {
		case 1:
			entries = append(entries, parts[0])
		default:
			wrapped := make([]string, len(parts))
			for i, p := range parts {
				wrapped[i] = "(" + p + ")"
			}
			entries = append(entries, strings.Join(wrapped, " and "))
		}
	}
	return entries, true
}

// whereFilterToWhenFragment translates one {field, type, value} filter into
// a Pebble sub-expression. Supported:
//
//   - NAMESPACE + EQUALS / NOT_EQUALS / STARTS_WITH / ENDS_WITH
//   - FLOW_ID   + EQUALS / NOT_EQUALS / STARTS_WITH / ENDS_WITH
//   - EXPRESSION + IS_TRUE (value is the Pebble expression, possibly wrapped
//     in {{ }}, unwrapped here)
//
// Any other combination returns ok=false so the caller can fall back to
// a warning.
func whereFilterToWhenFragment(f *yaml.Node) (string, bool) {
	if f.Kind != yaml.MappingNode {
		return "", false
	}
	field := stringValue(f, "field")
	typ := stringValue(f, "type")
	value := stringValue(f, "value")

	if field == "EXPRESSION" {
		if value == "" || (typ != "IS_TRUE" && typ != "") {
			return "", false
		}
		return unwrapPebble(value), true
	}

	var target string
	switch field {
	case "NAMESPACE":
		target = "trigger.namespace"
	case "FLOW_ID":
		target = "trigger.flowId"
	default:
		return "", false
	}
	if value == "" {
		return "", false
	}
	switch typ {
	case "EQUALS":
		return fmt.Sprintf("%s == '%s'", target, value), true
	case "NOT_EQUALS":
		return fmt.Sprintf("%s != '%s'", target, value), true
	case "STARTS_WITH":
		return fmt.Sprintf("%s startsWith '%s'", target, value), true
	case "ENDS_WITH":
		return fmt.Sprintf("%s endsWith '%s'", target, value), true
	}
	return "", false
}

// buildDependsOnFromFlows expands each `preconditions.flows` entry into a
// `dependsOn` entry. Per-flow `states` win over shared states. When both a
// per-flow and a shared states are present AND they carry different values
// we refuse (intersection would change semantics). Identical values collapse
// to a single copy on the entry. The shared `when` clause is duplicated on
// every entry.
func buildDependsOnFromFlows(flows *yaml.Node, sharedStates *yaml.Node, sharedWhenParts []string) ([]*yaml.Node, bool) {
	entries := make([]*yaml.Node, 0, len(flows.Content))
	for _, f := range flows.Content {
		if f.Kind != yaml.MappingNode {
			return nil, false
		}
		fid := stringValue(f, "flowId")
		ns := stringValue(f, "namespace")
		if fid == "" || ns == "" {
			return nil, false
		}
		perStates := mappingValue(f, "states")
		if perStates != nil && sharedStates != nil && !yamlValueEqual(perStates, sharedStates) {
			return nil, false
		}
		entryStates := perStates
		if entryStates == nil {
			entryStates = sharedStates
		}
		entries = append(entries, makeDependsOnEntry("", fid, ns, nil, entryStates, sharedWhenParts))
	}
	return entries, true
}

// yamlValueEqual returns true when two YAML nodes represent the same value.
// For the shapes we actually compare (scalars and sequences of scalars —
// `states: [...]` lists) this is straightforward structural equality.
func yamlValueEqual(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case yaml.ScalarNode:
		return a.Value == b.Value
	case yaml.SequenceNode, yaml.MappingNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := range a.Content {
			if !yamlValueEqual(a.Content[i], b.Content[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// buildSingleDependsOnEntry builds the one entry used when there are no
// `preconditions` — all fields come from the `conditions` list.
func buildSingleDependsOnEntry(cc flowConditionFields, sharedStates *yaml.Node) *yaml.Node {
	return makeDependsOnEntry("", cc.flowID, cc.namespace, cc.labelsNode, sharedStates, cc.sharedWhenParts)
}

// buildDependsOnFromWhere fans a list of compiled `where`-entry when-fragments
// into per-entry dependsOn entries. Each entry carries its own `when:` clause
// (the entry's AND-combined filter fragments, plus any trigger-level shared
// when parts prefixed with `and`) and inherits the shared states.
func buildDependsOnFromWhere(whereFragments []string, sharedStates *yaml.Node, sharedWhenParts []string) []*yaml.Node {
	entries := make([]*yaml.Node, 0, len(whereFragments))
	for _, frag := range whereFragments {
		allParts := make([]string, 0, len(sharedWhenParts)+1)
		allParts = append(allParts, sharedWhenParts...)
		allParts = append(allParts, frag)
		entries = append(entries, makeDependsOnEntry("", "", "", nil, sharedStates, allParts))
	}
	return entries
}

// buildDependsOnFromSources fans a list of {flowID, namespace} pairs (typically
// from a MultipleCondition wrapper) into per-source dependsOn entries, each
// sharing the same `states:` and `when:` inherited from the trigger level.
func buildDependsOnFromSources(sources []flowSourceSpec, sharedStates *yaml.Node, sharedWhenParts []string) []*yaml.Node {
	entries := make([]*yaml.Node, 0, len(sources))
	for _, s := range sources {
		entries = append(entries, makeDependsOnEntry("", s.flowID, s.namespace, nil, sharedStates, sharedWhenParts))
	}
	return entries
}

// makeDependsOnEntry assembles a dependsOn mapping node with a stable field
// order: flowId, namespace, labels, when, states.
func makeDependsOnEntry(_ string, flowID, namespace string, labelsNode *yaml.Node, statesNode *yaml.Node, whenParts []string) *yaml.Node {
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(k string, v *yaml.Node) {
		entry.Content = append(entry.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"},
			v,
		)
	}
	if flowID != "" {
		add("flowId", &yaml.Node{Kind: yaml.ScalarNode, Value: flowID, Tag: "!!str"})
	}
	if namespace != "" {
		add("namespace", &yaml.Node{Kind: yaml.ScalarNode, Value: namespace, Tag: "!!str"})
	}
	if labelsNode != nil {
		add("labels", labelsNode)
	}
	if len(whenParts) > 0 {
		add("when", &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: joinAnd(whenParts),
			Tag:   "!!str",
			Style: yaml.DoubleQuotedStyle,
		})
	}
	if statesNode != nil {
		add("states", statesNode)
	}
	return entry
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// docRoot returns the root mapping node of a DocumentNode, or nil.
func docRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return nil
}

// walkMappings calls fn on every MappingNode in the tree (depth-first).
func walkMappings(node *yaml.Node, fn func(*yaml.Node)) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		fn(node)
	}
	for _, child := range node.Content {
		walkMappings(child, fn)
	}
}

// mappingValue returns the value node for the given key in a mapping, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// stringValue returns the string value for key in mapping m, or "".
func stringValue(m *yaml.Node, key string) string {
	v := mappingValue(m, key)
	if v != nil {
		return v.Value
	}
	return ""
}

// setStringValue sets the value of an existing key in mapping m.
func setStringValue(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Value = value
			return
		}
	}
}

// renameKey renames the first occurrence of key `from` to `to` in mapping m.
func renameKey(m *yaml.Node, from, to string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == from {
			m.Content[i].Value = to
			return
		}
	}
}

// removeKey removes the key-value pair for key from mapping m.
func removeKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// addBoolKey appends a boolean key-value pair to mapping m.
func addBoolKey(m *yaml.Node, key string, value bool) {
	v := "false"
	if value {
		v = "true"
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: "!!bool"},
	)
}

// migrateHTTPBasicAuth converts deprecated `basicAuthUser` / `basicAuthPassword`
// inside the `options` mapping to the new `auth` sub-object structure:
//
//	options:
//	  auth:
//	    type: BASIC
//	    username: <user>
//	    password: <pass>
//
// This applies to any task with an `options` mapping containing these fields,
// not just core HTTP tasks — many plugins share the same HTTP options structure.
// (flows-changes.md: HTTP task properties restructured)
func migrateHTTPBasicAuth(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		opts := mappingValue(m, "options")
		if opts == nil || opts.Kind != yaml.MappingNode {
			return
		}
		user := stringValue(opts, "basicAuthUser")
		pass := stringValue(opts, "basicAuthPassword")
		if user == "" && pass == "" {
			return
		}
		removeKey(opts, "basicAuthUser")
		removeKey(opts, "basicAuthPassword")

		// Build the auth mapping node.
		authMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		authMap.Content = append(authMap.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "type", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "BASIC", Tag: "!!str"},
		)
		if user != "" {
			authMap.Content = append(authMap.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "username", Tag: "!!str"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: user, Tag: "!!str"},
			)
		}
		if pass != "" {
			authMap.Content = append(authMap.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "password", Tag: "!!str"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: pass, Tag: "!!str"},
			)
		}
		opts.Content = append(opts.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "auth", Tag: "!!str"},
			authMap,
		)
	})
	return nil
}

// removeDeprecatedHTTPOptions removes deprecated properties from any task's
// `options` mapping that no longer exist in v2.
// (flows-changes.md: HTTP task properties restructured)
func removeDeprecatedHTTPOptions(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		opts := mappingValue(m, "options")
		if opts == nil || opts.Kind != yaml.MappingNode {
			return
		}
		removeKey(opts, "connectionPoolIdleTimeout")
	})
	return nil
}
