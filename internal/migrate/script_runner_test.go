package migrate

import "testing"

// kestra-ee#11384: the reproducer from the issue. On 2.0 `runner` still parses
// but is ignored (PROCESS tasks silently move into Docker), and `docker` is
// gone, so the second task is rejected on save.
func TestApply_ScriptRunner_Issue11384(t *testing.T) {
	in := `id: script_runner
namespace: company.team

tasks:
  - id: process_runner
    type: io.kestra.plugin.scripts.shell.Commands
    runner: PROCESS
    commands:
      - echo hello

  - id: docker_runner
    type: io.kestra.plugin.scripts.shell.Commands
    runner: DOCKER
    docker:
      image: ubuntu:24.04
    commands:
      - echo hello
`
	want := `id: script_runner
namespace: company.team

tasks:
  - id: process_runner
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    commands:
      - echo hello

  - id: docker_runner
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
    containerImage: ubuntu:24.04
    commands:
      - echo hello
`
	out, warnings := applyWithWarnings(t, in)
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestApply_ScriptRunner(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			// v1.3 used Docker.from(docker) whenever `docker` was set and no
			// taskRunner was: every DockerOptions property is a Docker runner
			// property of the same name, except `image` → containerImage.
			name: "docker options without runner",
			in: `id: f
namespace: n
tasks:
  - id: py
    type: io.kestra.plugin.scripts.python.Script
    docker:
      image: python:3.12-slim
      pullPolicy: ALWAYS
      networkMode: host
      volumes:
        - /data:/data
    script: print(1)
`,
			want: `id: f
namespace: n
tasks:
  - id: py
    type: io.kestra.plugin.scripts.python.Script
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
      pullPolicy: ALWAYS
      networkMode: host
      volumes:
        - /data:/data
    containerImage: python:3.12-slim
    script: print(1)
`,
		},
		{
			// docker.image won over containerImage on v1.3 (Docker.image is
			// only defaulted from containerImage when unset).
			name: "docker image overrides containerImage",
			in: `id: f
namespace: n
tasks:
  - id: py
    type: io.kestra.plugin.scripts.python.Script
    containerImage: python:3.11
    runner: DOCKER
    docker:
      image: python:3.12
    script: print(1)
`,
			want: `id: f
namespace: n
tasks:
  - id: py
    type: io.kestra.plugin.scripts.python.Script
    containerImage: python:3.12
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
    script: print(1)
`,
		},
		{
			// DOCKER without docker options: Docker.from(null), the plain runner.
			name: "docker runner without options",
			in: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    runner: DOCKER
    commands:
      - echo hi
`,
			want: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
    commands:
      - echo hi
`,
		},
		{
			// PROCESS ignores docker options entirely on v1.3.
			name: "process runner drops docker options",
			in: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    runner: PROCESS
    docker:
      image: ubuntu
    commands:
      - echo hi
`,
			want: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    commands:
      - echo hi
`,
		},
		{
			// The legacy runner turned the output directory on by default
			// (CommandsWrapper.getEnableOutputDirectory); taskRunner does not.
			// A task that writes to {{ outputDir }} keeps it explicitly.
			name: "legacy runner using outputDir keeps the output directory",
			in: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    runner: PROCESS
    commands:
      - echo hi > {{ outputDir }}/out.txt
`,
			want: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    outputDirectory: true
    commands:
      - echo hi > {{ outputDir }}/out.txt
`,
		},
		{
			name: "explicit outputDirectory is kept",
			in: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    runner: PROCESS
    outputDirectory: false
    commands:
      - echo hi > {{ outputDir }}/out.txt
`,
			want: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    outputDirectory: false
    commands:
      - echo hi > {{ outputDir }}/out.txt
`,
		},
		{
			// An explicit taskRunner took precedence over `runner` on v1.3.
			name: "taskRunner wins over runner",
			in: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    runner: DOCKER
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    commands:
      - echo hi
`,
			want: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    commands:
      - echo hi
`,
		},
		{
			// …except that a Docker taskRunner was replaced wholesale by
			// Docker.from(docker) when docker options were set.
			name: "docker options replace a Docker taskRunner",
			in: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
      pullPolicy: NEVER
    docker:
      image: alpine:3
      user: "1000"
    commands:
      - echo hi
`,
			want: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.scripts.runner.docker.Docker
      user: "1000"
    containerImage: alpine:3
    commands:
      - echo hi
`,
		},
		{
			// A non-Docker taskRunner ignored docker options.
			name: "docker options ignored by a non-Docker taskRunner",
			in: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    docker:
      image: alpine:3
    commands:
      - echo hi
`,
			want: `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    commands:
      - echo hi
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out := apply(t, c.in); out != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", out, c.want)
			}
		})
	}
}

// Only tasks built on the core AbstractExecScript are rewritten: other plugins
// (e.g. modal.cli.ModalCLI) still declare their own `docker` property on 2.0.
func TestApply_ScriptRunner_OtherPluginsUntouched(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: modal
    type: io.kestra.plugin.modal.cli.ModalCLI
    commands:
      - modal run x.py
    docker:
      image: ghcr.io/kestra-io/modal:latest
`
	if out := apply(t, in); out != in {
		t.Errorf("expected unchanged output, got:\n%s", out)
	}
}

// taskRunner / Process / containerImage all exist on v1.3, so the rewrite
// also runs under --stay-v1-compatible.
func TestApply_ScriptRunner_StayV1Compatible(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    runner: PROCESS
    commands:
      - echo hi
`
	want := `id: f
namespace: n
tasks:
  - id: sh
    type: io.kestra.plugin.scripts.shell.Commands
    taskRunner:
      type: io.kestra.plugin.core.runner.Process
    commands:
      - echo hi
`
	if out := applyV1Compatible(t, in); out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
