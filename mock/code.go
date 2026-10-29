package mock

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ident turns a topic into a usable identifier: snake_case or camelCase.
func (w *writer) ident(camel bool) string {
	words := []string{pick(w.r, []string{"get", "load", "parse", "build", "check", "find", "update", "count"})}
	words = append(words, strings.Fields(w.topic())...)
	var b strings.Builder
	for i, word := range words {
		word = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) && r < 128 || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, word)
		if word == "" {
			continue
		}
		switch {
		case i == 0:
			b.WriteString(word)
		case camel:
			b.WriteString(upperFirst(word))
		default:
			b.WriteString("_" + word)
		}
	}
	return b.String()
}

func (w *writer) codeBlock(lang string) string {
	return "```" + lang + "\n" + w.code(lang) + "\n```"
}

// infill is a fill-in-the-middle answer: a few lines of code, no fence, in the language of the
// code around the gap.
func (w *writer) infill(around string) string {
	lang := codeLanguage(around)
	if lang == "" {
		lang = pick(w.r, []string{"python", "javascript", "go"})
	}
	lines := strings.Split(w.code(lang), "\n")
	start := w.r.between(1, max(1, len(lines)-3))
	return strings.Join(lines[start:min(len(lines), start+w.r.between(1, 4))], "\n")
}

// codeSigns tell a language by its syntax, most distinctive first: Go's := before JavaScript's
// braces, TypeScript's annotations before plain JavaScript.
var codeSigns = []struct {
	lang string
	re   *regexp.Regexp
}{
	{"go", regexp.MustCompile(`(?m)^package \w+|:=|\bfunc \w+\(.*\) (\w|\[|\{)|\bfmt\.`)},
	{"rust", regexp.MustCompile(`\bfn \w+\(|\blet mut\b|\bimpl\b|println!`)},
	{"swift", regexp.MustCompile(`(?m)^import (Foundation|SwiftUI|UIKit)|\bfunc \w+\(.*\) -> `)},
	{"python", regexp.MustCompile(`(?m)^\s*def \w+\(.*\):|^\s*(import|from) \w+|"""|\bself\.|^\s*(if|for|while|elif|else|try|with) ?.*:\s*$`)},
	{"ruby", regexp.MustCompile(`(?m)^\s*def \w+|^\s*end\s*$|\bputs\b|\.each do\b`)},
	{"sql", regexp.MustCompile(`(?i)\bselect\b[\s\S]*\bfrom\b|\binsert into\b|\bcreate table\b`)},
	{"bash", regexp.MustCompile(`(?m)^#!.*\b(ba)?sh\b|^\s*(echo|export|set -e)\b|\$\{\w+`)},
	{"typescript", regexp.MustCompile(`\binterface \w+|:\s*(string|number|boolean)\b|<\w+>\(`)},
	{"javascript", regexp.MustCompile(`\bfunction\b|\b(const|let|var) \w+ =|=>|console\.log|\brequire\(`)},
}

// codeLanguage names the language of a piece of code, or "" when nothing gives it away.
func codeLanguage(src string) string {
	for _, s := range codeSigns {
		if s.re.MatchString(src) {
			return s.lang
		}
	}
	return ""
}

func (w *writer) code(lang string) string {
	fn := w.ident(lang != "python" && lang != "ruby" && lang != "rust" && lang != "bash" && lang != "sql")
	item, items, limit := pick(w.r, []string{"item", "entry", "row", "record"}), pick(w.r, []string{"items", "entries", "rows", "records"}), w.r.between(2, 100)
	field := pick(w.r, []string{"name", "status", "value", "id", "count"})
	switch lang {
	case "python":
		return fmt.Sprintf(`def %s(%s, limit=%d):
    """%s"""
    result = []
    for %s in %s:
        if not %s.get(%q):
            continue
        result.append(%s)
        if len(result) >= limit:
            break
    return result`, fn, items, limit, w.sentence(), item, items, item, field, item)
	case "go":
		return fmt.Sprintf(`// %s %s
func %s(%s []map[string]any, limit int) []map[string]any {
	var result []map[string]any
	for _, %s := range %s {
		if %s[%q] == nil {
			continue
		}
		result = append(result, %s)
		if len(result) >= limit {
			break
		}
	}
	return result
}`, fn, lowerFirst(w.sentence()), fn, items, item, items, item, field, item)
	case "rust":
		return fmt.Sprintf(`/// %s
fn %s(%s: &[Item], limit: usize) -> Vec<&Item> {
    %s
        .iter()
        .filter(|%s| %s.%s.is_some())
        .take(limit)
        .collect()
}`, w.sentence(), fn, items, items, item, item, field)
	case "bash":
		return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

# %s
%s() {
  local dir="${1:-.}"
  find "$dir" -type f -name '*.%s' | head -n %d
}

%s "$@"`, w.sentence(), fn, pick(w.r, []string{"log", "json", "txt", "csv"}), limit, fn)
	case "sql":
		return fmt.Sprintf(`-- %s
SELECT %s, COUNT(*) AS total
FROM %s
WHERE %s IS NOT NULL
GROUP BY %s
ORDER BY total DESC
LIMIT %d;`, w.sentence(), field, items, field, field, limit)
	case "ruby":
		return fmt.Sprintf(`# %s
def %s(%s, limit: %d)
  %s.select { |%s| %s[:%s] }.first(limit)
end`, w.sentence(), fn, items, limit, items, item, item, field)
	case "swift":
		return fmt.Sprintf(`/// %s
func %s(_ %s: [[String: Any]], limit: Int = %d) -> [[String: Any]] {
    Array(%s.filter { $0[%q] != nil }.prefix(limit))
}`, w.sentence(), fn, items, limit, items, field)
	case "typescript":
		return fmt.Sprintf(`// %s
export function %s(%s: Record<string, unknown>[], limit = %d): Record<string, unknown>[] {
  return %s.filter((%s) => %s[%q] != null).slice(0, limit);
}`, w.sentence(), fn, items, limit, items, item, item, field)
	default:
		return fmt.Sprintf(`// %s
function %s(%s, limit = %d) {
  const result = [];
  for (const %s of %s) {
    if (%s.%s == null) continue;
    result.push(%s);
    if (result.length >= limit) break;
  }
  return result;
}`, w.sentence(), fn, items, limit, item, items, item, field, item)
	}
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	rs[0] = unicode.ToLower(rs[0])
	return string(rs)
}
