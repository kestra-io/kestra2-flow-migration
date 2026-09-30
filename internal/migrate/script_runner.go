package migrate

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	dockerTaskRunner  = "io.kestra.plugin.scripts.runner.docker.Docker"
	processTaskRunner = "io.kestra.plugin.core.runner.Process"
)

// outputDirReference matches a Pebble reference to the `outputDir` variable,
// but not the `outputDirectory` property name.
var outputDirReference = regexp.MustCompile(`\boutputDir\b`)

// migrateLegacyScriptRunner rewrites the deprecated `runner` / `docker`
// properties of script tasks into `taskRunner` (+ `containerImage`),
// reproducing how v1.3 resolved them (CommandsWrapper.getTaskRunner):
//
//   - an explicit `taskRunner` wins over `runner`, which is dropped — except
//     that a Docker `taskRunner` is replaced by one built from `docker`;
//   - `runner: PROCESS` → the Process runner, `docker` ignored;
//   - `runner: DOCKER`, or `docker` alone → a Docker runner carrying every
//     `docker` property (same names on both classes), with `docker.image`
//     moved to `containerImage` (it won over `containerImage` on v1.3);
//   - the legacy runner enabled the output directory by default, so a task
//     that references `{{ outputDir }}` gets an explicit
//     `outputDirectory: true`.
//
// On 2.0 `runner` still parses but is ignored — a PROCESS task silently runs
// in Docker — and `docker` is removed, so the flow is rejected on save.
// Scoped to `io.kestra.plugin.scripts.*` (the core AbstractExecScript
// family): other plugins, e.g. modal.cli.ModalCLI, still declare their own
// `docker` property. Runs under --stay-v1-compatible too: taskRunner, the
// Process runner and containerImage all exist on v1.3.
// (flows-changes.md: `runner` removed)
func migrateLegacyScriptRunner(doc *yaml.Node) error {
	walkMappings(doc, func(m *yaml.Node) {
		if !strings.HasPrefix(stringValue(m, "type"), "io.kestra.plugin.scripts.") {
			return
		}
		runnerNode := mappingValue(m, "runner")
		docker := mappingValue(m, "docker")
		if runnerNode == nil && docker == nil {
			return
		}
		runner := ""
		if runnerNode != nil {
			if runnerNode.Kind != yaml.ScalarNode || (runnerNode.Value != "DOCKER" && runnerNode.Value != "PROCESS") {
				return
			}
			runner = runnerNode.Value
		}
		if docker != nil && docker.Kind != yaml.MappingNode {
			return
		}
		existingRunner := mappingValue(m, "taskRunner")

		// newRunner replaces (or creates) the taskRunner; nil keeps the
		// existing one. image is docker.image, applied to containerImage.
		var newRunner, image *yaml.Node
		switch {
		case existingRunner != nil:
			if docker != nil && existingRunner.Kind == yaml.MappingNode && stringValue(existingRunner, "type") == dockerTaskRunner {
				newRunner, image = dockerRunnerFrom(docker)
			}
		case runner == "PROCESS":
			newRunner = runnerOfType(processTaskRunner)
		default:
			newRunner, image = dockerRunnerFrom(docker)
		}
		addOutputDirectory := runner != "" && existingRunner == nil &&
			mappingValue(m, "outputDirectory") == nil && referencesOutputDir(m)
		hasContainerImage := mappingValue(m, "containerImage") != nil

		lastLegacyKey := "docker"
		if docker == nil || (runnerNode != nil && keyIndex(m, "runner") > keyIndex(m, "docker")) {
			lastLegacyKey = "runner"
		}
		content := make([]*yaml.Node, 0, len(m.Content)+4)
		emittedRunner := existingRunner != nil
		for i := 0; i+1 < len(m.Content); i += 2 {
			k, v := m.Content[i], m.Content[i+1]
			switch k.Value {
			case "runner", "docker":
				if !emittedRunner {
					content = append(content, scalarKey("taskRunner"), newRunner)
					emittedRunner = true
				}
				if k.Value == "docker" && image != nil && !hasContainerImage {
					content = append(content, scalarKey("containerImage"), image)
				}
				if k.Value == lastLegacyKey && addOutputDirectory {
					content = append(content, scalarKey("outputDirectory"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
				}
			case "taskRunner":
				if newRunner != nil {
					v = newRunner
				}
				content = append(content, k, v)
			case "containerImage":
				if image != nil {
					v = image
				}
				content = append(content, k, v)
			default:
				content = append(content, k, v)
			}
		}
		m.Content = content
	})
	return nil
}

// dockerRunnerFrom mirrors v1.3's Docker.from(DockerOptions): a Docker task
// runner carrying every docker option but `image`, which is returned
// separately for `containerImage`. A nil docker yields the plain runner.
func dockerRunnerFrom(docker *yaml.Node) (runner, image *yaml.Node) {
	runner = runnerOfType(dockerTaskRunner)
	if docker == nil {
		return runner, nil
	}
	for i := 0; i+1 < len(docker.Content); i += 2 {
		if docker.Content[i].Value == "image" {
			image = docker.Content[i+1]
			continue
		}
		runner.Content = append(runner.Content, docker.Content[i], docker.Content[i+1])
	}
	return runner, image
}

func runnerOfType(typ string) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		scalarKey("type"), {Kind: yaml.ScalarNode, Tag: "!!str", Value: typ},
	}}
}

func scalarKey(name string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
}

// keyIndex returns the position of key in mapping m, or -1.
func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// referencesOutputDir reports whether any scalar value under m mentions the
// `outputDir` variable.
func referencesOutputDir(m *yaml.Node) bool {
	found := false
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if found || n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode && outputDirReference.MatchString(n.Value) {
			found = true
			return
		}
		for i, c := range n.Content {
			if n.Kind == yaml.MappingNode && i%2 == 0 {
				continue
			}
			walk(c)
		}
	}
	walk(m)
	return found
}
