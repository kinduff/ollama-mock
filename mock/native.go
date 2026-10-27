package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Inference on the native API: /api/generate, /api/chat, /api/embed, /api/embeddings. The pacing
// and fault handling here are shared with the OpenAI endpoints.

type timing struct {
	load, prompt, eval, total time.Duration
}

func timings(m Model, res genResult, load time.Duration) timing {
	r := newRNG(res.Seed ^ 0x7177)
	prompt := time.Duration(float64(res.PromptTokens) / m.PromptRate() * r.jitter(0.08) * float64(time.Second))
	eval := time.Duration(float64(res.EvalCount()) / m.DecodeRate() * r.jitter(0.08) * float64(time.Second))
	return timing{load, prompt, eval, load + prompt + eval + time.Duration(r.between(800, 4000))*time.Microsecond}
}

func (t timing) native(res genResult) map[string]any {
	return map[string]any{
		"total_duration": t.total.Nanoseconds(), "load_duration": t.load.Nanoseconds(),
		"prompt_eval_count": res.PromptTokens, "prompt_eval_duration": t.prompt.Nanoseconds(),
		"eval_count": res.EvalCount(), "eval_duration": t.eval.Nanoseconds(),
	}
}

// piece is one streamed unit: a token of thinking or content, or the tool calls.
type piece struct {
	thinking string
	content  string
	calls    []toolCall
}

func pieces(res genResult) []piece {
	var out []piece
	for _, t := range res.Thinking {
		out = append(out, piece{thinking: t})
	}
	for _, t := range res.Content {
		out = append(out, piece{content: t})
	}
	if len(res.ToolCalls) > 0 {
		out = append(out, piece{calls: res.ToolCalls})
	}
	return out
}

// abortPoint is where a faulty stream stops (0: it doesn't).
func (s *Server) abortPoint(m Model, res genResult, n int) int {
	if f := s.modelFaults(m.Name); f.AbortAfter > 0 {
		return min(f.AbortAfter, n)
	}
	if s.opts.FaultRate > 0 {
		r := newRNG(res.Seed ^ 0xfa17)
		if r.float() < s.opts.FaultRate {
			return 1 + r.intn(max(n-1, 1))
		}
	}
	return 0
}

// play paces the pieces like the model would, calling emit for each. It returns false when the
// stream was cut: by a fault (aborted true) or by the client going away.
func (s *Server) play(ctx context.Context, m Model, res genResult, t timing, emit func(piece)) (ok, aborted bool) {
	ps := pieces(res)
	stop := s.abortPoint(m, res, len(ps))
	if !s.sleep(ctx, t.prompt) {
		return false, false
	}
	per := t.eval / time.Duration(max(len(ps), 1))
	for i, p := range ps {
		if stop > 0 && i == stop {
			return false, true
		}
		if !s.sleep(ctx, per) {
			return false, false
		}
		emit(p)
	}
	if stop > 0 {
		return false, true
	}
	return true, false
}

const abortMessage = "an error was encountered while running the model: unexpected EOF"

// thinkLevel reads `think`: true, false, or "low" / "medium" / "high". Absent, a thinking model
// thinks, as Ollama does.
func thinkLevel(raw json.RawMessage, m Model) (string, error) {
	level := ""
	explicit := false
	if len(raw) > 0 && string(raw) != "null" {
		var b bool
		var str string
		switch {
		case json.Unmarshal(raw, &b) == nil:
			explicit = b
			if b {
				level = "medium"
			}
		case json.Unmarshal(raw, &str) == nil:
			switch str {
			case "low", "medium", "high":
				level, explicit = str, true
			case "", "none", "false":
			default:
				return "", httpError{400, fmt.Sprintf("invalid think value: %q (must be \"high\", \"medium\", \"low\", true, or false)", str)}
			}
		default:
			return "", httpError{400, "invalid think value"}
		}
		if explicit && !m.Has("thinking") {
			return "", httpError{400, fmt.Sprintf("%q does not support thinking", m.Name)}
		}
		return level, nil
	}
	if m.Has("thinking") {
		return "medium", nil
	}
	return "", nil
}

