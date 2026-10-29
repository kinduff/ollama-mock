package mock

import (
	"bufio"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTest(t *testing.T, opts Options) string {
	t.Helper()
	ts := httptest.NewServer(New(opts))
	t.Cleanup(ts.Close)
	return ts.URL
}

func post(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func lines(t *testing.T, url, body string) []string {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out []string
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if sc.Text() != "" {
			out = append(out, sc.Text())
		}
	}
	return out
}

func chatContent(t *testing.T, base, model, prompt string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"model": model, "stream": false, "think": false,
		"messages": []any{map[string]any{"role": "user", "content": prompt}}})
	status, out := post(t, base+"/api/chat", string(body))
	if status != 200 {
		t.Fatalf("chat %d: %v", status, out)
	}
	return out["message"].(map[string]any)["content"].(string)
}

func TestSameRequestSameAnswer(t *testing.T) {
	a := newTest(t, Options{Models: []string{"qwen3:8b"}})
	b := newTest(t, Options{Models: []string{"qwen3:8b"}})
	for _, p := range []string{"Explain how DNS works", "Write a python function to dedupe a list", "hi"} {
		x, y := chatContent(t, a, "qwen3:8b", p), chatContent(t, b, "qwen3:8b", p)
		if x != y {
			t.Errorf("%q: answers differ across servers:\n%s\n---\n%s", p, x, y)
		}
	}
}

func TestVariety(t *testing.T) {
	base := newTest(t, Options{Models: []string{"qwen3:8b", "llama3.2"}})
	prompts := []string{
		"Explain how DNS works", "What is a good name for a cat?", "Compare Postgres vs MySQL",
		"List some tips for remote work", "Write a bash script to rotate logs", "Tell me about Lisbon",
		"How do I bake bread?", "Is coffee bad for you?", "Summarize the plot of a heist movie",
		"Why is the sky blue?", "Give me ideas for a birthday party", "What does a load balancer do?",
	}
	seen := map[string]bool{}
	lengths := map[int]bool{}
	for _, p := range prompts {
		for _, m := range []string{"qwen3:8b", "llama3.2"} {
			c := chatContent(t, base, m, p)
			seen[c] = true
			lengths[len(c)/50] = true
		}
	}
	if len(seen) != 2*len(prompts) {
		t.Errorf("only %d distinct answers for %d requests", len(seen), 2*len(prompts))
	}
	if len(lengths) < 6 {
		t.Errorf("answers all about the same length: %v", lengths)
	}
}

func TestEchoDirective(t *testing.T) {
	base := newTest(t, Options{Models: []string{"llama3.2"}})
	for prompt, want := range map[string]string{`Say "pong"`: "pong", "Reply with OK.": "OK", "Respond with only the word banana": "banana"} {
		if got := chatContent(t, base, "llama3.2", prompt); got != want {
			t.Errorf("%q: got %q, want %q", prompt, got, want)
		}
	}
}

func TestUnknownModelNeedsPull(t *testing.T) {
	base := newTest(t, Options{})
	status, out := post(t, base+"/api/chat", `{"model":"gemma3:4b","messages":[{"role":"user","content":"hi"}]}`)
	if status != 404 || out["error"] != `model "gemma3:4b" not found, try pulling it first` {
		t.Fatalf("got %d %v", status, out)
	}
	ls := lines(t, base+"/api/pull", `{"model":"gemma3:4b"}`)
	if ls[0] != `{"status":"pulling manifest"}` || ls[len(ls)-1] != `{"status":"success"}` {
		t.Fatalf("pull stream: %v", ls)
	}
	if got := chatContent(t, base, "gemma3:4b", "hi"); got == "" {
		t.Fatal("empty answer after pull")
	}
	status, out = post(t, base+"/api/show", `{"model":"gemma3:4b"}`)
	if status != 200 || out["details"].(map[string]any)["family"] != "gemma3" {
		t.Fatalf("show: %d %v", status, out)
	}
}

func TestAnyModel(t *testing.T) {
	base := newTest(t, Options{AnyModel: true})
	if got := chatContent(t, base, "someone/brand-new-model:7b", "hello"); got == "" {
		t.Fatal("empty answer")
	}
}

