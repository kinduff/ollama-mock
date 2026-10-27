package mock

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// The OpenAI-compatible endpoints, in the shapes Ollama's compatibility layer gives them.

func oaError(w http.ResponseWriter, err error) {
	status, msg := 500, err.Error()
	if he, ok := err.(httpError); ok {
		status, msg = he.status, he.message
	}
	if err == context.Canceled || err == context.DeadlineExceeded {
		return
	}
	typ := map[int]string{400: "invalid_request_error", 404: "not_found_error"}[status]
	if typ == "" {
		typ = "api_error"
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": msg, "type": typ, "param": nil, "code": nil}})
}

type sse struct {
	w       http.ResponseWriter
	started bool
}

func (e *sse) send(v any) {
	if !e.started {
		e.w.Header().Set("Content-Type", "text/event-stream")
		e.w.Header().Set("Cache-Control", "no-cache")
		e.w.WriteHeader(200)
		e.started = true
	}
	var b []byte
	if s, ok := v.(string); ok {
		b = []byte(s)
	} else {
		b, _ = json.Marshal(v)
	}
	e.w.Write([]byte("data: " + string(b) + "\n\n"))
	if f, ok := e.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) handleOAModels(w http.ResponseWriter, r *http.Request) {
	data := []map[string]any{}
	for _, m := range s.sortedModels() {
		data = append(data, oaModel(m))
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

func oaModel(m *installed) map[string]any {
	owner := "library"
	if i := strings.Index(m.Name, "/"); i > 0 {
		owner = m.Name[:i]
	}
	return map[string]any{"id": m.Name, "object": "model", "created": m.Modified.Unix(), "owned_by": owner}
}

func (s *Server) handleOAModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("model")
	noteModel(r, name)
	s.mu.Lock()
	m := s.models[Normalize(name)]
	s.mu.Unlock()
	if m == nil {
		oaError(w, httpError{404, fmt.Sprintf("model %q not found", name)})
		return
	}
	writeJSON(w, 200, oaModel(m))
}

// oaText reads an OpenAI message content: a string or a list of parts.
func oaText(raw json.RawMessage) (string, int) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", 0
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, 0
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", 0
	}
	var texts []string
	images := 0
	for _, p := range parts {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "image_url", "input_image":
			images++
		}
	}
	return strings.Join(texts, "\n"), images
}

type oaUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func usageOf(res genResult) oaUsage {
	return oaUsage{res.PromptTokens, res.EvalCount(), res.PromptTokens + res.EvalCount()}
}

// llamaTimings is the `timings` object Ollama puts next to `usage` in the last streamed chunk.
func llamaTimings(res genResult, t timing) map[string]any {
	pms := float64(t.prompt.Microseconds()) / 1000
	ems := float64(t.eval.Microseconds()) / 1000
	return map[string]any{
		"prompt_n": res.PromptTokens, "prompt_ms": pms, "prompt_per_second": rate(res.PromptTokens, pms),
		"predicted_n": res.EvalCount(), "predicted_ms": ems, "predicted_per_second": rate(res.EvalCount(), ems),
	}
}

func rate(n int, ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return math.Round(float64(n)/ms*1000*100) / 100
}

func finishReason(res genResult) string {
	if len(res.ToolCalls) > 0 {
		return "tool_calls"
	}
	return res.DoneReason
}

func oaCalls(calls []toolCall) []map[string]any {
	out := make([]map[string]any, len(calls))
	for i, c := range calls {
		args, _ := json.Marshal(c.Arguments)
		out[i] = map[string]any{"id": c.ID, "index": i, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(args)}}
	}
	return out
}

func oaID(prefix string, seed uint64) string { return fmt.Sprintf("%s-%d", prefix, seed%1000) }

func reasoningLevel(effort *string, m Model) (string, error) {
	if effort == nil {
		return thinkLevel(nil, m)
	}
	switch *effort {
	case "none", "":
		return "", nil
	case "minimal":
		return thinkLevel(json.RawMessage(`"low"`), m)
	}
	b, _ := json.Marshal(*effort)
	return thinkLevel(b, m)
}

