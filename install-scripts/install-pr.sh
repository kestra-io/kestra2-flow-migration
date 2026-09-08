#!/usr/bin/env bash
# Install the binary built by the PR Build workflow for an open pull request,
# so a reviewer can try the PR without a Go toolchain or a checkout.
#
#   curl -fsSL .../install-pr.sh | PR=3 bash
#   curl -fsSL .../install-pr.sh | bash -s -- 3
#
# Unlike install.sh, this needs the `gh` CLI: GitHub Actions artifacts are not
# anonymously downloadable, so the download has to be authenticated.
set -euo pipefail

GITHUB_REPO="${GITHUB_REPO:-kestra-io/kestra2-flow-migration}"
WORKFLOW="${WORKFLOW:-pr-build.yml}"
PR="${PR:-${1:-}}"
INSTALL_DIR="${INSTALL_DIR:-}"
# Installed under a PR-specific name so it never shadows a released
# kestra-migrate the reviewer may already depend on.
BINARY_NAME="${BINARY_NAME:-}"

err() {
  printf "Error: %s\n" "$1" >&2
  exit 1
}

[ -n "$PR" ] || err "No pull request given. Use: PR=<number> bash, or bash -s -- <number>"
case "$PR" in
  ''|*[!0-9]*) err "Pull request must be a number, got: $PR" ;;
esac

command -v gh >/dev/null 2>&1 || err "The gh CLI is required (https://cli.github.com/)"
gh auth status >/dev/null 2>&1 || err "gh is not authenticated. Run: gh auth login"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin|linux) ;;
  *) err "Unsupported OS: $os" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) err "Unsupported architecture: $arch" ;;
esac

head_sha="$(gh pr view "$PR" --repo "$GITHUB_REPO" --json headRefOid --jq .headRefOid 2>/dev/null || true)"
[ -n "$head_sha" ] || err "Pull request #${PR} not found in ${GITHUB_REPO}"

# Match the run to the PR head SHA rather than to the branch: that way an
# install can never hand back a binary built from an older push. gh's own --jq
# does the filtering, so this script needs no jq of its own.
runs_api="repos/${GITHUB_REPO}/actions/workflows/${WORKFLOW}/runs?head_sha=${head_sha}&per_page=100"
runs_jq='{
  id: ([.workflow_runs[] | select(.status == "completed" and .conclusion == "success")]
        | sort_by(.run_started_at) | last | .id // ""),
  pending: ([.workflow_runs[] | select(.status != "completed")] | length)
} | "\(.id) \(.pending)"'

if ! summary="$(gh api "$runs_api" --jq "$runs_jq" 2>/dev/null)"; then
  err "Workflow ${WORKFLOW} not found in ${GITHUB_REPO}. It may not have been merged to the default branch yet."
fi
run_id="${summary%% *}"
pending="${summary##* }"

if [ -z "$run_id" ]; then
  if [ "$pending" != "0" ]; then
    err "PR Build for #${PR} (${head_sha:0:7}) has not finished yet. Watch it: gh run watch --repo ${GITHUB_REPO}"
  fi
  err "No successful PR Build run for #${PR} at commit ${head_sha:0:7}"
fi

artifact_name="kestra-migrate-pr-${PR}"
asset_name="kestra-migrate_pr-${PR}_${os}_${arch}"

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

gh run download "$run_id" --repo "$GITHUB_REPO" --name "$artifact_name" --dir "$tmp_dir" \
  || err "Could not download artifact ${artifact_name} from run ${run_id}. Artifacts expire 14 days after the build; push a commit to rebuild."

[ -f "$tmp_dir/$asset_name" ] || err "Artifact has no binary for ${os}/${arch} (expected ${asset_name})"
[ -f "$tmp_dir/checksums.txt" ] || err "checksums.txt missing from artifact ${artifact_name}"

if command -v sha256sum >/dev/null 2>&1; then
  checksum_cmd="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  checksum_cmd="shasum -a 256"
else
  err "sha256sum or shasum is required for verification"
fi

expected_sum="$(awk -v name="$asset_name" '$2 == name || $2 == "*" name {print $1}' "$tmp_dir/checksums.txt")"
[ -n "$expected_sum" ] || err "Checksum not found for ${asset_name}"
actual_sum="$(cd "$tmp_dir" && $checksum_cmd "$asset_name" | awk '{print $1}')"
[ "$expected_sum" = "$actual_sum" ] || err "Checksum verification failed for ${asset_name}"

if [ -z "$INSTALL_DIR" ]; then
  if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="${HOME}/.local/bin"
  fi
fi
[ -n "$BINARY_NAME" ] || BINARY_NAME="kestra-migrate-pr${PR}"

mkdir -p "$INSTALL_DIR"
install "$tmp_dir/$asset_name" "$INSTALL_DIR/$BINARY_NAME"

printf "%s installed to %s/%s\n" "$BINARY_NAME" "$INSTALL_DIR" "$BINARY_NAME"
printf "built from PR #%s at commit %s (run %s)\n" "$PR" "${head_sha:0:7}" "$run_id"

if [ "$INSTALL_DIR" = "${HOME}/.local/bin" ] && ! command -v "$BINARY_NAME" >/dev/null 2>&1; then
  printf "\nAdd %s to your PATH:\n" "$INSTALL_DIR"
  printf "\tPATH='\$PATH:%s'\n" "$INSTALL_DIR"
fi

printf "\nget started with: %s --help\n" "$BINARY_NAME"