func TestMissingPull(t *testing.T) {
	base := newTest(t, Options{Faults: Faults{Missing: []string{"nope:1b"}}})
	ls := lines(t, base+"/api/pull", `{"model":"nope:1b"}`)
	if ls[len(ls)-1] != `{"error":"pull model manifest: file does not exist"}` {
		t.Fatalf("got %v", ls)
	}
}

func TestStreamsJoinToTheSameAnswer(t *testing.T) {
	base := newTest(t, Options{Models: []string{"qwen3:8b"}})
	body := `{"model":"qwen3:8b","messages":[{"role":"user","content":"Explain caching"}]}`
	var content, thinking strings.Builder
	ls := lines(t, base+"/api/chat", body)
	for _, l := range ls {
		var c struct {
			Message struct{ Content, Thinking string }
			Done    bool `json:"done"`
		}
		json.Unmarshal([]byte(l), &c)
		content.WriteString(c.Message.Content)
		thinking.WriteString(c.Message.Thinking)
	}
	_, full := post(t, base+"/api/chat", strings.Replace(body, `{"model"`, `{"stream":false,"model"`, 1))
	msg := full["message"].(map[string]any)
	if content.String() != msg["content"] || thinking.String() != msg["thinking"] {
		t.Fatal("streamed and whole answers differ")
	}
	if thinking.Len() == 0 {
		t.Fatal("a thinking model didn't think")
	}
	var last map[string]any
	json.Unmarshal([]byte(ls[len(ls)-1]), &last)
	if last["done"] != true || last["eval_count"].(float64) < 2 {
		t.Fatalf("final chunk: %v", last)
	}
}

func TestMaxTokensAndStop(t *testing.T) {
	base := newTest(t, Options{Models: []string{"llama3.2"}})
	_, full := post(t, base+"/api/generate", `{"model":"llama3.2","prompt":"Tell me about rivers","stream":false}`)
	_, short := post(t, base+"/api/generate", `{"model":"llama3.2","prompt":"Tell me about rivers","stream":false,"options":{"num_predict":5}}`)
	if short["done_reason"] != "length" || short["eval_count"].(float64) != 5 {
		t.Fatalf("num_predict 5: %v", short)
	}
	if !strings.HasPrefix(full["response"].(string), short["response"].(string)) {
		t.Fatal("a limited answer is not a prefix of the full one")
	}
	words := strings.Fields(full["response"].(string))
	stop := words[len(words)/2]
	body, _ := json.Marshal(map[string]any{"model": "llama3.2", "prompt": "Tell me about rivers", "stream": false, "options": map[string]any{"stop": []string{stop}}})
	_, stopped := post(t, base+"/api/generate", string(body))
	if strings.Contains(stopped["response"].(string), stop) {
		t.Fatalf("stop %q still in %q", stop, stopped["response"])
	}
}

func TestToolCalls(t *testing.T) {
	base := newTest(t, Options{Models: []string{"qwen3:8b"}})
	tools := `[{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"},"unit":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["city"]}}}]`
	calls := 0
	for _, city := range []string{"Paris", "Tokyo", "Lima", "Oslo", "Cairo", "Quito", "Rome", "Dakar"} {
		_, out := post(t, base+"/api/chat", `{"model":"qwen3:8b","stream":false,"think":false,"tools":`+tools+`,"messages":[{"role":"user","content":"What's the weather in `+city+`?"}]}`)
		tc, _ := out["message"].(map[string]any)["tool_calls"].([]any)
		if len(tc) == 0 {
			continue
		}
		calls++
		fn := tc[0].(map[string]any)["function"].(map[string]any)
		args := fn["arguments"].(map[string]any)
		if fn["name"] != "get_weather" || args["city"] != city {
			t.Errorf("%s: %v", city, fn)
		}
		if u, ok := args["unit"]; ok && u != "celsius" && u != "fahrenheit" {
			t.Errorf("unit %v not in enum", u)
		}
	}
	if calls < 3 || calls == 8 {
		t.Errorf("%d of 8 requests called the tool; want most, not all", calls)
	}
	// After a tool result, the model answers.
	_, out := post(t, base+"/api/chat", `{"model":"qwen3:8b","stream":false,"think":false,"tools":`+tools+`,"messages":[{"role":"user","content":"Weather in Paris?"},{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Paris"}}}]},{"role":"tool","tool_name":"get_weather","content":"18 C, cloudy"}]}`)
	msg := out["message"].(map[string]any)
	if msg["tool_calls"] != nil || msg["content"] == "" {
		t.Fatalf("after a tool result: %v", msg)
	}
	status, _ := post(t, base+"/api/chat", `{"model":"gemma2","messages":[{"role":"user","content":"hi"}],"tools":`+tools+`}`)
	if status != 404 {
		t.Fatalf("uninstalled gemma2: %d", status)
	}
}

