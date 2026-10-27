package mock

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// instance makes a value that satisfies a JSON schema: tool call arguments and structured output.
// It covers what tools and response formats use in practice (types, properties, required, enum,
// const, items, anyOf/oneOf, min/max, a few string formats), not all of JSON Schema.
func (w *writer) instance(schema map[string]any, name string, depth int) any {
	if schema == nil {
		return w.byName(name)
	}
	if c, ok := schema["const"]; ok {
		return c
	}
	if e, ok := schema["enum"].([]any); ok && len(e) > 0 {
		return e[w.r.intn(len(e))]
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if alts, ok := schema[key].([]any); ok && len(alts) > 0 {
			for _, a := range alts {
				if m, ok := a.(map[string]any); ok && m["type"] != "null" {
					return w.instance(m, name, depth)
				}
			}
		}
	}
	typ := schemaType(schema)
	switch typ {
	case "object":
		props, _ := schema["properties"].(map[string]any)
		required := map[string]bool{}
		if req, ok := schema["required"].([]any); ok {
			for _, k := range req {
				if s, ok := k.(string); ok {
					required[s] = true
				}
			}
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		obj := map[string]any{}
		for _, k := range keys {
			if required[k] || len(required) == 0 || w.r.chance(0.5) {
				sub, _ := props[k].(map[string]any)
				obj[k] = w.instance(sub, k, depth+1)
			}
		}
		if len(props) == 0 && depth < 2 && schema["properties"] == nil && schema["type"] == nil {
			obj[pick(w.r, nouns)] = w.byName("")
		}
		return obj
	case "array":
		if depth > 4 {
			return []any{}
		}
		items, _ := schema["items"].(map[string]any)
		lo, hi := 1, 3
		if v, ok := number(schema["minItems"]); ok {
			lo = int(v)
			hi = max(hi, lo)
		}
		if v, ok := number(schema["maxItems"]); ok {
			hi = min(hi, int(v))
			lo = min(lo, hi)
		}
		out := make([]any, w.r.between(lo, hi))
		for i := range out {
			out[i] = w.instance(items, strings.TrimSuffix(name, "s"), depth+1)
		}
		return out
	case "integer", "number":
		lo, hi := rangeByName(name)
		if v, ok := number(schema["minimum"]); ok {
			lo = v
			hi = math.Max(hi, lo+10)
		}
		if v, ok := number(schema["maximum"]); ok {
			hi = v
			lo = math.Min(lo, hi)
		}
		for i, x := range w.numbers {
			if x >= lo && x <= hi && (typ == "number" || x == math.Trunc(x)) {
				w.numbers = append(w.numbers[:i:i], w.numbers[i+1:]...)
				if typ == "integer" {
					return int(x)
				}
				return x
			}
		}
		if typ == "integer" {
			return w.r.between(int(math.Ceil(lo)), int(math.Floor(hi)))
		}
		return math.Round((lo+(hi-lo)*w.r.float())*100) / 100
	case "boolean":
		return w.r.chance(0.5)
	case "null":
		return nil
	default:
		s := w.stringFor(name, schema)
		if v, ok := number(schema["maxLength"]); ok && len([]rune(s)) > int(v) {
			s = string([]rune(s)[:int(v)])
		}
		if v, ok := number(schema["minLength"]); ok {
			for len([]rune(s)) < int(v) {
				s += " " + pick(w.r, nouns)
			}
		}
		return s
	}
}

func schemaType(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && s != "null" {
				return s
			}
		}
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	if _, ok := schema["items"]; ok {
		return "array"
	}
	return "string"
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func rangeByName(name string) (float64, float64) {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "year"):
		return 1990, 2026
	case strings.Contains(n, "age"):
		return 18, 90
	case strings.Contains(n, "month"):
		return 1, 12
	case strings.Contains(n, "day"):
		return 1, 28
	case strings.Contains(n, "lat"):
		return -60, 60
	case strings.Contains(n, "lon") || strings.Contains(n, "lng"):
		return -150, 150
	case strings.Contains(n, "temp"):
		return -5, 35
	case strings.Contains(n, "percent") || strings.Contains(n, "score"):
		return 0, 100
	case strings.Contains(n, "count") || strings.Contains(n, "limit") || strings.Contains(n, "num") || strings.Contains(n, "max"):
		return 1, 20
	case strings.Contains(n, "price") || strings.Contains(n, "amount"):
		return 1, 500
	}
	return 0, 100
}

