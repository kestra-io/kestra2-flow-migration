package migrate

import (
	"strings"
	"testing"
)

// kestra-ee#11385: the reproducer from the issue. read() on an ION URI returns
// byte[] on 2.0, so `contains` on it no longer matches.
func TestApply_DetectIonRead_Issue11385(t *testing.T) {
	in := `id: ion_read
namespace: company.team

tasks:
  - id: csv
    type: io.kestra.plugin.core.storage.Write
    extension: .csv
    content: |
      name
      jane

  - id: to_ion
    type: io.kestra.plugin.serdes.csv.CsvToIon
    from: "{{ outputs.csv.uri }}"

  - id: assert
    type: io.kestra.plugin.core.execution.Assert
    conditions:
      - "{{ read(outputs.to_ion.uri) contains 'jane' }}"
`
	out, warnings := applyWithWarningDetails(t, in)
	if out != in {
		t.Errorf("detector must not rewrite the flow, got:\n%s", out)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %+v", warnings)
	}
	w := warnings[0]
	if w.V2Incompatible {
		t.Error("the flow saves on 2.0 and misbehaves at run time: the warning must be advisory")
	}
	if w.Code != CodeIonRead || w.DocURL != docIonBinaryFormat {
		t.Errorf("Code/DocURL = %q/%q, want %q/%q", w.Code, w.DocURL, CodeIonRead, docIonBinaryFormat)
	}
	for _, s := range []string{"line 19", "to_ion", "fromIon("} {
		if !strings.Contains(w.Message, s) {
			t.Errorf("message %q does not mention %q", w.Message, s)
		}
	}
}

func TestApply_DetectIonRead(t *testing.T) {
	cases := []struct {
		name  string
		tasks string
		want  int
	}{
		{"serdes ToIon", `
  - id: t
    type: io.kestra.plugin.serdes.json.JsonToIon
    from: x
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.t.uri) }}"`, 1},
		{"bracket access", `
  - id: to-ion
    type: io.kestra.plugin.serdes.csv.CsvToIon
    from: x
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs['to-ion'].uri) }}"`, 1},
		{"query stored as ION", `
  - id: q
    type: io.kestra.plugin.jdbc.postgresql.Query
    sql: select 1
    fetchType: STORE
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.q.uri) }}"`, 1},
		{"query with store: true", `
  - id: q
    type: io.kestra.plugin.jdbc.postgresql.Query
    sql: select 1
    store: true
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.q.uri) }}"`, 1},
		{"Write with .ion extension", `
  - id: w
    type: io.kestra.plugin.core.storage.Write
    extension: .ion
    content: x
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.w.uri) }}"`, 1},
		{"FileTransform", `
  - id: ft
    type: io.kestra.plugin.graalvm.js.FileTransform
    from: x
    script: row
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.ft.uri) }}"`, 1},
		{"one warning per read", `
  - id: t
    type: io.kestra.plugin.serdes.json.JsonToIon
    from: x
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.t.uri) }} / {{ read(outputs.t.uri) | length }}"`, 2},

		{"already wrapped", `
  - id: t
    type: io.kestra.plugin.serdes.json.JsonToIon
    from: x
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ fromIon(read(outputs.t.uri), allRows=true) }}"`, 0},
		{"non-ION producer", `
  - id: csv
    type: io.kestra.plugin.core.storage.Write
    extension: .csv
    content: x
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.csv.uri) }}"`, 0},
		{"ION URI passed on, not read", `
  - id: t
    type: io.kestra.plugin.serdes.json.JsonToIon
    from: x
  - id: back
    type: io.kestra.plugin.serdes.json.IonToJson
    from: "{{ outputs.t.uri }}"`, 0},
		{"query fetched in memory", `
  - id: q
    type: io.kestra.plugin.jdbc.postgresql.Query
    sql: select 1
    fetchType: FETCH
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ outputs.q.rows }}"`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := "id: f\nnamespace: n\ntasks:" + c.tasks + "\n"
			_, warnings := applyWithWarningDetails(t, in)
			got := 0
			for _, w := range warnings {
				if w.Code == CodeIonRead {
					got++
				}
			}
			if got != c.want {
				t.Errorf("got %d ION-read warnings, want %d: %+v", got, c.want, warnings)
			}
		})
	}
}

// fromIon() is not needed on v1.3, where ION files are text, and the
// detector's advice targets 2.0: v2 path only, like the other detectors.
func TestApply_DetectIonRead_SkippedUnderStayV1Compatible(t *testing.T) {
	in := `id: f
namespace: n
tasks:
  - id: t
    type: io.kestra.plugin.serdes.json.JsonToIon
    from: x
  - id: log
    type: io.kestra.plugin.core.log.Log
    message: "{{ read(outputs.t.uri) }}"
`
	_, warnings := applyWithWarningDetails(t, in, StayV1Compatible())
	for _, w := range warnings {
		if w.Code == CodeIonRead {
			t.Errorf("unexpected ION-read warning under --stay-v1-compatible: %+v", w)
		}
	}
}
