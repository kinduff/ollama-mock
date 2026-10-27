// Package mock is a fake Ollama server: the native /api endpoints, the OpenAI-compatible /v1
// endpoints and System One's /v1/systemone, answering for any model name with deterministic,
// varied output and realistic shapes, sizes and timings.
//
//	srv := mock.New(mock.Options{Models: []string{"qwen3:8b"}, Latency: 0})
//	ts := httptest.NewServer(srv)
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options configure a Server. The zero value is a usable, instant server with no models.
type Options struct {
	// Models installed at start (and after a reset), as if pulled.
	Models []string
	// AnyModel serves any name without a pull, installing it on first use. Off, an unknown model
	// answers 404 "try pulling it first", as Ollama does.
	AnyModel bool
	// Latency scales the waits: 1 sleeps as long as a real model would take (load, prompt, each
	// token, downloads), 0 never sleeps, 0.1 is ten times faster. Reported durations are always
	// the realistic ones.
	Latency float64
	// MaxLoaded is how many models stay loaded at once (OLLAMA_MAX_LOADED_MODELS); the least
	// recently used is unloaded to make room. Default 3.
	MaxLoaded int
	// Parallel is how many requests one model answers at once (OLLAMA_NUM_PARALLEL); the rest
	// wait. 0 or less: no limit.
	Parallel int
	// KeepAlive is how long a model stays loaded after its last request (OLLAMA_KEEP_ALIVE).
	// Default 5m; negative: forever.
	KeepAlive time.Duration
	// ContextLength is the context a model loads with when a request doesn't ask
	// (OLLAMA_CONTEXT_LENGTH). Default 4096.
	ContextLength int
	// PullSpeed is the simulated download speed in bytes per second. Default 100 MB/s.
	PullSpeed float64
	// Version answers /api/version. Default 0.40.6.
	Version string
	// FaultRate makes this share of generations fail mid-stream, picked by the request's hash, so
	// the same request always fails the same way.
	FaultRate float64
	// Faults per model and missing pulls. Can be replaced at runtime with PUT /_mock/faults.
	Faults Faults
	// Clock, when set, stamps responses (created_at, modified_at, created) instead of the wall
	// clock, for byte-for-byte reproducible output. Expiry still uses the wall clock.
	Clock func() time.Time
	// Log, when set, gets one line per request.
	Log io.Writer
}

// Faults inject failures.
type Faults struct {
	Models map[string]ModelFaults `json:"models,omitempty"`
	// Missing names fail to pull with "file does not exist".
	Missing []string `json:"missing,omitempty"`
}