func (s *Server) handleOAChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Name      string          `json:"name"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Stream        bool `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
		MaxTokens           *int         `json:"max_tokens"`
		MaxCompletionTokens *int         `json:"max_completion_tokens"`
		Stop                any          `json:"stop"`
		Seed                *int64       `json:"seed"`
		Tools               []nativeTool `json:"tools"`
		ResponseFormat      *struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Schema map[string]any `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
		ReasoningEffort *string `json:"reasoning_effort"`
		Reasoning       *struct {
			Effort *string `json:"effort"`
		} `json:"reasoning"`
		KeepAlive json.RawMessage `json:"keep_alive"`
	}
	if err := decode(r, &body); err != nil {
		oaError(w, httpError{400, err.Error()})
		return
	}
	noteModel(r, body.Model)
	if len(body.Messages) == 0 {
		oaError(w, httpError{400, "[] is too short - 'messages'"})
		return
	}
	m, err := s.lookup(body.Model)
	if err != nil {
		oaError(w, httpError{404, fmt.Sprintf("model %q not found, try pulling it first", body.Model)})
		return
	}
	if !m.Has("completion") {
		oaError(w, httpError{400, fmt.Sprintf("%q does not support chat", m.Name)})
		return
	}
	if len(body.Tools) > 0 && !m.Has("tools") {
		oaError(w, httpError{400, fmt.Sprintf("%s does not support tools", fullName(m.Name))})
		return
	}
	effort := body.ReasoningEffort
	if effort == nil && body.Reasoning != nil {
		effort = body.Reasoning.Effort
	}
	think, err := reasoningLevel(effort, m.Model)
	if err != nil {
		oaError(w, err)
		return
	}
	q := genRequest{Model: m.Model, System: m.System, Think: think, Stop: optStops(body.Stop), Seed: body.Seed}
	if body.MaxTokens != nil {
		q.MaxTokens = *body.MaxTokens
	}
	if body.MaxCompletionTokens != nil {
		q.MaxTokens = *body.MaxCompletionTokens
	}
	if f := body.ResponseFormat; f != nil {
		switch {
		case f.Type == "json_schema" && f.JSONSchema != nil && f.JSONSchema.Schema != nil:
			q.Format = f.JSONSchema.Schema
		case f.Type == "json_object" || f.Type == "json_schema":
			q.Format = "json"
		}
	}
	toolNames := map[string]string{}
	for _, msg := range body.Messages {
		text, images := oaText(msg.Content)
		mm := message{Role: msg.Role, Content: text, Images: images, ToolName: toolNames[msg.ToolCallID]}
		for _, c := range msg.ToolCalls {
			var args map[string]any
			json.Unmarshal([]byte(c.Function.Arguments), &args)
			mm.ToolCalls = append(mm.ToolCalls, toolCall{Name: c.Function.Name, Arguments: args})
			toolNames[c.ID] = c.Function.Name
		}
		q.Messages = append(q.Messages, mm)
	}
	for _, t := range body.Tools {
		q.Tools = append(q.Tools, tool{Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters})
	}
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, 0, "completion")
	if err != nil {
		oaError(w, err)
		return
	}
	defer s.finish(in)
	res := generate(q)
	t := timings(m.Model, res, in.load)
	id, created := oaID("chatcmpl", res.Seed), s.stamp().Unix()
	head := func() map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": m.Name, "system_fingerprint": "fp_ollama"}
	}

	if !body.Stream {
		var text, reasoning strings.Builder
		ok, aborted := s.play(r.Context(), m.Model, res, t, func(p piece) {
			text.WriteString(p.content)
			reasoning.WriteString(p.thinking)
		})
		if !ok {
			if aborted {
				oaError(w, httpError{500, abortMessage})
			}
			return
		}
		msg := map[string]any{"role": "assistant", "content": text.String()}
		if reasoning.Len() > 0 {
			msg["reasoning"] = reasoning.String()
		}
		if len(res.ToolCalls) > 0 {
			msg["tool_calls"] = oaCalls(res.ToolCalls)
		}
		out := head()
		out["object"] = "chat.completion"
		out["choices"] = []any{map[string]any{"index": 0, "message": msg, "finish_reason": finishReason(res)}}
		out["usage"] = usageOf(res)
		writeJSON(w, 200, out)
		return
	}

	e := &sse{w: w}
	ok, _ := s.play(r.Context(), m.Model, res, t, func(p piece) {
		delta := map[string]any{"role": "assistant", "content": p.content}
		if p.thinking != "" {
			delta["reasoning"] = p.thinking
		}
		if len(p.calls) > 0 {
			delta["tool_calls"] = oaCalls(p.calls)
		}
		c := head()
		c["choices"] = []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}
		e.send(c)
	})
	if !ok {
		return // Ollama ends a stream that failed mid-way without [DONE]
	}
	c := head()
	c["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": finishReason(res)}}
	e.send(c)
	if body.StreamOptions != nil && body.StreamOptions.IncludeUsage {
		c := head()
		c["choices"] = []any{}
		c["usage"] = usageOf(res)
		c["timings"] = llamaTimings(res, t)
		e.send(c)
	}
	e.send("[DONE]")
}

func (s *Server) handleOACompletion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model         string          `json:"model"`
		Prompt        json.RawMessage `json:"prompt"`
		Suffix        string          `json:"suffix"`
		Stream        bool            `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
		MaxTokens *int            `json:"max_tokens"`
		Stop      any             `json:"stop"`
		Seed      *int64          `json:"seed"`
		KeepAlive json.RawMessage `json:"keep_alive"`
	}
	if err := decode(r, &body); err != nil {
		oaError(w, httpError{400, err.Error()})
		return
	}
	noteModel(r, body.Model)
	prompt, _ := oaText(body.Prompt)
	if prompt == "" {
		var list []string
		if json.Unmarshal(body.Prompt, &list) == nil && len(list) > 0 {
			prompt = list[0]
		}
	}
	m, err := s.lookup(body.Model)
	if err != nil {
		oaError(w, httpError{404, fmt.Sprintf("model %q not found, try pulling it first", body.Model)})
		return
	}
	if body.Suffix != "" && !m.Has("insert") {
		oaError(w, httpError{400, fmt.Sprintf("%q does not support insert", m.Name)})
		return
	}
	q := genRequest{
		Model: m.Model, Raw: true, Suffix: body.Suffix, Stop: optStops(body.Stop), Seed: body.Seed,
		Messages: []message{{Role: "user", Content: prompt}},
	}
	if body.Suffix != "" {
		q.Raw = false
	}
	if body.MaxTokens != nil {
		q.MaxTokens = *body.MaxTokens
	}
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, 0, "completion")
	if err != nil {
		oaError(w, err)
		return
	}
	defer s.finish(in)
	res := generate(q)
	t := timings(m.Model, res, in.load)
	id, created := oaID("cmpl", res.Seed), s.stamp().Unix()
	head := func() map[string]any {
		return map[string]any{"id": id, "object": "text_completion", "created": created, "model": m.Name, "system_fingerprint": "fp_ollama"}
	}
	if !body.Stream {
		var text strings.Builder
		ok, aborted := s.play(r.Context(), m.Model, res, t, func(p piece) { text.WriteString(p.content) })
		if !ok {
			if aborted {
				oaError(w, httpError{500, abortMessage})
			}
			return
		}
		out := head()
		out["choices"] = []any{map[string]any{"text": text.String(), "index": 0, "finish_reason": res.DoneReason}}
		out["usage"] = usageOf(res)
		writeJSON(w, 200, out)
		return
	}
	e := &sse{w: w}
	ok, _ := s.play(r.Context(), m.Model, res, t, func(p piece) {
		c := head()
		c["choices"] = []any{map[string]any{"text": p.content, "index": 0, "finish_reason": nil}}
		e.send(c)
	})
	if !ok {
		return
	}
	c := head()
	c["choices"] = []any{map[string]any{"text": "", "index": 0, "finish_reason": res.DoneReason}}
	e.send(c)
	if body.StreamOptions != nil && body.StreamOptions.IncludeUsage {
		c := head()
		c["choices"] = []any{}
		c["usage"] = usageOf(res)
		c["timings"] = llamaTimings(res, t)
		e.send(c)
	}
	e.send("[DONE]")
}

