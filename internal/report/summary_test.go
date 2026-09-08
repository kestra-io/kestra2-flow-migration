package report

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kestra-io/kestra2-flow-migration/internal/migrate"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain renders the summary without colour, as a grep would see it.
func plain(entries []Entry) string {
	return ansi.ReplaceAllString(Summarize(entries), "")
}

func blocking(code migrate.Code, subject, doc string) migrate.Warning {
	return migrate.Warning{Message: "m", V2Incompatible: true, DocURL: doc, Code: code, Subject: subject}
}

func advisoryW(code migrate.Code, subject, doc string) migrate.Warning {
	return migrate.Warning{Message: "m", DocURL: doc, Code: code, Subject: subject}
}

func TestSummarize_GroupsByFamilyNotByMessage(t *testing.T) {
	// The ForEach message embeds the task id, so the rendered text differs on
	// every occurrence — grouping must key off Code, not the message.
	entries := []Entry{
		{Flow: "a.yaml", Warnings: []migrate.Warning{
			{Message: "for_each uses ForEach (…)", V2Incompatible: true, Code: migrate.CodeForEachLoop, Subject: "io.kestra.plugin.core.flow.ForEach", DocURL: "u/foreach"},
		}},
		{Flow: "b.yaml", Warnings: []migrate.Warning{
			{Message: "each uses ForEach (…)", V2Incompatible: true, Code: migrate.CodeForEachLoop, Subject: "io.kestra.plugin.core.flow.ForEach", DocURL: "u/foreach"},
		}},
		{Flow: "c.yaml", Warnings: []migrate.Warning{
			{Message: "parallel uses ForEach (…)", V2Incompatible: true, Code: migrate.CodeForEachLoop, Subject: "io.kestra.plugin.core.flow.ForEach", DocURL: "u/foreach"},
		}},
	}
	out := plain(entries)
	if !strings.Contains(out, "3× ✗  ForEach removed, rewrite as Loop") {
		t.Errorf("three differently-worded ForEach warnings must collapse to one row of 3, got:\n%s", out)
	}
	if strings.Count(out, "ForEach removed") != 1 {
		t.Errorf("expected exactly one ForEach row, got:\n%s", out)
	}
}

func TestSummarize_SeparatesSeveritiesAndOrdersByCount(t *testing.T) {
	entries := []Entry{
		{Flow: "a.yaml", Warnings: []migrate.Warning{
			advisoryW(migrate.CodeSdkAuthAdvisory, "io.kestra.plugin.git.PushFlows", "u/sdk"),
			blocking(migrate.CodePluginDefaults, "", "u/pd"),
			blocking(migrate.CodePluginDefaults, "", "u/pd"),
			blocking(migrate.CodeMissingTriggerInput, "", "u/mg"),
		}},
		{Flow: "b.yaml", Warnings: []migrate.Warning{
			blocking(migrate.CodePluginDefaults, "", "u/pd"),
		}},
	}
	out := plain(entries)
	if !strings.Contains(out, "Summary: 5 warnings across 2 flows — 4 blocking, 1 advisory") {
		t.Errorf("header should count warnings, flows and severities, got:\n%s", out)
	}
	bi, ai := strings.Index(out, "BLOCKING"), strings.Index(out, "ADVISORY")
	if bi < 0 || ai < 0 || bi > ai {
		t.Errorf("expected BLOCKING before ADVISORY, got:\n%s", out)
	}
	pd, mg := strings.Index(out, "pluginDefaults"), strings.Index(out, "trigger missing")
	if pd > mg {
		t.Errorf("families must be ordered by count descending, got:\n%s", out)
	}
}

func TestSummarize_SubjectBreakdownOnlyWhenFamilySpansCauses(t *testing.T) {
	mixed := []Entry{
		{Flow: "a.yaml", Warnings: []migrate.Warning{
			advisoryW(migrate.CodeSdkAuthAdvisory, "io.kestra.plugin.git.PushFlows", "u/sdk"),
			advisoryW(migrate.CodeSdkAuthAdvisory, "io.kestra.plugin.git.PushFlows", "u/sdk"),
			advisoryW(migrate.CodeSdkAuthAdvisory, "io.kestra.plugin.kestra.logs.Fetch", "u/sdk"),
		}},
		{Flow: "b.yaml", Warnings: []migrate.Warning{
			advisoryW(migrate.CodeSdkAuthAdvisory, "io.kestra.plugin.git.SyncFlows", "u/sdk"),
		}},
	}
	out := plain(mixed)
	// Most frequent first, FQNs shortened.
	if !strings.Contains(out, "2 git.PushFlows, 1 git.SyncFlows, 1 kestra.logs.Fetch") {
		t.Errorf("expected a per-subject breakdown, got:\n%s", out)
	}

	single := []Entry{
		{Flow: "a.yaml", Warnings: []migrate.Warning{blocking(migrate.CodePluginDefaults, "", "u/pd")}},
		{Flow: "b.yaml", Warnings: []migrate.Warning{blocking(migrate.CodePluginDefaults, "", "u/pd")}},
	}
	if strings.Contains(plain(single), "2 ") && strings.Count(plain(single), "pluginDefaults") > 1 {
		t.Errorf("a single-cause family needs no breakdown line, got:\n%s", plain(single))
	}
}

// The block must not reuse the line shapes the QA pipeline and users grep for:
// a per-flow status line (`^(✔|⚠|✎|✗) `) or a per-flow warning line
// (`^  (✗|⚠) `). Getting this wrong silently double-counts every warning.
func TestSummarize_DoesNotCollideWithPerFlowLineShapes(t *testing.T) {
	entries := []Entry{
		{Flow: "a.yaml", Warnings: []migrate.Warning{blocking(migrate.CodeForEachLoop, "t", "u/foreach")}},
		{Flow: "b.yaml", Warnings: []migrate.Warning{advisoryW(migrate.CodeSdkAuthAdvisory, "t", "u/sdk")}},
	}
	status := regexp.MustCompile(`(?m)^(✔|⚠|✎|✗) `)
	warning := regexp.MustCompile(`(?m)^  (✗|⚠) `)
	out := plain(entries)
	if m := status.FindAllString(out, -1); len(m) > 0 {
		t.Errorf("summary must not emit per-flow status line shapes, got %q in:\n%s", m, out)
	}
	if m := warning.FindAllString(out, -1); len(m) > 0 {
		t.Errorf("summary must not emit per-flow warning line shapes, got %q in:\n%s", m, out)
	}
}

func TestSummarize_SuppressedForSingleFlowOrNoWarnings(t *testing.T) {
	one := []Entry{{Flow: "a.yaml", Warnings: []migrate.Warning{blocking(migrate.CodeForEachLoop, "t", "u")}}}
	if got := Summarize(one); got != "" {
		t.Errorf("a single flow needs no summary, got:\n%s", got)
	}
	clean := []Entry{{Flow: "a.yaml"}, {Flow: "b.yaml"}}
	if got := Summarize(clean); got != "" {
		t.Errorf("no warnings means no summary, got:\n%s", got)
	}
}

func TestSummarize_UnlabelledCodeStillRenders(t *testing.T) {
	entries := []Entry{
		{Flow: "a.yaml", Warnings: []migrate.Warning{blocking(migrate.Code("brand-new"), "", "u")}},
		{Flow: "b.yaml", Warnings: []migrate.Warning{blocking(migrate.Code("brand-new"), "", "u")}},
	}
	if out := plain(entries); !strings.Contains(out, "brand-new") {
		t.Errorf("a code with no label should fall back to the raw code, got:\n%s", out)
	}
}
