package mock

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// message is a turn of a conversation, from either API.
type message struct {
	Role      string
	Content   string
	Images    int
	ToolCalls []toolCall
	ToolName  string
}

type tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type toolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// genRequest is a generation with everything that decides its output.
type genRequest struct {
	Model     Model
	System    string
	Messages  []message
	Raw       bool   // a completion: continue the prompt, no chat formatting
	Suffix    string // fill-in-the-middle
	Tools     []tool
	Format    any    // nil, "json", or a JSON schema
	Think     string // "", "low", "medium", "high"
	MaxTokens int    // <= 0: no limit
	Stop      []string
	Seed      *int64
}

type genResult struct {
	Thinking     []string
	Content      []string
	ToolCalls    []toolCall
	DoneReason   string // stop, length
	PromptTokens int
	Seed         uint64
}

// EvalCount is how many tokens the answer took, thinking and tool calls included.
func (g genResult) EvalCount() int {
	n := len(g.Thinking) + len(g.Content)
	for _, c := range g.ToolCalls {
		b, _ := json.Marshal(c.Arguments)
		n += len(tokenize(c.Name+string(b))) + 3
	}
	return max(n, 1)
}

// seed hashes everything that decides the answer: same request, same answer. MaxTokens and Stop
// are left out on purpose, so a limited answer is a prefix of the full one, as with a real model.
func (q genRequest) seed() uint64 {
	parts := []string{q.Model.Name, q.System, q.Suffix, q.Think, strconv.FormatBool(q.Raw)}
	for _, m := range q.Messages {
		parts = append(parts, m.Role, m.Content, strconv.Itoa(m.Images), m.ToolName)
		for _, c := range m.ToolCalls {
			b, _ := json.Marshal(c.Arguments)
			parts = append(parts, c.Name, string(b))
		}
	}
	for _, t := range q.Tools {
		parts = append(parts, "tool", t.Name)
	}
	if q.Format != nil {
		b, _ := json.Marshal(q.Format)
		parts = append(parts, "format", string(b))
	}
	if q.Seed != nil {
		parts = append(parts, "seed", strconv.FormatInt(*q.Seed, 10))
	}
	return hash64(parts...)
}

func (q genRequest) lastUser() string {
	for i := len(q.Messages) - 1; i >= 0; i-- {
		if q.Messages[i].Role == "user" {
			return q.Messages[i].Content
		}
	}
	if len(q.Messages) > 0 {
		return q.Messages[len(q.Messages)-1].Content
	}
	return ""
}

func (q genRequest) promptTokens() int {
	n := 8 + len(tokenize(q.System)) + len(tokenize(q.Suffix))
	for _, m := range q.Messages {
		n += 4 + len(tokenize(m.Content)) + 256*m.Images
		for _, c := range m.ToolCalls {
			b, _ := json.Marshal(c.Arguments)
			n += len(tokenize(c.Name + string(b)))
		}
	}
	for _, t := range q.Tools {
		b, _ := json.Marshal(t.Parameters)
		n += 10 + len(tokenize(t.Name+" "+t.Description+" "+string(b)))
	}
	return n
}

// generate writes the answer. It never fails: inputs the mock can't make sense of still get one.
func generate(q genRequest) genResult {
	seed := q.seed()
	r := newRNG(seed)
	prompt := q.lastUser()
	w := &writer{r: newRNG(seed ^ 0x5eed), topics: keywords(prompt), names: properNouns(prompt), numbers: numbersIn(prompt)}
	for i, t := range w.topics {
		for _, n := range w.names {
			if strings.ToLower(n) == t {
				w.topics[i] = n // "Kyoto", not "kyoto"
			}
		}
	}
	res := genResult{Seed: seed, PromptTokens: q.promptTokens(), DoneReason: "stop"}

	if q.Think != "" {
		res.Thinking = tokenize(w.thinking(q.Think))
	}
	last := message{}
	if len(q.Messages) > 0 {
		last = q.Messages[len(q.Messages)-1]
	}
	switch {
	case len(q.Tools) > 0 && last.Role != "tool" && r.chance(0.7):
		res.ToolCalls = w.toolCalls(q.Tools, prompt)
	case q.Format != nil:
		res.Content = tokenize(w.json(q.Format))
	case last.Role == "tool":
		res.Content = tokenize(w.afterTool(last))
	case q.Raw:
		res.Content = tokenize(w.continuation(prompt))
	case q.Suffix != "":
		res.Content = tokenize(w.infill(q.Model))
	default:
		res.Content = tokenize(w.answer(q, prompt, last.Images))
	}

	if text, cut := cutAtStop(strings.Join(res.Content, ""), q.Stop); cut {
		res.Content = tokenize(text)
	}
	if q.MaxTokens > 0 && res.EvalCount() > q.MaxTokens {
		res.ToolCalls = nil
		if len(res.Thinking) >= q.MaxTokens {
			res.Thinking, res.Content = res.Thinking[:q.MaxTokens], nil
		} else {
			res.Content = res.Content[:min(len(res.Content), q.MaxTokens-len(res.Thinking))]
		}
		res.DoneReason = "length"
	}
	return res
}

