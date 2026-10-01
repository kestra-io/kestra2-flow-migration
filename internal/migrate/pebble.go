package migrate

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// renamePebbleJSON rewrites the Pebble `json` filter → `toJson` and the
// `json()` function → `fromJson()`. Both were removed in v2 (only
// ToJsonFilter / FromJsonFunction remain on releases/v2.0.x). On v1.3 they are
// pure aliases — `JsonFilter extends ToJsonFilter`, `JsonFunction extends
// FromJsonFunction`, no override logic — so the rewrite is behavior-identical
// on both versions and runs under StayV1Compatible too.
//
// Unlike detectPebbleVersionArg this rewrites instead of warning, because the
// change is a pure rename. The risk of touching embedded script code is
// contained by rewritePebbleJSON: only text inside Pebble delimiters is
// considered, never quoted string literals within them, and raw/verbatim
// regions are skipped. The `json` Pebble *test* (`x is json`) still exists in
// v2 and is left alone.
// (flows-changes.md: Pebble json filter and function removed)
func renamePebbleJSON(doc *yaml.Node) error {
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode {
			if strings.Contains(n.Value, "json") {
				n.Value = rewritePebbleJSON(n.Value)
			}
			return
		}
		for i, c := range n.Content {
			// Mapping keys are not rendered as Pebble.
			if n.Kind == yaml.MappingNode && i%2 == 0 {
				continue
			}
			walk(c)
		}
	}
	walk(doc)
	return nil
}

var (
	// pebbleJSONFilter matches the `json` filter: `| json`, `|json`. `\b`
	// keeps `| jsonPath`-style names and `| json_x` out.
	pebbleJSONFilter = regexp.MustCompile(`(\|\s*)json\b`)
	// pebbleJSONFunction matches a `json(` call. `\b` keeps longer names
	// (`my_json(`, `fromJson(`) out; member calls (`x.json(`) are excluded by
	// renameJSONCalls, since a leading class would consume the `(` a nested
	// `json(json(x))` needs.
	pebbleJSONFunction = regexp.MustCompile(`\bjson\s*\(`)
	// pebbleIsTestTail matches text ending in the `is` / `is not` test
	// operator, so `x is json` is never taken for a function call.
	pebbleIsTestTail = regexp.MustCompile(`\bis\s+(?:not\s+)?$`)
	// pebbleRawTag matches the opening tag of a region Pebble does not parse.
	pebbleRawTag = regexp.MustCompile(`^-?\s*(raw|verbatim)\s*-?$`)
	// pebbleRawEnd matches the tag closing each of those regions.
	pebbleRawEnd = map[string]*regexp.Regexp{
		"raw":      regexp.MustCompile(`\{%-?\s*endraw\s*-?%\}`),
		"verbatim": regexp.MustCompile(`\{%-?\s*endverbatim\s*-?%\}`),
	}
)

// rewritePebbleJSON applies the json → toJson / fromJson rename to every
// Pebble expression and tag in s (see rewritePebbleBodies).
func rewritePebbleJSON(s string) string {
	return rewritePebbleBodies(s, rewritePebbleJSONCode)
}

// rewritePebbleBodies applies fn to the body of every Pebble expression
// (`{{ … }}`) and tag (`{% … %}`) in s. Text outside delimiters, comments
// (`{# … #}`) and `{% raw %}` / `{% verbatim %}` regions are copied
// unchanged, as is anything after an unterminated delimiter.
func rewritePebbleBodies(s string, fn func(body string) string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		open := strings.IndexByte(s[i:], '{')
		if open < 0 || i+open+1 >= len(s) {
			break
		}
		start := i + open
		kind := s[start+1]
		if kind != '{' && kind != '%' && kind != '#' {
			b.WriteString(s[i : start+1])
			i = start + 1
			continue
		}
		b.WriteString(s[i:start])
		if kind == '#' {
			end := strings.Index(s[start+2:], "#}")
			if end < 0 {
				i = start
				break
			}
			end += start + 2 + 2
			b.WriteString(s[start:end])
			i = end
			continue
		}
		closing := "}}"
		if kind == '%' {
			closing = "%}"
		}
		bodyEnd := findPebbleClose(s, start+2, closing)
		if bodyEnd < 0 {
			i = start
			break
		}
		body := s[start+2 : bodyEnd]
		end := bodyEnd + len(closing)
		if kind == '%' {
			if m := pebbleRawTag.FindStringSubmatch(strings.TrimSpace(body)); m != nil {
				loc := pebbleRawEnd[m[1]].FindStringIndex(s[end:])
				if loc == nil {
					i = start
					break
				}
				end += loc[1]
				b.WriteString(s[start:end])
				i = end
				continue
			}
		}
		b.WriteString(s[start : start+2])
		b.WriteString(fn(body))
		b.WriteString(closing)
		i = end
	}
	b.WriteString(s[i:])
	return b.String()
}

// findPebbleClose returns the index of the closing delimiter that ends the
// expression or tag whose body starts at from, or -1. Quoted strings and
// nested braces (map literals) are skipped, so `{{ {'a': 1}}}` and
// `{{ 'a}}b' }}` close at the right place.
func findPebbleClose(s string, from int, closing string) int {
	depth := 0
	for i := from; i < len(s); i++ {
		switch c := s[i]; c {
		case '\'', '"':
			j := skipPebbleString(s, i)
			if j < 0 {
				return -1
			}
			i = j - 1
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			} else if closing == "}}" && strings.HasPrefix(s[i:], "}}") {
				return i
			}
		case '%':
			if closing == "%}" && depth == 0 && strings.HasPrefix(s[i:], "%}") {
				return i
			}
		}
	}
	return -1
}

// skipPebbleString returns the index just past the string literal opening at
// s[i], honoring backslash escapes, or -1 when it is unterminated.
func skipPebbleString(s string, i int) int {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		}
	}
	return -1
}

// rewritePebbleJSONCode renames the json filter and function in the body of
// one expression or tag, leaving string literals untouched.
func rewritePebbleJSONCode(body string) string {
	var b strings.Builder
	i := 0
	for i < len(body) {
		q := strings.IndexAny(body[i:], `'"`)
		if q < 0 {
			break
		}
		b.WriteString(renameJSONCalls(body[i : i+q]))
		end := skipPebbleString(body, i+q)
		if end < 0 {
			end = len(body)
		}
		b.WriteString(body[i+q : end])
		i = end
	}
	b.WriteString(renameJSONCalls(body[i:]))
	return b.String()
}

// renameJSONCalls rewrites one run of Pebble code containing no string
// literal. The filter goes first: once `| json(` becomes `| toJson(` the
// function pattern no longer sees a bare `json(`.
func renameJSONCalls(code string) string {
	code = pebbleJSONFilter.ReplaceAllString(code, "${1}toJson")
	locs := pebbleJSONFunction.FindAllStringIndex(code, -1)
	if locs == nil {
		return code
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		nameStart := loc[0]
		if nameStart > 0 && code[nameStart-1] == '.' || pebbleIsTestTail.MatchString(code[:nameStart]) {
			continue
		}
		b.WriteString(code[last:nameStart])
		b.WriteString("fromJson")
		last = nameStart + len("json")
	}
	b.WriteString(code[last:])
	return b.String()
}