// ModelFaults are the failures of one model.
type ModelFaults struct {
	// AbortAfter ends every stream after this many tokens: an error line on the native API, a
	// stream without [DONE] on the OpenAI API, a 500 when not streaming.
	AbortAfter int `json:"abort_after,omitempty"`
	// LoadError fails every load with this message (500).
	LoadError string `json:"load_error,omitempty"`
	// PullError fails a pull with this message after "pulling manifest".
	PullError string `json:"pull_error,omitempty"`
	// DeleteError fails DELETE /api/delete with this message (500) and keeps the model.
	DeleteError string `json:"delete_error,omitempty"`
	// Status and Error answer every inference request with this status and message.
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

type installed struct {
	Model
	Modified   time.Time
	System     string
	Template   string
	Parameters map[string]any
	Parent     string
}

type runner struct {
	numCtx   int
	expires  time.Time // zero: never
	lastUsed time.Time
	size     int64
}

// RequestRecord is one request, as GET /_mock/requests lists them.
type RequestRecord struct {
	Time     time.Time `json:"time"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Model    string    `json:"model,omitempty"`
	Status   int       `json:"status"`
	Duration float64   `json:"duration_ms"`
}

// Server is a fake Ollama. It is an http.Handler.
type Server struct {
	opts Options
	mux  *http.ServeMux

	mu      sync.Mutex
	models  map[string]*installed
	loaded  map[string]*runner
	blobs   map[string]bool
	faults  Faults
	slots   map[string]chan struct{}
	history []RequestRecord
}

// New returns a server with opts' models installed.
func New(opts Options) *Server {
	if opts.MaxLoaded <= 0 {
		opts.MaxLoaded = 3
	}
	if opts.KeepAlive == 0 {
		opts.KeepAlive = 5 * time.Minute
	}
	if opts.ContextLength <= 0 {
		opts.ContextLength = 4096
	}
	if opts.PullSpeed <= 0 {
		opts.PullSpeed = 100e6
	}
	if opts.Version == "" {
		opts.Version = "0.40.6"
	}
	s := &Server{opts: opts, mux: http.NewServeMux()}
	s.reset()
	s.routes()
	return s
}

func (s *Server) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = map[string]*installed{}
	s.loaded = map[string]*runner{}
	s.blobs = map[string]bool{}
	s.slots = map[string]chan struct{}{}
	s.history = nil
	s.faults = normalizeFaults(s.opts.Faults)
	for _, name := range s.opts.Models {
		if m := Describe(name); m.Name != "" {
			s.models[m.Name] = &installed{Model: m, Modified: s.stamp()}
		}
	}
}

// stamp is the time written into responses.
func (s *Server) stamp() time.Time {
	if s.opts.Clock != nil {
		return s.opts.Clock()
	}
	return time.Now()
}

type ctxKey struct{}

// ServeHTTP records the request and routes it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &RequestRecord{Time: start, Method: r.Method, Path: r.URL.Path}
	sw := &statusWriter{ResponseWriter: w, status: 200}
	s.mux.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, rec)))
	rec.Status = sw.status
	rec.Duration = float64(time.Since(start).Microseconds()) / 1000
	if strings.HasPrefix(r.URL.Path, "/_mock/") {
		return
	}
	s.mu.Lock()
	s.history = append(s.history, *rec)
	if len(s.history) > 1000 {
		s.history = s.history[len(s.history)-1000:]
	}
	s.mu.Unlock()
	if s.opts.Log != nil {
		model := rec.Model
		if model != "" {
			model = " " + model
		}
		fmt.Fprintf(s.opts.Log, "%s %s %s%s %d %s\n", start.Format("15:04:05"), r.Method, r.URL.Path, model, sw.status,
			time.Since(start).Round(time.Millisecond))
	}
}

func noteModel(r *http.Request, name string) {
	if rec, ok := r.Context().Value(ctxKey{}).(*RequestRecord); ok {
		rec.Model = name
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "Ollama is running") })
	m.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"version": s.opts.Version})
	})
	m.HandleFunc("GET /api/tags", s.handleTags)
	m.HandleFunc("GET /api/ps", s.handlePS)
	m.HandleFunc("POST /api/show", s.handleShow)
	m.HandleFunc("POST /api/pull", s.handlePull)
	m.HandleFunc("POST /api/push", s.handlePush)
	m.HandleFunc("POST /api/create", s.handleCreate)
	m.HandleFunc("POST /api/copy", s.handleCopy)
	m.HandleFunc("DELETE /api/delete", s.handleDelete)
	m.HandleFunc("HEAD /api/blobs/{digest}", s.handleBlobHead)
	m.HandleFunc("POST /api/blobs/{digest}", s.handleBlobPost)
	m.HandleFunc("POST /api/generate", s.handleGenerate)
	m.HandleFunc("POST /api/chat", s.handleChat)
	m.HandleFunc("POST /api/embed", s.handleEmbed)
	m.HandleFunc("POST /api/embeddings", s.handleEmbeddings)

	m.HandleFunc("GET /v1/models", s.handleOAModels)
	m.HandleFunc("GET /v1/models/{model...}", s.handleOAModel)
	m.HandleFunc("POST /v1/chat/completions", s.handleOAChat)
	m.HandleFunc("POST /v1/completions", s.handleOACompletion)
	m.HandleFunc("POST /v1/embeddings", s.handleOAEmbeddings)

	m.HandleFunc("GET /_mock/state", s.handleMockState)
	m.HandleFunc("GET /_mock/requests", s.handleMockRequests)
	m.HandleFunc("POST /_mock/reset", func(w http.ResponseWriter, r *http.Request) {
		s.reset()
		writeJSON(w, 200, map[string]any{"status": "reset"})
	})
	m.HandleFunc("PUT /_mock/faults", s.handleMockFaults)
}

// MARK: models

var errNotFound = fmt.Errorf("not found")

// lookup finds an installed model, or installs it when AnyModel is on.
func (s *Server) lookup(name string) (*installed, error) {
	n := Normalize(name)
	if n == "" {
		return nil, errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.models[n]; ok {
		return m, nil
	}
	if !s.opts.AnyModel {
		return nil, errNotFound
	}
	m := &installed{Model: Describe(n), Modified: s.stamp()}
	s.models[n] = m
	return m, nil
}

func (s *Server) modelFaults(name string) ModelFaults {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.faults.Models[Normalize(name)]
}

// expire unloads models whose keep-alive ran out. Callers hold s.mu.
func (s *Server) expire() {
	now := time.Now()
	for name, r := range s.loaded {
		if !r.expires.IsZero() && now.After(r.expires) {
			delete(s.loaded, name)
		}
	}
}

// kvBytes is the memory a context of n tokens takes next to the weights.
func kvBytes(m Model, n int) int64 {
	perToken := 131072.0 * math.Pow(max(m.Params, 0.05)/8, 0.6)
	if m.Has("embedding") {
		perToken /= 8
	}
	return int64(perToken*float64(n)) + 180_000_000
}

// loadTime is how long a load takes: reading the weights and setting up the context.
func loadTime(m Model, numCtx int) time.Duration {
	secs := 0.25 + float64(m.Size)/2.2e9 + float64(numCtx)/400000
	return time.Duration(secs * float64(time.Second))
}

// keepAlive parses keep_alive: a number of seconds or a duration ("5m", "-1"). Absent: nil.
func parseKeepAlive(raw json.RawMessage) (*time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var d time.Duration
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		d = time.Duration(n * float64(time.Second))
	} else {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return nil, fmt.Errorf("invalid keep_alive")
		}
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			d = time.Duration(f * float64(time.Second))
		} else if d, err = time.ParseDuration(str); err != nil {
			return nil, fmt.Errorf("time: invalid duration %q", str)
		}
	}
	return &d, nil
}

// load makes a model resident (loading it again when the context differs) and returns how long
// the load took, 0 when it already was.
func (s *Server) load(ctx context.Context, m *installed, keepAlive *time.Duration, numCtx int) (time.Duration, error) {
	if f := s.modelFaults(m.Name); f.LoadError != "" {
		return 0, httpError{500, f.LoadError}
	}
	if numCtx <= 0 {
		numCtx = s.opts.ContextLength
	}
	numCtx = min(numCtx, m.ContextLength)
	s.mu.Lock()
	s.expire()
	r, ok := s.loaded[m.Name]
	need := !ok || r.numCtx != numCtx
	var took time.Duration
	if need {
		took = loadTime(m.Model, numCtx)
		delete(s.loaded, m.Name)
		for len(s.loaded) >= s.opts.MaxLoaded {
			var oldest string
			for name, x := range s.loaded {
				if oldest == "" || x.lastUsed.Before(s.loaded[oldest].lastUsed) {
					oldest = name
				}
			}
			delete(s.loaded, oldest)
		}
	}
	s.mu.Unlock()
	if need && !s.sleep(ctx, took) {
		return 0, ctx.Err()
	}
	s.touch(m, keepAlive, numCtx)
	return took, nil
}

// touch sets a loaded model's expiry from the request's keep_alive.
func (s *Server) touch(m *installed, keepAlive *time.Duration, numCtx int) {
	ka := s.opts.KeepAlive
	if keepAlive != nil {
		ka = *keepAlive
	}
	if numCtx <= 0 {
		numCtx = min(s.opts.ContextLength, m.ContextLength)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ka == 0 {
		delete(s.loaded, m.Name)
		return
	}
	r := s.loaded[m.Name]
	if r == nil {
		r = &runner{numCtx: numCtx, size: m.Size + kvBytes(m.Model, numCtx)}
		s.loaded[m.Name] = r
	}
	r.lastUsed = time.Now()
	r.expires = time.Time{}
	if ka > 0 {
		r.expires = time.Now().Add(ka)
	}
}

func (s *Server) unload(name string) {
	s.mu.Lock()
	delete(s.loaded, name)
	s.mu.Unlock()
}

// slot waits for a free place to run a request on the model (Options.Parallel).
func (s *Server) slot(ctx context.Context, name string) (func(), error) {
	if s.opts.Parallel <= 0 {
		return func() {}, nil
	}
	s.mu.Lock()
	ch := s.slots[name]
	if ch == nil {
		ch = make(chan struct{}, s.opts.Parallel)
		s.slots[name] = ch
	}
	s.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// sleep waits d scaled by Latency; false when the request went away first.
func (s *Server) sleep(ctx context.Context, d time.Duration) bool {
	if s.opts.Latency <= 0 || d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(time.Duration(float64(d) * s.opts.Latency))
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) sortedModels() []*installed {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*installed, 0, len(s.models))
	for _, m := range s.models {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].Modified.Equal(list[j].Modified) {
			return list[i].Modified.After(list[j].Modified)
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// MARK: helpers

type httpError struct {
	status  int
	message string
}

func (e httpError) Error() string { return e.message }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	b, _ := json.Marshal(v)
	w.Write(b)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func decode(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return io.EOF
	}
	return json.Unmarshal(b, v)
}

func (s *Server) handleMockState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.expire()
	installedNames := make([]string, 0, len(s.models))
	for n := range s.models {
		installedNames = append(installedNames, n)
	}
	loadedNames := make([]string, 0, len(s.loaded))
	for n := range s.loaded {
		loadedNames = append(loadedNames, n)
	}
	faults := s.faults
	s.mu.Unlock()
	slices.Sort(installedNames)
	slices.Sort(loadedNames)
	writeJSON(w, 200, map[string]any{"installed": installedNames, "loaded": loadedNames, "faults": faults})
}

func (s *Server) handleMockRequests(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	list := slices.Clone(s.history)
	s.mu.Unlock()
	if list == nil {
		list = []RequestRecord{}
	}
	writeJSON(w, 200, map[string]any{"requests": list})
}

func (s *Server) handleMockFaults(w http.ResponseWriter, r *http.Request) {
	var f Faults
	if err := decode(r, &f); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	f = normalizeFaults(f)
	s.mu.Lock()
	s.faults = f
	s.mu.Unlock()
	writeJSON(w, 200, f)
}

func normalizeFaults(f Faults) Faults {
	models := map[string]ModelFaults{}
	for k, v := range f.Models {
		models[Normalize(k)] = v
	}
	missing := make([]string, len(f.Missing))
	for i, n := range f.Missing {
		missing[i] = Normalize(n)
	}
	return Faults{Models: models, Missing: missing}
}