func cutAtStop(text string, stops []string) (string, bool) {
	at := -1
	for _, s := range stops {
		if s == "" {
			continue
		}
		if i := strings.Index(text, s); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 {
		return text, false
	}
	return text[:at], true
}

var reToken = regexp.MustCompile(`\n+|[ \t]*[\p{L}\p{N}_']+|[ \t]*[^\p{L}\p{N}_\s]|\s`)

// tokenize splits text roughly the way a BPE tokenizer would: words with their leading space,
// long words in pieces, punctuation and newlines on their own. The pieces join back to the text.
func tokenize(s string) []string {
	var out []string
	for _, t := range reToken.FindAllString(s, -1) {
		rs := []rune(t)
		if len(rs) <= 7 {
			out = append(out, t)
			continue
		}
		lead := 0
		for lead < len(rs) && (rs[lead] == ' ' || rs[lead] == '\t') {
			lead++
		}
		end := min(len(rs), lead+4)
		out = append(out, string(rs[:end]))
		for i := end; i < len(rs); i += 4 {
			out = append(out, string(rs[i:min(len(rs), i+4)]))
		}
	}
	return out
}

// keywords picks the words of a prompt worth talking about, in order.
func keywords(prompt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(prompt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '\''
	}) {
		w = strings.Trim(strings.ToLower(w), "-'")
		if len([]rune(w)) < 3 || stopwords[w] || seen[w] {
			continue
		}
		if _, err := strconv.Atoi(w); err == nil {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) == 6 {
			break
		}
	}
	return out
}