func TestStructuredOutput(t *testing.T) {
	base := newTest(t, Options{Models: []string{"llama3.2"}})
	schema := `{"type":"object","properties":{"name":{"type":"string"},"age":{"type":"integer","minimum":1,"maximum":20},"tags":{"type":"array","items":{"type":"string"}}},"required":["name","age","tags"]}`
	_, out := post(t, base+"/api/generate", `{"model":"llama3.2","prompt":"Describe a dog","stream":false,"format":`+schema+`}`)
	var v struct {
		Name string
		Age  *int
		Tags []string
	}
	if err := json.Unmarshal([]byte(out["response"].(string)), &v); err != nil {
		t.Fatalf("not JSON: %q", out["response"])
	}
	if v.Name == "" || v.Age == nil || *v.Age < 1 || *v.Age > 20 || len(v.Tags) == 0 {
		t.Fatalf("doesn't match the schema: %+v", v)
	}
}

func TestEmbeddings(t *testing.T) {
	base := newTest(t, Options{Models: []string{"nomic-embed-text", "qwen3:8b"}})
	_, out := post(t, base+"/api/embed", `{"model":"nomic-embed-text","input":["the cat sat on the mat","a cat sitting on a mat","quarterly revenue grew eight percent"]}`)
	raw := out["embeddings"].([]any)
	vec := func(i int) []float64 {
		var v []float64
		for _, x := range raw[i].([]any) {
			v = append(v, x.(float64))
		}
		return v
	}
	cos := func(a, b []float64) (s float64) {
		for i := range a {
			s += a[i] * b[i]
		}
		return
	}
	a, b, c := vec(0), vec(1), vec(2)
	if len(a) != 768 {
		t.Fatalf("nomic-embed-text has 768 dimensions, got %d", len(a))
	}
	if n := cos(a, a); math.Abs(n-1) > 1e-4 {
		t.Fatalf("not normalized: %f", n)
	}
	if cos(a, b) <= cos(a, c)+0.1 {
		t.Fatalf("similar texts aren't closer: %f vs %f", cos(a, b), cos(a, c))
	}
	status, _ := post(t, base+"/api/generate", `{"model":"nomic-embed-text"}`)
	if status != 400 {
		t.Fatalf("generate on an embedding model: %d", status)
	}
	_, out = post(t, base+"/v1/embeddings", `{"model":"nomic-embed-text","input":"hello","dimensions":256}`)
	if n := len(out["data"].([]any)[0].(map[string]any)["embedding"].([]any)); n != 256 {
		t.Fatalf("dimensions 256: got %d", n)
	}
}

