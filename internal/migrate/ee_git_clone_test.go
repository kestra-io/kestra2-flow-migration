package migrate

import (
	"strings"
	"testing"
)

// #38: io.kestra.plugin.ee.git.Clone was an internal duplicate of plugin-git's
// Clone, deleted in plugin-ee-git 2.2.1: EE 2.0 rejects it as an unknown type,
// and adding `auth:` (the old advice) does not help. It becomes
// io.kestra.plugin.git.Clone, pinning the strictHostKeyChecking: true default
// the EE copy had (git.Clone defaults to false).
func TestApply_EEGitClone_Issue38(t *testing.T) {
	in := `id: qa_auth_ee_clone
namespace: qa.global
tasks:
  - id: t
    type: io.kestra.plugin.ee.git.Clone
    url: https://github.com/kestra-io/scripts
`
	want := `id: qa_auth_ee_clone
namespace: qa.global
tasks:
  - id: t
    type: io.kestra.plugin.git.Clone
    url: https://github.com/kestra-io/scripts
    strictHostKeyChecking: true
`
	out, warnings := applyWithWarningDetails(t, in)
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings (git.Clone needs no auth), got %+v", warnings)
	}
}

// Every other property exists on git.Clone under the same name and is kept
// as is, `auth` included (optional there); an explicit strictHostKeyChecking
// wins over the pinned default.
func TestApply_EEGitClone_KeepsProperties(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: clone
    type: io.kestra.plugin.ee.git.Clone
    url: git@github.com:org/repo.git
    branch: main
    directory: src
    depth: 5
    privateKey: "{{ secret('SSH_KEY') }}"
    strictHostKeyChecking: false
    auth:
      apiToken: "{{ secret('KESTRA_API_TOKEN') }}"
`
	want := strings.Replace(in, "io.kestra.plugin.ee.git.Clone", "io.kestra.plugin.git.Clone", 1)
	if out := apply(t, in); out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// The OSS Clone is untouched.
func TestApply_EEGitClone_OSSCloneUntouched(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: clone
    type: io.kestra.plugin.git.Clone
    url: https://github.com/org/repo
`
	if out := apply(t, in); out != in {
		t.Errorf("expected unchanged output, got:\n%s", out)
	}
}

// ee.git.Clone still exists on v1.3 (plugin-ee-git 1.7.2), whose git.Clone
// (plugin-git 2.0.12) has no strictHostKeyChecking property: v2 path only.
func TestApply_EEGitClone_SkippedUnderStayV1Compatible(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: clone
    type: io.kestra.plugin.ee.git.Clone
    url: https://github.com/org/repo
`
	if out := applyV1Compatible(t, in); out != in {
		t.Errorf("expected unchanged output under --stay-v1-compatible, got:\n%s", out)
	}
}
