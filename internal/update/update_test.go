package update

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// isolate points the cache at a temp dir and the release URL at a stub server,
// so no test touches the user's real cache or the network.
func isolate(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	t.Setenv(EnvDisable, "")

	dir := t.TempDir()
	origDir, origURL := cacheDir, releaseURL
	cacheDir = func() (string, error) { return dir, nil }
	if handler != nil {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		releaseURL = srv.URL
	}
	t.Cleanup(func() { cacheDir, releaseURL = origDir, origURL })
}

func serveTag(tag string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	}
}

func TestCheckReportsNewerRelease(t *testing.T) {
	isolate(t, serveTag("2.1.2"))

	notice := Check(context.Background(), "2.0.0")
	if notice == nil {
		t.Fatal("expected a notice for an outdated binary")
	}
	if notice.Current != "2.0.0" || notice.Latest != "2.1.2" {
		t.Fatalf("unexpected notice: %+v", notice)
	}
}

func TestCheckSilentWhenCurrent(t *testing.T) {
	for _, current := range []string{"2.1.2", "2.2.0"} {
		t.Run(current, func(t *testing.T) {
			isolate(t, serveTag("2.1.2"))
			if notice := Check(context.Background(), current); notice != nil {
				t.Fatalf("expected no notice for %s, got %+v", current, notice)
			}
		})
	}
}

func TestCheckSkipsUnreleasedBuilds(t *testing.T) {
	for _, current := range []string{"dev", "pr-12-abc1234", "", "not-a-version"} {
		t.Run(current, func(t *testing.T) {
			isolate(t, func(http.ResponseWriter, *http.Request) {
				t.Error("unreleased build must not hit the network")
			})
			if notice := Check(context.Background(), current); notice != nil {
				t.Fatalf("expected no notice for %q, got %+v", current, notice)
			}
		})
	}
}

func TestCheckDisabledByEnv(t *testing.T) {
	isolate(t, func(http.ResponseWriter, *http.Request) {
		t.Error("check must not hit the network when disabled")
	})
	t.Setenv(EnvDisable, "1")

	if notice := Check(context.Background(), "1.0.0"); notice != nil {
		t.Fatalf("expected no notice when disabled, got %+v", notice)
	}
}

func TestCheckSilentOnServerError(t *testing.T) {
	isolate(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if notice := Check(context.Background(), "1.0.0"); notice != nil {
		t.Fatalf("expected no notice on a failed lookup, got %+v", notice)
	}
}

func TestCheckSilentOnCancelledContext(t *testing.T) {
	isolate(t, serveTag("2.1.2"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if notice := Check(ctx, "1.0.0"); notice != nil {
		t.Fatalf("expected no notice when the lookup is cancelled, got %+v", notice)
	}
}

func TestCheckUsesCacheInsteadOfRefetching(t *testing.T) {
	calls := 0
	isolate(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		serveTag("2.1.2")(w, r)
	})

	for i := 0; i < 3; i++ {
		if notice := Check(context.Background(), "2.0.0"); notice == nil {
			t.Fatalf("call %d: expected a notice", i)
		}
	}
	if calls != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", calls)
	}
}

func TestCheckCachesFailuresToStayOfflineFast(t *testing.T) {
	calls := 0
	isolate(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
	})

	Check(context.Background(), "1.0.0")
	Check(context.Background(), "1.0.0")

	if calls != 1 {
		t.Fatalf("expected the failure to be cached after 1 call, got %d", calls)
	}
}

func TestCheckRefetchesWhenCacheIsStale(t *testing.T) {
	calls := 0
	isolate(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		serveTag("2.1.2")(w, r)
	})

	Check(context.Background(), "2.0.0")

	path, err := cacheFile()
	if err != nil {
		t.Fatal(err)
	}
	stale, err := json.Marshal(cacheEntry{Latest: "2.1.2", CheckedAt: time.Now().Add(-cacheTTL - time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}

	Check(context.Background(), "2.0.0")
	if calls != 2 {
		t.Fatalf("expected a refetch after the TTL expired, got %d calls", calls)
	}
}

func TestCheckSurvivesUnwritableCache(t *testing.T) {
	isolate(t, serveTag("2.1.2"))
	cacheDir = func() (string, error) { return "", errors.New("no cache dir on this platform") }

	// A cache that cannot be written must not stop the notice being produced.
	if notice := Check(context.Background(), "2.0.0"); notice == nil {
		t.Fatal("expected a notice even when the cache is unavailable")
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want semver
		ok   bool
	}{
		{"2.1.2", semver{major: 2, minor: 1, patch: 2}, true},
		{"v2.1.2", semver{major: 2, minor: 1, patch: 2}, true},
		{" 2.1.2\n", semver{major: 2, minor: 1, patch: 2}, true},
		{"1.0.0-alpha.6", semver{major: 1, patch: 0, prerelease: "alpha.6"}, true},
		{"2.1.2+dirty", semver{major: 2, minor: 1, patch: 2}, true},
		{"dev", semver{}, false},
		{"pr-12-abc1234", semver{}, false},
		{"2.1", semver{}, false},
		{"2.1.x", semver{}, false},
		{"", semver{}, false},
	}
	for _, tt := range tests {
		got, ok := parse(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parse(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSemverLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"2.0.0", "2.1.2", true},
		{"2.1.2", "2.0.0", false},
		{"2.1.2", "2.1.2", false},
		{"1.9.9", "2.0.0", true},
		{"2.1.1", "2.1.2", true},
		{"10.0.0", "9.0.0", false},
		{"2.9.0", "2.10.0", true},
		{"1.0.0-alpha.6", "1.0.0", true},
		{"1.0.0", "1.0.0-alpha.6", false},
		{"1.0.0-alpha.2", "1.0.0-alpha.6", true},
		{"1.0.0-alpha.6", "2.0.0", true},
	}
	for _, tt := range tests {
		a, okA := parse(tt.a)
		b, okB := parse(tt.b)
		if !okA || !okB {
			t.Fatalf("bad fixture: %q / %q", tt.a, tt.b)
		}
		if got := a.less(b); got != tt.want {
			t.Errorf("%q.less(%q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