func TestLoadUnloadAndPS(t *testing.T) {
	base := newTest(t, Options{Models: []string{"qwen3:8b", "llama3.2", "phi4", "gemma2"}, MaxLoaded: 2})
	ps := func() []string {
		res, err := http.Get(base + "/api/ps")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct{ Models []struct{ Name string } }
		json.NewDecoder(res.Body).Decode(&out)
		var names []string
		for _, m := range out.Models {
			names = append(names, m.Name)
		}
		return names
	}
	_, out := post(t, base+"/api/generate", `{"model":"qwen3:8b","keep_alive":-1}`)
	if out["done_reason"] != "load" {
		t.Fatalf("load: %v", out)
	}
	post(t, base+"/api/generate", `{"model":"llama3.2"}`)
	time.Sleep(5 * time.Millisecond)
	post(t, base+"/api/generate", `{"model":"phi4"}`)
	if got := strings.Join(ps(), ","); got != "llama3.2:latest,phi4:latest" {
		t.Fatalf("max 2 loaded, least recently used out: %s", got)
	}
	_, out = post(t, base+"/api/generate", `{"model":"phi4","keep_alive":0}`)
	if out["done_reason"] != "unload" || strings.Join(ps(), ",") != "llama3.2:latest" {
		t.Fatalf("unload: %v %v", out, ps())
	}
}

func TestOpenAIStream(t *testing.T) {
	base := newTest(t, Options{Models: []string{"qwen3:8b"}})
	ls := lines(t, base+"/v1/chat/completions", `{"model":"qwen3:8b","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Explain queues"}]}`)
	if ls[len(ls)-1] != "data: [DONE]" {
		t.Fatalf("no [DONE]: %s", ls[len(ls)-1])
	}
	var usage struct {
		Choices []any
		Usage   struct {
			CompletionTokens int `json:"completion_tokens"`
		}
		Timings map[string]float64
	}
	json.Unmarshal([]byte(strings.TrimPrefix(ls[len(ls)-2], "data: ")), &usage)
	if len(usage.Choices) != 0 || usage.Usage.CompletionTokens == 0 || usage.Timings["predicted_per_second"] == 0 {
		t.Fatalf("usage chunk: %s", ls[len(ls)-2])
	}
	reasoning := false
	for _, l := range ls {
		reasoning = reasoning || strings.Contains(l, `"reasoning":`)
	}
	if !reasoning {
		t.Fatal("no reasoning deltas from a thinking model")
	}
}

func TestAbortFault(t *testing.T) {
	base := newTest(t, Options{Models: []string{"llama3.2"}, Faults: Faults{Models: map[string]ModelFaults{"llama3.2": {AbortAfter: 2}}}})
	ls := lines(t, base+"/v1/chat/completions", `{"model":"llama3.2","stream":true,"messages":[{"role":"user","content":"Explain queues in detail"}]}`)
	if len(ls) != 2 || strings.Contains(strings.Join(ls, ""), "[DONE]") {
		t.Fatalf("want 2 chunks and no [DONE], got %d: %v", len(ls), ls)
	}
	ls = lines(t, base+"/api/chat", `{"model":"llama3.2","messages":[{"role":"user","content":"Explain queues in detail"}]}`)
	if !strings.Contains(ls[len(ls)-1], `"error"`) {
		t.Fatalf("native stream should end with an error line: %v", ls[len(ls)-1])
	}
}

func TestSystemOne(t *testing.T) {
	base := newTest(t, Options{Models: []string{"kev:0.8b-q8_0", "qwen3:8b"}})
	body := `{"model":"kev:0.8b-q8_0","state":"App crashes on save","questions":{"is_bug":{"type":"noul","instructions":"Is this a software error?"},"team":{"type":"choice","criteria":["billing","technical"]},"urgency":{"type":"score","criteria":["Routine","Soon","Immediate"]}}}`
	status, out := post(t, base+"/v1/systemone", body)
	if status != 200 {
		t.Fatalf("%d %v", status, out)
	}
	answers := out["answers"].(map[string]any)
	p := answers["is_bug"].(map[string]any)["noul"].(float64)
	if p <= 0 || p >= 1 {
		t.Fatalf("noul %f", p)
	}
	sum := 0.0
	for _, v := range answers["urgency"].(map[string]any)["probabilities"].(map[string]any) {
		sum += v.(float64)
	}
	if math.Abs(sum-1) > 0.001 {
		t.Fatalf("probabilities sum to %f", sum)
	}
	if status, _ := post(t, base+"/v1/systemone", strings.Replace(body, "kev:0.8b-q8_0", "qwen3:8b", 1)); status != 400 {
		t.Fatalf("a chat model answered System One: %d", status)
	}
	if status, _ := post(t, base+"/api/generate", `{"model":"kev:0.8b-q8_0"}`); status != 400 {
		t.Fatalf("generate on a decision model: %d", status)
	}
}