var (
	cities    = []string{"Lisbon", "Tokyo", "Toronto", "Nairobi", "Oslo", "Mexico City", "Seoul", "Buenos Aires", "Berlin", "Melbourne"}
	countries = []string{"Portugal", "Japan", "Canada", "Kenya", "Norway", "Mexico", "South Korea", "Argentina", "Germany", "Australia"}
	names     = []string{"Ana Silva", "Kenji Sato", "Maya Patel", "Omar Haddad", "Lena Berg", "Diego Ruiz", "Ada Okafor", "Sam Lee"}
)

func (w *writer) stringFor(name string, schema map[string]any) string {
	switch schema["format"] {
	case "date":
		return fmt.Sprintf("2026-%02d-%02d", w.r.between(1, 12), w.r.between(1, 28))
	case "date-time":
		return fmt.Sprintf("2026-%02d-%02dT%02d:%02d:00Z", w.r.between(1, 12), w.r.between(1, 28), w.r.between(0, 23), w.r.between(0, 59))
	case "email":
		return strings.ToLower(strings.Fields(pick(w.r, names))[0]) + "@example.com"
	case "uri", "url":
		return "https://example.com/" + strings.ReplaceAll(w.topic(), " ", "-")
	case "uuid":
		h := sha256hex(fmt.Sprint(w.r.next()))
		return h[:8] + "-" + h[8:12] + "-4" + h[13:16] + "-a" + h[17:20] + "-" + h[20:32]
	}
	return w.byName(name)
}

// byName guesses a value from a property name: "city" gets a city, "email" an address.
func (w *writer) byName(name string) string {
	n := strings.ToLower(name)
	has := func(s ...string) bool {
		for _, x := range s {
			if strings.Contains(n, x) {
				return true
			}
		}
		return false
	}
	if len(w.names) > 0 && (has("city", "location", "place", "destination", "origin", "country", "query", "search", "company", "person", "author", "restaurant", "venue", "hotel", "store", "brand", "product", "recipient") || n == "name") {
		return w.names[w.r.intn(len(w.names))]
	}
	switch {
	case has("city", "location", "place", "destination", "origin"):
		return pick(w.r, cities)
	case has("country"):
		return pick(w.r, countries)
	case has("email"):
		return strings.ToLower(strings.Fields(pick(w.r, names))[0]) + "@example.com"
	case has("url", "link", "website", "href"):
		return "https://example.com/" + strings.ReplaceAll(w.topic(), " ", "-")
	case has("date", "when", "time", "deadline"):
		return fmt.Sprintf("2026-%02d-%02d", w.r.between(1, 12), w.r.between(1, 28))
	case n == "name" || has("author", "person", "user", "owner", "assignee"):
		return pick(w.r, names)
	case has("unit"):
		return pick(w.r, []string{"celsius", "fahrenheit"})
	case has("lang"):
		return pick(w.r, []string{"en", "es", "pt", "ja", "de"})
	case has("currency"):
		return pick(w.r, []string{"USD", "EUR", "MXN", "JPY"})
	case has("color", "colour"):
		return pick(w.r, []string{"red", "blue", "green", "orange", "purple"})
	case has("status", "state"):
		return pick(w.r, []string{"open", "pending", "done", "active"})
	case has("id"):
		return fmt.Sprintf("%s_%d", pick(w.r, []string{"usr", "ord", "doc", "evt"}), w.r.between(1000, 99999))
	case has("path", "file"):
		return "/tmp/" + strings.ReplaceAll(w.topic(), " ", "_") + pick(w.r, []string{".txt", ".json", ".md"})
	case has("query", "search", "keyword", "term", "topic", "subject"):
		return w.topic()
	case has("title", "summary", "heading"):
		return upperFirst(w.fill("{a} {n} for the {t}"))
	case has("description", "message", "content", "text", "body", "reason", "note", "comment"):
		return w.sentence()
	case has("tag", "label", "category", "type", "kind"):
		return pick(w.r, adjectives)
	}
	return w.topic()
}
