// Package report renders the end-of-run warning summary.
//
// It exists because the per-flow output is dominated by repetition: on a
// 400-flow corpus, 96 warning lines carry only 5 distinct problems, and on a
// customer estate 107 advisories of three types buried the single real error in
// the run. The failure mode is not verbosity — it is a genuine error becoming
// statistically invisible. Grouping by family restores the shape of the run and
// puts a one-off finding on its own line, where it can be seen.
//
// Presentation lives here rather than in internal/migrate so the migration
// rules stay free of output concerns, and so the layout is unit-testable.
package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kestra-io/kestra2-flow-migration/internal/migrate"
)

// Entry is one flow's warnings.
type Entry struct {
	Flow     string
	Warnings []migrate.Warning
}

// family is one grouped row of the summary.
type family struct {
	code           migrate.Code
	label          string
	docURL         string
	v2Incompatible bool
	count          int
	// subjects counts occurrences per Warning.Subject, rendering the
	// per-cause breakdown under a family that spans several constructs.
	subjects map[string]int
	// flows is the number of distinct flows the family touches.
	flows map[string]bool
}

// Summarize renders the grouped summary block, or "" when there is nothing
// worth summarising: no warnings at all, or a single flow, where the block
// would only restate the lines directly above it.
func Summarize(entries []Entry) string {
	if len(entries) < 2 {
		return ""
	}

	families := map[migrate.Code]*family{}
	var total, blocking, advisory int
	affected := map[string]bool{}

	for _, e := range entries {
		for _, w := range e.Warnings {
			total++
			if w.V2Incompatible {
				blocking++
			} else {
				advisory++
			}
			affected[e.Flow] = true

			f, ok := families[w.Code]
			if !ok {
				f = &family{
					code:           w.Code,
					label:          w.Code.Label(),
					docURL:         w.DocURL,
					v2Incompatible: w.V2Incompatible,
					subjects:       map[string]int{},
					flows:          map[string]bool{},
				}
				families[w.Code] = f
			}
			f.count++
			f.flows[e.Flow] = true
			if w.Subject != "" {
				f.subjects[shortenType(w.Subject)]++
			}
		}
	}
	if total == 0 {
		return ""
	}

	// Deliberately no leading ✔/⚠/✎/✗ marker on any line of this block, and no
	// "two spaces then a marker" either: those are the shapes of the per-flow
	// status and warning lines, which the QA pipeline and users grep for. The
	// count leads each family row so a summary row can never be mistaken for an
	// occurrence.
	var b strings.Builder
	fmt.Fprintf(&b, "\n\033[1;33mSummary: %s across %d %s — %d blocking, %d advisory\033[0m\n",
		plural(total, "warning"), len(affected), noun(len(affected), "flow"), blocking, advisory)

	if rows := sorted(families, true); len(rows) > 0 {
		b.WriteString("\n\033[1m  BLOCKING\033[0m \033[2m(Kestra 2.0 rejects the flow)\033[0m\n")
		writeRows(&b, rows, "\033[31m✗\033[0m")
	}
	if rows := sorted(families, false); len(rows) > 0 {
		b.WriteString("\n\033[1m  ADVISORY\033[0m \033[2m(deploys, breaks at run time)\033[0m\n")
		writeRows(&b, rows, "\033[33m⚠\033[0m")
	}
	return b.String()
}

// sorted returns the families of one severity, most frequent first. Ties break
// on the label so the report is stable across runs.
func sorted(families map[migrate.Code]*family, v2Incompatible bool) []*family {
	var rows []*family
	for _, f := range families {
		if f.v2Incompatible == v2Incompatible {
			rows = append(rows, f)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].label < rows[j].label
	})
	return rows
}

// width is the column the doc links line up on. Long labels simply push their
// own link right rather than wrapping.
const width = 52

func writeRows(b *strings.Builder, rows []*family, marker string) {
	for _, f := range rows {
		label := f.label
		pad := width - len(label)
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(b, "    %3d× %s  %s%s\033[2m→ %s\033[0m\n",
			f.count, marker, label, strings.Repeat(" ", pad), f.docURL)

		// Only worth a breakdown when the family actually spans several
		// causes; a single-subject family already says it in the label.
		if len(f.subjects) > 1 {
			fmt.Fprintf(b, "          \033[2m%s\033[0m\n", breakdown(f.subjects))
		}
		if n := len(f.flows); n > 1 && n != f.count {
			fmt.Fprintf(b, "          \033[2min %d flows\033[0m\n", n)
		}
	}
}

// breakdown renders "3 git.PushFlows, 2 kestra.logs.Fetch", most frequent
// first, capping the tail so one pathological family cannot flood the summary.
func breakdown(subjects map[string]int) string {
	type kv struct {
		name  string
		count int
	}
	var all []kv
	for name, count := range subjects {
		all = append(all, kv{name, count})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].count != all[j].count {
			return all[i].count > all[j].count
		}
		return all[i].name < all[j].name
	})

	const max = 4
	var parts []string
	for i, e := range all {
		if i == max {
			parts = append(parts, fmt.Sprintf("+%d more", len(all)-max))
			break
		}
		parts = append(parts, fmt.Sprintf("%d %s", e.count, e.name))
	}
	return strings.Join(parts, ", ")
}

// shortenType drops the shared plugin prefix so a breakdown reads
// "git.PushFlows" rather than the full FQN.
func shortenType(subject string) string {
	return strings.TrimPrefix(subject, "io.kestra.plugin.")
}

func plural(n int, word string) string {
	return fmt.Sprintf("%d %s", n, noun(n, word))
}

func noun(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