// properNouns are the capitalized words of a prompt that don't start a sentence: "Paris" in
// "What's the weather in Paris?". Runs of them stay together ("New York").
func properNouns(prompt string) []string {
	var out []string
	for _, sentence := range reSentence.Split(prompt, -1) {
		words := strings.Fields(sentence)
		run := []string{}
		flush := func() {
			if len(run) > 0 {
				out = append(out, strings.Join(run, " "))
				run = run[:0]
			}
		}
		for i, wd := range words {
			wd = strings.TrimFunc(wd, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			rs := []rune(wd)
			if i > 0 && len(rs) > 1 && unicode.IsUpper(rs[0]) && !stopwords[strings.ToLower(wd)] {
				run = append(run, wd)
				continue
			}
			flush()
		}
		flush()
	}
	return out
}

var reNumber = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

func numbersIn(prompt string) []float64 {
	var out []float64
	for _, m := range reNumber.FindAllString(prompt, 8) {
		if f, err := strconv.ParseFloat(m, 64); err == nil {
			out = append(out, f)
		}
	}
	return out
}

var (
	reSentence   = regexp.MustCompile(`[.!?\n]+`)
	reEchoQuoted = regexp.MustCompile(`(?i)\b(?:say|reply with|respond with|answer with|output|print|return|repeat)\s+(?:only\s+|exactly\s+|just\s+|back\s+)?(?:the\s+(?:word|text|phrase)\s+)?["'“‘` + "`" + `]([^"'”’` + "`" + `\n]{1,200})["'”’` + "`" + `]`)
	reEchoWord   = regexp.MustCompile(`(?i)\b(?:say|reply with|respond with|answer with)\s+(?:only\s+|exactly\s+|just\s+)?(?:the\s+word\s+)?([\p{L}\p{N}_-]+)\s*[.!]?\s*$`)
	reYesNo      = regexp.MustCompile(`(?i)^\s*(is|are|can|could|does|do|did|should|will|would|was|were|has|have|am)\b[^?]{0,140}\?\s*$`)
	reGreeting   = regexp.MustCompile(`(?i)^\s*(hi|hello|hey|hola|good (morning|afternoon|evening)|yo|sup)\b[\s!.,]*(there)?[\s!.,]*$`)
	reBrief      = regexp.MustCompile(`(?i)\b(briefly|in one sentence|one sentence|in a sentence|short answer|tl;?dr|in a few words|concise(ly)?|one line)\b`)
	reList       = regexp.MustCompile(`(?i)\b(list|steps|how (do|can|should) i|how to|ideas|tips|ways|options|checklist|bullet)\b`)
	reTable      = regexp.MustCompile(`(?i)\b(compare|comparison|vs\.?|versus|table|differences?|pros and cons)\b`)
	reCode       = regexp.MustCompile("(?i)(```|\\b(code|function|script|implement|snippet|program|regex|class|method|bug|compile|refactor)\\b)")
)

func echoDirective(prompt string) (string, bool) {
	if m := reEchoQuoted.FindStringSubmatch(prompt); m != nil {
		return m[1], true
	}
	if m := reEchoWord.FindStringSubmatch(strings.TrimSpace(prompt)); m != nil {
		return m[1], true
	}
	return "", false
}

// writer fills templates from the vocabulary and the prompt's own words.
type writer struct {
	r       *rng
	topics  []string
	names   []string  // capitalized words from the prompt, for arguments like "city"
	numbers []float64 // numbers in the prompt, for numeric arguments
	turn    int
}

func (w *writer) topic() string {
	if len(w.topics) == 0 {
		return pick(w.r, nouns)
	}
	w.turn++
	if w.r.chance(0.6) {
		return w.topics[0]
	}
	return w.topics[w.turn%len(w.topics)]
}

func (w *writer) fill(t string) string {
	v, v2 := pick(w.r, verbs), pick(w.r, verbs)
	n, n2 := pick(w.r, nouns), pick(w.r, nouns)
	for n2 == n {
		n2 = pick(w.r, nouns)
	}
	a, a2 := pick(w.r, adjectives), pick(w.r, adjectives)
	topic := w.topic()
	repl := strings.NewReplacer(
		"{t}", topic, "{T}", upperFirst(topic), "{n}", n, "{n2}", n2, "{a}", a, "{a2}", a2,
		"{v}", v[0], "{v2}", v2[0], "{vs}", v[1], "{V}", upperFirst(v[0]), "{Adv}", pick(w.r, adverbs),
	)
	return repl.Replace(t)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

func (w *writer) sentence() string { return w.fill(pick(w.r, sentenceTemplates)) }

func (w *writer) sentences(lo, hi int) string {
	n := w.r.between(lo, hi)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = w.sentence()
	}
	return strings.Join(parts, " ")
}

func (w *writer) paragraphs(lo, hi int) []string {
	n := w.r.between(lo, hi)
	out := make([]string, n)
	for i := range out {
		out[i] = w.sentences(2, 5)
	}
	return out
}

func (w *writer) list(numbered bool) string {
	n := w.r.between(3, 7)
	lines := make([]string, n)
	bold := !numbered && w.r.chance(0.5)
	for i := range lines {
		var item string
		switch {
		case numbered:
			item = w.fill(pick(w.r, stepTemplates))
		case bold:
			item = "**" + upperFirst(w.fill("{a} {n}")) + "**: " + w.sentence()
		default:
			item = w.sentence()
		}
		if numbered {
			lines[i] = fmt.Sprintf("%d. %s", i+1, item)
		} else {
			lines[i] = "- " + item
		}
	}
	return strings.Join(lines, "\n")
}

func (w *writer) table() string {
	cols := []string{upperFirst(pick(w.r, nouns)), upperFirst(pick(w.r, adjectives)), "Notes"}
	var b strings.Builder
	b.WriteString("| " + strings.Join(cols, " | ") + " |\n|---|---|---|\n")
	n := w.r.between(3, 5)
	labels := append([]string{}, w.topics...)
	for len(labels) < n {
		labels = append(labels, pick(w.r, nouns))
	}
	for i := 0; i < n; i++ {
		row := []string{upperFirst(labels[i]), pick(w.r, []string{"Yes", "No", "Partly", "Low", "Medium", "High"}), upperFirst(w.fill("{a} {n}"))}
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (w *writer) heading() string {
	return strings.Repeat("#", w.r.between(2, 3)) + " " + w.fill(pick(w.r, headings))
}

func (w *writer) opener() string { return w.fill(pick(w.r, openers)) }
func (w *writer) closer() string { return w.fill(pick(w.r, closers)) }

func join(blocks ...string) string {
	var out []string
	for _, b := range blocks {
		if strings.TrimSpace(b) != "" {
			out = append(out, b)
		}
	}
	return strings.Join(out, "\n\n")
}

// answer is a chat reply, shaped by what the prompt looks like it wants.
func (w *writer) answer(q genRequest, prompt string, images int) string {
	if echo, ok := echoDirective(prompt); ok {
		return echo
	}
	words := len(strings.Fields(prompt))
	switch {
	case reGreeting.MatchString(prompt):
		return pick(w.r, greetings)
	case images > 0 && q.Model.Has("vision"):
		n := w.r.between(3, 5)
		lines := make([]string, n)
		for i := range lines {
			lines[i] = w.fill(pick(w.r, imageTemplates))
		}
		return strings.Join(lines, " ")
	case reBrief.MatchString(prompt):
		return w.sentences(1, 2)
	case reYesNo.MatchString(prompt) && words < 25:
		return pick(w.r, []string{"Yes.", "Yes, mostly.", "No.", "Not usually.", "It depends on the " + pick(w.r, nouns) + "."}) + " " + w.sentences(1, 3)
	case reCode.MatchString(prompt) || languageIn(prompt) != "":
		lang := languageIn(prompt)
		if lang == "" {
			lang = pick(w.r, []string{"python", "javascript", "go", "bash"})
		}
		return join(w.opener(), w.sentences(1, 2), w.codeBlock(lang), w.sentences(1, 3), w.closer())
	case reTable.MatchString(prompt):
		return join(w.opener(), w.sentences(1, 2), w.table(), w.sentences(1, 3))
	case reList.MatchString(prompt):
		return join(w.opener(), w.list(w.r.chance(0.6)), w.closer())
	}
	switch w.r.intn(6) {
	case 0, 1:
		return join(append(append([]string{w.opener()}, w.paragraphs(1, 3)...), w.closer())...)
	case 2:
		return join(w.opener(), w.heading(), w.sentences(2, 4), w.heading(), w.list(false), w.closer())
	case 3:
		return join(w.opener(), w.sentences(1, 3), w.list(w.r.chance(0.4)), w.sentences(1, 2), w.closer())
	case 4:
		return w.sentences(1, 3)
	default:
		return join(append([]string{w.sentences(2, 3)}, w.paragraphs(1, 2)...)...)
	}
}

func languageIn(prompt string) string {
	for _, f := range strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '+' && r != '#'
	}) {
		if lang, ok := codeLanguages[f]; ok && f != "query" {
			return lang
		}
	}
	return ""
}

func (w *writer) continuation(prompt string) string {
	text := w.sentences(2, 6)
	if w.r.chance(0.4) {
		text += "\n\n" + w.sentences(2, 5)
	}
	if prompt != "" && !strings.HasSuffix(prompt, " ") && !strings.HasSuffix(prompt, "\n") {
		text = " " + text
	}
	return text
}

func (w *writer) thinking(level string) string {
	lo, hi := 3, 7
	switch level {
	case "low":
		lo, hi = 1, 3
	case "high":
		lo, hi = 8, 16
	}
	n := w.r.between(lo, hi)
	parts := []string{w.fill(thoughtTemplates[0])}
	for i := 1; i < n; i++ {
		parts = append(parts, w.fill(pick(w.r, thoughtTemplates[1:])))
	}
	return strings.Join(parts, " ")
}

func (w *writer) afterTool(last message) string {
	result := strings.TrimSpace(last.Content)
	if len([]rune(result)) > 80 {
		result = string([]rune(result)[:80]) + "…"
	}
	name := last.ToolName
	if name == "" {
		name = "tool"
	}
	lead := pick(w.r, []string{
		fmt.Sprintf("Based on the %s result, here's what I found.", name),
		fmt.Sprintf("The %s call returned: %s", name, result),
		fmt.Sprintf("According to %s, %s", name, result),
		"Here's the answer from the result above.",
	})
	return join(lead, w.sentences(1, 3))
}

func (w *writer) toolCalls(tools []tool, prompt string) []toolCall {
	words := map[string]bool{}
	for _, k := range keywords(prompt) {
		words[k] = true
	}
	type scored struct {
		t     tool
		score int
		tie   uint64
	}
	ranked := make([]scored, len(tools))
	for i, t := range tools {
		s := 0
		for _, k := range keywords(strings.NewReplacer("_", " ", "-", " ").Replace(t.Name) + " " + t.Description) {
			if words[k] {
				s++
			}
		}
		ranked[i] = scored{t, s, w.r.next()}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].tie < ranked[j].tie
	})
	n := 1
	if len(ranked) > 1 && w.r.chance(0.2) {
		n = 2
	}
	calls := make([]toolCall, n)
	for i := range calls {
		t := ranked[i].t
		args, _ := w.instance(t.Parameters, "", 0).(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		calls[i] = toolCall{ID: w.callID(), Name: t.Name, Arguments: args}
	}
	return calls
}

func (w *writer) callID() string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 8)
	for i := range b {
		b[i] = digits[w.r.intn(len(digits))]
	}
	return "call_" + string(b)
}

func (w *writer) json(format any) string {
	var v any
	if schema, ok := format.(map[string]any); ok {
		v = w.instance(schema, "", 0)
	} else {
		obj := map[string]any{}
		keys := append([]string{}, w.topics...)
		for len(keys) < 2 {
			keys = append(keys, pick(w.r, nouns))
		}
		for _, k := range keys[:min(len(keys), w.r.between(2, 4))] {
			k = strings.ReplaceAll(k, " ", "_")
			obj[k] = w.instance(map[string]any{"type": pick(w.r, []string{"string", "string", "integer", "boolean"})}, k, 1)
		}
		v = obj
	}
	var b []byte
	if w.r.chance(0.5) {
		b, _ = json.MarshalIndent(v, "", "  ")
	} else {
		b, _ = json.Marshal(v)
	}
	return string(b)
}
