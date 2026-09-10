// Package update tells the user when the binary they are running is behind
// the latest published release.
//
// A migration is a one-shot, high-stakes operation: running last month's
// binary means missing rules that were added since, and the resulting flows
// look fine until they fail on deploy. The check is therefore on by default —
// but it is strictly advisory and can never break a run: every failure path
// (no network, GitHub down, malformed JSON, unwritable cache) returns "no
// notice" and the migration proceeds untouched.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvDisable turns the check off entirely. Set it in CI, or in an
	// air-gapped environment where the outbound call would just time out.
	EnvDisable = "KESTRA_MIGRATE_NO_UPDATE_CHECK"

	// cacheTTL is how long a result (success *or* failure) is reused. Failures
	// are cached too, otherwise an offline user pays the HTTP timeout on every
	// single invocation.
	cacheTTL = 24 * time.Hour

	cacheDirName  = "kestra-migrate"
	cacheFileName = "update-check.json"
)

// Overridable in tests.
var (
	releaseURL = "https://api.github.com/repos/kestra-io/kestra2-flow-migration/releases/latest"
	cacheDir   = defaultCacheDir
)

// Notice reports that a newer release exists.
type Notice struct {
	Current string
	Latest  string
}

func (n Notice) String() string {
	return fmt.Sprintf("you are running kestra-migrate %s, but %s is available.\n"+
		"   Migration rules are added continuously — an outdated binary silently skips them.\n"+
		"   Update: curl -fsSL https://raw.githubusercontent.com/kestra-io/kestra2-flow-migration/main/install-scripts/install.sh | bash",
		n.Current, n.Latest)
}

// Check returns a Notice when current is an older release than the latest one
// published on GitHub, and nil in every other case — including every error.
func Check(ctx context.Context, current string) *Notice {
	if os.Getenv(EnvDisable) != "" {
		return nil
	}
	cur, ok := parse(current)
	if !ok {
		// "dev", "pr-42-abc1234" and anything else unreleased: there is
		// nothing meaningful to compare against.
		return nil
	}

	latest, ok := cachedLatest()
	if !ok {
		latest = fetchLatest(ctx)
		writeCache(latest)
	}
	if latest == "" {
		return nil
	}

	lat, ok := parse(latest)
	if !ok || !cur.less(lat) {
		return nil
	}
	return &Notice{Current: current, Latest: latest}
}

// fetchLatest returns the latest release tag, or "" on any failure.
func fetchLatest(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ""
	}
	return strings.TrimSpace(release.TagName)
}

type cacheEntry struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checkedAt"`
}

func cacheFile() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheDirName, cacheFileName), nil
}

func defaultCacheDir() (string, error) { return os.UserCacheDir() }

// cachedLatest returns the cached tag and whether the cache is still fresh. A
// fresh entry with an empty tag is a cached *failure*: honoured, so a repeated
// run stays offline-fast.
func cachedLatest() (string, bool) {
	path, err := cacheFile()
	if err != nil {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var entry cacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", false
	}
	if time.Since(entry.CheckedAt) > cacheTTL {
		return "", false
	}
	return entry.Latest, true
}

func writeCache(latest string) {
	path, err := cacheFile()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, err := json.Marshal(cacheEntry{Latest: latest, CheckedAt: time.Now()})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o644)
}

// semver is the subset of semantic versioning this repo's tags use:
// "2.1.2" and "1.0.0-alpha.6". Tags carry no "v" prefix (.releaserc.json sets
// tagFormat "${version}"), but one is tolerated in case that ever changes.
type semver struct {
	major, minor, patch int
	prerelease          string
}

func parse(s string) (semver, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	// Release builds stamp the bare tag; strip anything a local build appended.
	if i := strings.IndexAny(s, "+ "); i >= 0 {
		s = s[:i]
	}
	core, pre, _ := strings.Cut(s, "-")

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], prerelease: pre}, true
}

// less reports whether v is older than other. Prerelease ordering follows
// semver only as far as this repo needs: a prerelease is older than its own
// release, and two prereleases of the same core compare lexically.
func (v semver) less(other semver) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	if v.patch != other.patch {
		return v.patch < other.patch
	}
	if v.prerelease == other.prerelease {
		return false
	}
	if v.prerelease == "" {
		return false // a release is never older than its own prerelease
	}
	if other.prerelease == "" {
		return true
	}
	return v.prerelease < other.prerelease
}