func (s *Server) handleOAEmbeddings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model          string          `json:"model"`
		Input          json.RawMessage `json:"input"`
		EncodingFormat string          `json:"encoding_format"`
		Dimensions     int             `json:"dimensions"`
		KeepAlive      json.RawMessage `json:"keep_alive"`
	}
	if err := decode(r, &body); err != nil {
		oaError(w, httpError{400, err.Error()})
		return
	}
	noteModel(r, body.Model)
	inputs, err := embedInputs(body.Input)
	if err != nil {
		oaError(w, err)
		return
	}
	if len(body.Input) == 0 || string(body.Input) == "null" {
		oaError(w, httpError{400, "invalid input"})
		return
	}
	if len(inputs) == 0 {
		inputs = []string{""}
	}
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, 0, "embedding")
	if err != nil {
		oaError(w, err)
		return
	}
	defer s.finish(in)
	vectors, tokens, err := s.embedAll(in.m, inputs, in.numCtx, true, body.Dimensions)
	if err != nil {
		oaError(w, err)
		return
	}
	if !s.sleep(r.Context(), time.Duration(float64(tokens)/in.m.PromptRate()*float64(time.Second))) {
		return
	}
	data := make([]any, len(vectors))
	for i, v := range vectors {
		var emb any = v
		if body.EncodingFormat == "base64" {
			b := make([]byte, 4*len(v))
			for j, x := range v {
				binary.LittleEndian.PutUint32(b[4*j:], math.Float32bits(x))
			}
			emb = base64.StdEncoding.EncodeToString(b)
		}
		data[i] = map[string]any{"object": "embedding", "embedding": emb, "index": i}
	}
	writeJSON(w, 200, map[string]any{
		"object": "list", "data": data, "model": in.m.Name,
		"usage": map[string]any{"prompt_tokens": tokens, "total_tokens": tokens},
	})
}