func parseFormat(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return "json"
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) == nil {
		return schema
	}
	return nil
}

func optInt(o map[string]any, k string) (int, bool) {
	if v, ok := o[k].(float64); ok {
		return int(v), true
	}
	return 0, false
}

func optStops(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, s := range x {
			if str, ok := s.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func optSeed(o map[string]any) *int64 {
	if v, ok := o["seed"].(float64); ok {
		n := int64(v)
		return &n
	}
	return nil
}

func fullName(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return "registry.ollama.ai/library/" + name
}

// inference is what every generation does before writing: find the model, check what it can do,
// wait for a slot, load it.
type inference struct {
	m         *installed
	keepAlive *time.Duration
	numCtx    int
	load      time.Duration
	release   func()
}

func (s *Server) startInference(ctx context.Context, name string, keepAliveRaw json.RawMessage, numCtx int, need string) (*inference, error) {
	m, err := s.lookup(name)
	if err != nil {
		return nil, httpError{404, fmt.Sprintf("model %q not found, try pulling it first", name)}
	}
	if need != "" && !m.Has(need) {
		verb := map[string]string{"completion": "generate", "embedding": "embeddings", "decision": "decisions"}[need]
		return nil, httpError{400, fmt.Sprintf("%q does not support %s", m.Name, verb)}
	}
	keepAlive, err := parseKeepAlive(keepAliveRaw)
	if err != nil {
		return nil, httpError{400, err.Error()}
	}
	if f := s.modelFaults(m.Name); f.Status != 0 || f.Error != "" {
		status := f.Status
		if status == 0 {
			status = 500
		}
		return nil, httpError{status, f.Error}
	}
	release, err := s.slot(ctx, m.Name)
	if err != nil {
		return nil, err
	}
	load, err := s.load(ctx, m, keepAlive, numCtx)
	if err != nil {
		release()
		return nil, err
	}
	return &inference{m: m, keepAlive: keepAlive, numCtx: numCtx, load: load, release: release}, nil
}

// finish releases the slot and applies keep_alive.
func (s *Server) finish(in *inference) {
	in.release()
	s.touch(in.m, in.keepAlive, in.numCtx)
}

func nativeError(w http.ResponseWriter, err error) {
	if he, ok := err.(httpError); ok {
		writeError(w, he.status, he.message)
		return
	}
	if err == context.Canceled || err == context.DeadlineExceeded {
		return
	}
	writeError(w, 500, err.Error())
}

func nativeCalls(calls []toolCall) []map[string]any {
	out := make([]map[string]any, len(calls))
	for i, c := range calls {
		out[i] = map[string]any{"id": c.ID, "function": map[string]any{"index": i, "name": c.Name, "arguments": c.Arguments}}
	}
	return out
}

// loadOnly answers a request with nothing to generate: Ollama loads (or, with keep_alive 0,
// unloads) the model and says which.
func (s *Server) loadOnly(w http.ResponseWriter, r *http.Request, name string, keepAliveRaw json.RawMessage, numCtx int, chat bool) {
	keepAlive, err := parseKeepAlive(keepAliveRaw)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	m, _ := s.lookup(name)
	res := map[string]any{"model": m.Name, "created_at": s.stamp().Format(time.RFC3339Nano), "done": true}
	if chat {
		res["message"] = map[string]any{"role": "assistant", "content": ""}
	} else {
		res["response"] = ""
	}
	if keepAlive != nil && *keepAlive == 0 {
		s.unload(m.Name)
		res["done_reason"] = "unload"
		writeJSON(w, 200, res)
		return
	}
	in, err := s.startInference(r.Context(), name, keepAliveRaw, numCtx, "")
	if err != nil {
		nativeError(w, err)
		return
	}
	s.finish(in)
	res["done_reason"] = "load"
	writeJSON(w, 200, res)
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string          `json:"model"`
		Prompt    string          `json:"prompt"`
		Suffix    string          `json:"suffix"`
		System    string          `json:"system"`
		Context   []int           `json:"context"`
		Stream    *bool           `json:"stream"`
		Raw       bool            `json:"raw"`
		Format    json.RawMessage `json:"format"`
		Images    []string        `json:"images"`
		Options   map[string]any  `json:"options"`
		KeepAlive json.RawMessage `json:"keep_alive"`
		Think     json.RawMessage `json:"think"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	noteModel(r, body.Model)
	if body.Model == "" {
		writeError(w, 400, "model is required")
		return
	}
	m, err := s.lookup(body.Model)
	if err != nil {
		writeError(w, 404, fmt.Sprintf("model %q not found, try pulling it first", body.Model))
		return
	}
	if !m.Has("completion") {
		writeError(w, 400, fmt.Sprintf("%q does not support generate", m.Name))
		return
	}
	numCtx, _ := optInt(body.Options, "num_ctx")
	if body.Prompt == "" && len(body.Images) == 0 && body.Suffix == "" {
		s.loadOnly(w, r, body.Model, body.KeepAlive, numCtx, false)
		return
	}
	if body.Suffix != "" && !m.Has("insert") {
		writeError(w, 400, fmt.Sprintf("%q does not support insert", m.Name))
		return
	}
	think, err := thinkLevel(body.Think, m.Model)
	if err != nil {
		nativeError(w, err)
		return
	}
	if body.Raw {
		think = ""
	}
	maxTokens, _ := optInt(body.Options, "num_predict")
	system := body.System
	if system == "" {
		system = m.System
	}
	q := genRequest{
		Model: m.Model, System: system, Raw: body.Raw, Suffix: body.Suffix, Format: parseFormat(body.Format),
		Think: think, MaxTokens: maxTokens, Stop: optStops(body.Options["stop"]), Seed: optSeed(body.Options),
		Messages: []message{{Role: "user", Content: body.Prompt, Images: len(body.Images)}},
	}
	if len(body.Context) > 0 {
		q.Messages = append([]message{{Role: "context", Content: fmt.Sprint(body.Context)}}, q.Messages...)
	}
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, numCtx, "completion")
	if err != nil {
		nativeError(w, err)
		return
	}
	defer s.finish(in)
	res := generate(q)
	t := timings(m.Model, res, in.load)
	stream := streamFlag(body.Stream)
	out := &ndjson{w: w, stream: stream}
	created := func() string { return s.stamp().Format(time.RFC3339Nano) }
	var text, thinking strings.Builder
	ok, aborted := s.play(r.Context(), m.Model, res, t, func(p piece) {
		text.WriteString(p.content)
		thinking.WriteString(p.thinking)
		chunk := map[string]any{"model": m.Name, "created_at": created(), "response": p.content, "done": false}
		if p.thinking != "" {
			chunk["thinking"] = p.thinking
		}
		out.send(chunk)
	})
	if !ok {
		if aborted {
			out.fail(abortMessage)
		}
		return
	}
	final := t.native(res)
	final["model"], final["created_at"], final["done"], final["done_reason"] = m.Name, created(), true, res.DoneReason
	final["response"] = ""
	if !body.Raw {
		final["context"] = contextIDs(body.Context, q, res)
	}
	if !stream {
		final["response"] = text.String()
		if thinking.Len() > 0 {
			final["thinking"] = thinking.String()
		}
	}
	out.done(final)
}

// contextIDs stands in for the token ids /api/generate returns to continue a conversation.
func contextIDs(prev []int, q genRequest, res genResult) []int {
	ids := append([]int{}, prev...)
	for _, t := range tokenize(q.lastUser()) {
		ids = append(ids, int(hash64(t)%151000))
	}
	for _, t := range res.Content {
		ids = append(ids, int(hash64(t)%151000))
	}
	return ids
}

type nativeMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Thinking  string `json:"thinking,omitempty"`
	Images    []any  `json:"images,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolCalls []struct {
		Function struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

type nativeTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string          `json:"model"`
		Messages  []nativeMessage `json:"messages"`
		Tools     []nativeTool    `json:"tools"`
		Format    json.RawMessage `json:"format"`
		Stream    *bool           `json:"stream"`
		Options   map[string]any  `json:"options"`
		KeepAlive json.RawMessage `json:"keep_alive"`
		Think     json.RawMessage `json:"think"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	noteModel(r, body.Model)
	if body.Model == "" {
		writeError(w, 400, "model is required")
		return
	}
	m, err := s.lookup(body.Model)
	if err != nil {
		writeError(w, 404, fmt.Sprintf("model %q not found, try pulling it first", body.Model))
		return
	}
	if !m.Has("completion") {
		writeError(w, 400, fmt.Sprintf("%q does not support chat", m.Name))
		return
	}
	numCtx, _ := optInt(body.Options, "num_ctx")
	if len(body.Messages) == 0 {
		s.loadOnly(w, r, body.Model, body.KeepAlive, numCtx, true)
		return
	}
	if len(body.Tools) > 0 && !m.Has("tools") {
		writeError(w, 400, fmt.Sprintf("%s does not support tools", fullName(m.Name)))
		return
	}
	think, err := thinkLevel(body.Think, m.Model)
	if err != nil {
		nativeError(w, err)
		return
	}
	maxTokens, _ := optInt(body.Options, "num_predict")
	q := genRequest{
		Model: m.Model, System: m.System, Format: parseFormat(body.Format), Think: think, MaxTokens: maxTokens,
		Stop: optStops(body.Options["stop"]), Seed: optSeed(body.Options),
	}
	for _, msg := range body.Messages {
		mm := message{Role: msg.Role, Content: msg.Content, Images: len(msg.Images), ToolName: msg.ToolName}
		for _, c := range msg.ToolCalls {
			mm.ToolCalls = append(mm.ToolCalls, toolCall{Name: c.Function.Name, Arguments: c.Function.Arguments})
		}
		q.Messages = append(q.Messages, mm)
	}
	for _, t := range body.Tools {
		q.Tools = append(q.Tools, tool{Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters})
	}
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, numCtx, "completion")
	if err != nil {
		nativeError(w, err)
		return
	}
	defer s.finish(in)
	res := generate(q)
	t := timings(m.Model, res, in.load)
	stream := streamFlag(body.Stream)
	out := &ndjson{w: w, stream: stream}
	created := func() string { return s.stamp().Format(time.RFC3339Nano) }
	var text, thinking strings.Builder
	ok, aborted := s.play(r.Context(), m.Model, res, t, func(p piece) {
		text.WriteString(p.content)
		thinking.WriteString(p.thinking)
		msg := map[string]any{"role": "assistant", "content": p.content}
		if p.thinking != "" {
			msg["thinking"] = p.thinking
		}
		if len(p.calls) > 0 {
			msg["tool_calls"] = nativeCalls(p.calls)
		}
		out.send(map[string]any{"model": m.Name, "created_at": created(), "message": msg, "done": false})
	})
	if !ok {
		if aborted {
			out.fail(abortMessage)
		}
		return
	}
	final := t.native(res)
	msg := map[string]any{"role": "assistant", "content": ""}
	if !stream {
		msg["content"] = text.String()
		if thinking.Len() > 0 {
			msg["thinking"] = thinking.String()
		}
		if len(res.ToolCalls) > 0 {
			msg["tool_calls"] = nativeCalls(res.ToolCalls)
		}
	}
	final["model"], final["created_at"], final["message"], final["done"], final["done_reason"] = m.Name, created(), msg, true, res.DoneReason
	out.done(final)
}

// embedInputs reads `input`: a string, a list of strings, or token ids.
func embedInputs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil, nil
		}
		return []string{one}, nil
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many, nil
	}
	var ids []int
	if json.Unmarshal(raw, &ids) == nil {
		return []string{fmt.Sprint(ids)}, nil
	}
	var idLists [][]int
	if json.Unmarshal(raw, &idLists) == nil {
		out := make([]string, len(idLists))
		for i, l := range idLists {
			out[i] = fmt.Sprint(l)
		}
		return out, nil
	}
	return nil, httpError{400, "invalid input type"}
}

// embedAll embeds inputs for a model; tokens is the prompt_eval_count.
func (s *Server) embedAll(m *installed, inputs []string, numCtx int, truncate bool, dims int) ([][]float32, int, error) {
	if numCtx <= 0 {
		numCtx = min(s.opts.ContextLength, m.ContextLength)
	}
	vectors := make([][]float32, len(inputs))
	tokens := 0
	for i, in := range inputs {
		n := len(tokenize(in)) + 2
		if n > numCtx {
			if !truncate {
				return nil, 0, httpError{400, "the input length exceeds the context length"}
			}
			n = numCtx
		}
		tokens += n
		vectors[i] = truncateVector(embedVector(m.Name, in, m.Dimensions), dims)
	}
	return vectors, tokens, nil
}

func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model      string          `json:"model"`
		Input      json.RawMessage `json:"input"`
		Truncate   *bool           `json:"truncate"`
		Dimensions int             `json:"dimensions"`
		Options    map[string]any  `json:"options"`
		KeepAlive  json.RawMessage `json:"keep_alive"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	noteModel(r, body.Model)
	inputs, err := embedInputs(body.Input)
	if err != nil {
		nativeError(w, err)
		return
	}
	numCtx, _ := optInt(body.Options, "num_ctx")
	keepAlive, err := parseKeepAlive(body.KeepAlive)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(inputs) == 0 && keepAlive != nil && *keepAlive == 0 {
		m, err := s.lookup(body.Model)
		if err != nil {
			writeError(w, 404, fmt.Sprintf("model %q not found, try pulling it first", body.Model))
			return
		}
		s.unload(m.Name)
		writeJSON(w, 200, map[string]any{"model": m.Name, "embeddings": [][]float32{}})
		return
	}
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, numCtx, "embedding")
	if err != nil {
		nativeError(w, err)
		return
	}
	defer s.finish(in)
	vectors, tokens, err := s.embedAll(in.m, inputs, in.numCtx, body.Truncate == nil || *body.Truncate, body.Dimensions)
	if err != nil {
		nativeError(w, err)
		return
	}
	if vectors == nil {
		vectors = [][]float32{}
	}
	prompt := time.Duration(float64(tokens) / in.m.PromptRate() * float64(time.Second))
	if !s.sleep(r.Context(), prompt) {
		return
	}
	writeJSON(w, 200, map[string]any{
		"model": in.m.Name, "embeddings": vectors, "total_duration": (in.load + prompt + time.Millisecond).Nanoseconds(),
		"load_duration": in.load.Nanoseconds(), "prompt_eval_count": tokens,
	})
}

// handleEmbeddings is the older /api/embeddings: one prompt, one vector.
func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string          `json:"model"`
		Prompt    string          `json:"prompt"`
		Options   map[string]any  `json:"options"`
		KeepAlive json.RawMessage `json:"keep_alive"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	noteModel(r, body.Model)
	numCtx, _ := optInt(body.Options, "num_ctx")
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, numCtx, "embedding")
	if err != nil {
		nativeError(w, err)
		return
	}
	defer s.finish(in)
	if body.Prompt == "" {
		writeJSON(w, 200, map[string]any{"embedding": []float32{}})
		return
	}
	vectors, _, _ := s.embedAll(in.m, []string{body.Prompt}, in.numCtx, true, 0)
	writeJSON(w, 200, map[string]any{"embedding": vectors[0]})
}