func TestDescribe(t *testing.T) {
	for name, want := range map[string]struct {
		family, size string
		caps         []string
	}{
		"qwen3:8b":                 {"qwen3", "8.2B", []string{"completion", "tools", "thinking"}},
		"gpt-oss:20b":              {"gpt-oss", "20.9B", []string{"completion", "tools", "thinking"}},
		"nomic-embed-text":         {"nomic-embed-text", "137M", []string{"embedding"}},
		"gemma3:1b":                {"gemma3", "1.0B", []string{"completion"}},
		"gemma3:12b":               {"gemma3", "12.0B", []string{"completion", "vision"}},
		"qwen3-coder:30b-a3b-q8_0": {"qwen3-coder", "30.5B", []string{"completion", "tools", "insert"}},
	} {
		m := Describe(name)
		if m.Family != want.family || m.ParameterSize() != want.size || strings.Join(m.Capabilities, ",") != strings.Join(want.caps, ",") {
			t.Errorf("%s: %s %s %v", name, m.Family, m.ParameterSize(), m.Capabilities)
		}
	}
	if m := Describe("qwen3-coder:30b-a3b-q8_0"); m.Quant != "Q8_0" || m.Active != 3 {
		t.Errorf("quant %s active %f", m.Quant, m.Active)
	}
}

func TestLatencyPacesTokens(t *testing.T) {
	base := newTest(t, Options{Models: []string{"llama3.2"}, Latency: 0.05})
	start := time.Now()
	_, out := post(t, base+"/api/generate", `{"model":"llama3.2","prompt":"Explain rivers","stream":false}`)
	took := time.Since(start)
	want := time.Duration(out["total_duration"].(float64) * 0.05)
	if took < want*8/10 {
		t.Fatalf("took %s, want about %s", took, want)
	}
}

func TestContractionsAreNotTopics(t *testing.T) {
	got := strings.Join(keywords("What's the weather in Paris? There's rain and it isn't warm"), ",")
	if want := "weather,paris,rain,warm"; got != want {
		t.Errorf("keywords: got %s, want %s", got, want)
	}
}

func TestInfillKeepsLanguage(t *testing.T) {
	for src, want := range map[string]string{
		"def fibonacci(n):\n    \"\"\"Return the nth Fibonacci number.\"\"\"\n": "python",
		"package main\n\nfunc main() {\n\tx := 1\n":                             "go",
		"const total = items.reduce((a, b) => a + b, 0);\n":                     "javascript",
		"interface User { name: string }\n":                                     "typescript",
		"fn main() {\n    let mut n = 0;\n":                                     "rust",
		"SELECT name FROM users\n":                                              "sql",
		"#!/usr/bin/env bash\necho hi\n":                                        "bash",
		"def greet(name)\n  puts name\nend\n":                                   "ruby",
		"hello there\n":                                                         "",
	} {
		if got := codeLanguage(src); got != want {
			t.Errorf("codeLanguage(%q) = %q, want %q", src, got, want)
		}
	}

	// Fills for a Python prefix stay Python, whatever the seed.
	base := newTest(t, Options{Models: []string{"qwen2.5-coder:7b"}})
	for i := range 20 {
		body, _ := json.Marshal(map[string]any{"model": "qwen2.5-coder:7b", "stream": false,
			"prompt": "def fibonacci(n):\n    \"\"\"Return the nth Fibonacci number.\"\"\"\n",
			"suffix": "\n\nprint(fibonacci(" + string(rune('0'+i%10)) + strings.Repeat("0", i/10) + "))\n"})
		status, out := post(t, base+"/api/generate", string(body))
		if status != 200 {
			t.Fatalf("generate %d: %v", status, out)
		}
		middle := out["response"].(string)
		for _, sign := range []string{":=", "{", "}", ";", "const ", "func "} {
			if strings.Contains(middle, sign) {
				t.Errorf("python fill has %q: %q", sign, middle)
			}
		}
	}
}
