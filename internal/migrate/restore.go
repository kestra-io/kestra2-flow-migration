package migrate

import (
	"sort"

	"gopkg.in/yaml.v3"
)

// restoreUnchangedBlocks reduces yaml.v3 round-trip noise by replacing byte
// ranges in the migrated output with the corresponding original bytes for any
// subtree that is semantically unchanged. This preserves block-scalar styles
// (| and >), non-ASCII characters, compact sequence indentation, and other
// formatting that yaml.v3 remaps on round-trip.
//
// The walk pairs mapping entries by key name and sequence items by index. For
// each matched pair/item whose value is semantically equal, the migrated bytes
// are replaced with the original bytes at line-start granularity — so leading
// indent, `- ` prefixes, and any comments between entries are preserved from
// the original. Subtrees whose value changed are recursed into so nested
// unchanged entries are still restored.
func restoreUnchangedBlocks(original, migrated []byte) []byte {
	var oDoc, mDoc yaml.Node
	if err := yaml.Unmarshal(original, &oDoc); err != nil {
		return migrated
	}
	if err := yaml.Unmarshal(migrated, &mDoc); err != nil {
		return migrated
	}
	oLines := buildLineIndex(original)
	mLines := buildLineIndex(migrated)

	type edit struct {
		start, end int
		replace    []byte
	}
	var edits []edit

	var walk func(o, m *yaml.Node, oEnd, mEnd int)
	walk = func(o, m *yaml.Node, oEnd, mEnd int) {
		if o == nil || m == nil || o.Kind != m.Kind {
			return
		}
		if o.Style == yaml.FlowStyle || m.Style == yaml.FlowStyle {
			return
		}
		switch o.Kind {
		case yaml.MappingNode:
			oIdx := make(map[string]int, len(o.Content)/2)
			for i := 0; i+1 < len(o.Content); i += 2 {
				if k := o.Content[i]; k.Kind == yaml.ScalarNode {
					oIdx[k.Value] = i
				}
			}
			for j := 0; j+1 < len(m.Content); j += 2 {
				mKey := m.Content[j]
				if mKey.Kind != yaml.ScalarNode {
					continue
				}
				mVal := m.Content[j+1]
				mPairStart := lineStart(mKey, mLines)
				var mPairEnd int
				if j+2 < len(m.Content) {
					mPairEnd = lineStart(m.Content[j+2], mLines)
				} else {
					mPairEnd = mEnd
				}
				oi, ok := oIdx[mKey.Value]
				if !ok {
					continue
				}
				oKey := o.Content[oi]
				oVal := o.Content[oi+1]
				oPairStart := lineStart(oKey, oLines)
				var oPairEnd int
				if oi+2 < len(o.Content) {
					oPairEnd = lineStart(o.Content[oi+2], oLines)
				} else {
					oPairEnd = oEnd
				}
				if mPairStart < 0 || oPairStart < 0 {
					continue
				}
				// Don't restore from the original when the `- ` block-sequence
				// marker presence differs between the two lines: the migrated
				// bytes carry the correct marker (e.g. a rule removed the first
				// key of a list-item mapping, which had held the marker), so
				// copying the original — where this key was not first and had no
				// marker — would drop it and corrupt the sequence. Recurse
				// instead, leaving the structurally-correct migrated line.
				markersMatch := lineHasSeqMarker(migrated, mPairStart, mKey) == lineHasSeqMarker(original, oPairStart, oKey)
				if semEq(oVal, mVal) && markersMatch {
					oPairEndT := trimTrailingBlankLines(original, oPairStart, oPairEnd)
					mPairEndT := trimTrailingBlankLines(migrated, mPairStart, mPairEnd)
					edits = append(edits, edit{
						start:   mPairStart,
						end:     mPairEndT,
						replace: original[oPairStart:oPairEndT],
					})
				} else {
					walk(oVal, mVal, oPairEnd, mPairEnd)
				}
			}
		case yaml.SequenceNode:
			if len(o.Content) != len(m.Content) {
				return
			}
			for i := 0; i < len(o.Content); i++ {
				oItem := o.Content[i]
				mItem := m.Content[i]
				mItemStart := lineStart(mItem, mLines)
				oItemStart := lineStart(oItem, oLines)
				if mItemStart < 0 || oItemStart < 0 {
					continue
				}
				var mItemEnd, oItemEnd int
				if i+1 < len(m.Content) {
					mItemEnd = lineStart(m.Content[i+1], mLines)
				} else {
					mItemEnd = mEnd
				}
				if i+1 < len(o.Content) {
					oItemEnd = lineStart(o.Content[i+1], oLines)
				} else {
					oItemEnd = oEnd
				}
				if semEq(oItem, mItem) {
					oItemEndT := trimTrailingBlankLines(original, oItemStart, oItemEnd)
					mItemEndT := trimTrailingBlankLines(migrated, mItemStart, mItemEnd)
					edits = append(edits, edit{
						start:   mItemStart,
						end:     mItemEndT,
						replace: original[oItemStart:oItemEndT],
					})
				} else {
					walk(oItem, mItem, oItemEnd, mItemEnd)
				}
			}
		}
	}

	var oRoot, mRoot *yaml.Node
	if len(oDoc.Content) > 0 {
		oRoot = oDoc.Content[0]
	}
	if len(mDoc.Content) > 0 {
		mRoot = mDoc.Content[0]
	}
	walk(oRoot, mRoot, len(original), len(migrated))

	if len(edits) == 0 {
		return migrated
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	result := migrated
	for _, e := range edits {
		if e.start < 0 || e.end > len(result) || e.start > e.end {
			continue
		}
		buf := make([]byte, 0, len(result)-(e.end-e.start)+len(e.replace))
		buf = append(buf, result[:e.start]...)
		buf = append(buf, e.replace...)
		buf = append(buf, result[e.end:]...)
		result = buf
	}
	return result
}

// semEq tests two yaml.Nodes for semantic equality, ignoring style and
// comments. Used by restoreUnchangedBlocks to decide whether a subtree's byte
// range can be restored from the original document verbatim.
func semEq(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case yaml.ScalarNode:
		return a.Value == b.Value && a.Tag == b.Tag
	case yaml.SequenceNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := range a.Content {
			if !semEq(a.Content[i], b.Content[i]) {
				return false
			}
		}
		return true
	case yaml.MappingNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		aIdx := make(map[string]*yaml.Node, len(a.Content)/2)
		for i := 0; i+1 < len(a.Content); i += 2 {
			if k := a.Content[i]; k.Kind == yaml.ScalarNode {
				aIdx[k.Value] = a.Content[i+1]
			}
		}
		for j := 0; j+1 < len(b.Content); j += 2 {
			bk := b.Content[j]
			if bk.Kind != yaml.ScalarNode {
				return false
			}
			av, ok := aIdx[bk.Value]
			if !ok {
				return false
			}
			if !semEq(av, b.Content[j+1]) {
				return false
			}
		}
		return true
	case yaml.AliasNode:
		return semEq(a.Alias, b.Alias)
	}
	return false
}

