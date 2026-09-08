package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kestra-io/kestra2-flow-migration/internal/migrate"
)

func warn(code migrate.Code, url string) migrate.Warning {
	return migrate.Warning{Code: code, DocURL: url}
}

// With the summary on, a family's link is printed once and the end-of-run block
// repeats it — 96 doc lines collapse to 5 on the corpus.
func TestDocLinks_DedupesPerFamilyWhenSummarised(t *testing.T) {
	var b bytes.Buffer
	links := newDocLinks(true)
	for i := 0; i < 3; i++ {
		links.print(&b, "   ", warn("removed-type", "https://example/foreach"))
	}
	links.print(&b, "   ", warn("sdk-auth", "https://example/auth"))

	if got := strings.Count(b.String(), "https://example/foreach"); got != 1 {
		t.Fatalf("family link printed %d times, want 1:\n%s", got, b.String())
	}
	if got := strings.Count(b.String(), "https://example/auth"); got != 1 {
		t.Fatalf("second family link printed %d times, want 1", got)
	}
}

// Without the summary there is no end-of-run block to carry the link, so
// deduping would strip it from every occurrence but the first. This is what
// keeps --summary=false byte-identical to the pre-summary output.
func TestDocLinks_NoDedupeWhenSummaryDisabled(t *testing.T) {
	var b bytes.Buffer
	links := newDocLinks(false)
	for i := 0; i < 3; i++ {
		links.print(&b, "   ", warn("removed-type", "https://example/foreach"))
	}

	if got := strings.Count(b.String(), "https://example/foreach"); got != 3 {
		t.Fatalf("link printed %d times, want 3:\n%s", got, b.String())
	}
}

// An unclassified warning cannot be collapsed onto another family's link.
func TestDocLinks_EmptyCodeNeverDeduped(t *testing.T) {
	var b bytes.Buffer
	links := newDocLinks(true)
	for i := 0; i < 2; i++ {
		links.print(&b, "   ", warn("", "https://example/guide"))
	}

	if got := strings.Count(b.String(), "https://example/guide"); got != 2 {
		t.Fatalf("unclassified link printed %d times, want 2", got)
	}
}

func TestDocLinks_NoURLPrintsNothing(t *testing.T) {
	var b bytes.Buffer
	newDocLinks(true).print(&b, "   ", warn("removed-type", ""))
	if b.Len() != 0 {
		t.Fatalf("printed %q, want nothing", b.String())
	}
}