func buildLineIndex(b []byte) []int {
	offsets := []int{0}
	for i, c := range b {
		if c == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

func lineStart(n *yaml.Node, lineIdx []int) int {
	if n == nil || n.Line <= 0 || n.Line > len(lineIdx) {
		return -1
	}
	return lineIdx[n.Line-1]
}

// lineHasSeqMarker reports whether the text from the start of node n's line
// (byte offset lineStartOff) up to n's column contains a YAML block-sequence
// marker ('-'). The indent before a key only ever contains spaces or a single
// `- ` item marker, so a dash in that span unambiguously means n is the first
// key of a block-sequence item.
func lineHasSeqMarker(content []byte, lineStartOff int, n *yaml.Node) bool {
	if lineStartOff < 0 || n == nil || n.Column <= 1 {
		return false
	}
	end := lineStartOff + n.Column - 1
	if end > len(content) {
		end = len(content)
	}
	for i := lineStartOff; i < end; i++ {
		if content[i] == '-' {
			return true
		}
	}
	return false
}

// trimTrailingBlankLines returns a new end offset with trailing whitespace-only
// lines excluded from [start, end). Used so that a restored key-value pair
// doesn't drag trailing blank lines — which logically belong to the parent
// mapping's inter-sibling whitespace — into the replacement, introducing blank
// lines that weren't in migrated's content.
func trimTrailingBlankLines(content []byte, start, end int) int {
	for end > start {
		lineEnd := end
		if lineEnd-1 < start || content[lineEnd-1] != '\n' {
			break
		}
		lineStart := lineEnd - 1
		for lineStart > start && content[lineStart-1] != '\n' {
			lineStart--
		}
		blank := true
		for i := lineStart; i < lineEnd-1; i++ {
			if content[i] != ' ' && content[i] != '\t' {
				blank = false
				break
			}
		}
		if !blank {
			break
		}
		end = lineStart
	}
	return end
}
