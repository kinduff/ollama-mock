//go:build js && wasm

// Command ollama-mock-wasm runs the mock inside a web page. Instead of listening on a port it
// serves requests handed to it from JavaScript; site/public/mock.js wraps that as fetch.
//
//	__ollamaMock.serve(method, path, headers, body, onHead, onChunk, onEnd) -> abort()
//	__ollamaMock.configure({latency, models}) resets the mock with new options
//	__ollamaMock.onLog = (line) => ... gets the server's request log
package main

import (
	"bytes"
	"context"
	"net/http"
	"syscall/js"

	"github.com/kinduff/ollama-mock/mock"
)

var server = mock.New(options(js.Undefined()))

func main() {
	js.Global().Set("__ollamaMock", map[string]any{
		"serve":     js.FuncOf(serve),
		"configure": js.FuncOf(configure),
	})
	select {}
}

// Models installed at start: one per tab of the page, so each works before anything is pulled.
var models = []string{"qwen3:8b", "llama3.2", "gemma3:4b", "nomic-embed-text", "kev", "qwen2.5-coder:7b"}

// options reads {latency, models} from JavaScript; anything missing keeps the page's defaults.
// Other models have to be pulled first, as with Ollama. Pulls run at 400 MB/s so a demo doesn't
// wait a minute for one.
func options(v js.Value) mock.Options {
	opts := mock.Options{Latency: 1, Models: models, PullSpeed: 400e6, Log: logWriter{}}
	if v.Type() != js.TypeObject {
		return opts
	}
	if l := v.Get("latency"); l.Type() == js.TypeNumber {
		opts.Latency = l.Float()
	}
	if m := v.Get("models"); m.Type() == js.TypeObject {
		opts.Models = nil
		for i := range m.Length() {
			opts.Models = append(opts.Models, m.Index(i).String())
		}
	}
	return opts
}

// logWriter hands each request log line to __ollamaMock.onLog, when the page set one.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	if f := js.Global().Get("__ollamaMock").Get("onLog"); f.Type() == js.TypeFunction {
		f.Invoke(string(p))
	}
	return len(p), nil
}

func configure(this js.Value, args []js.Value) any {
	var v js.Value
	if len(args) > 0 {
		v = args[0]
	}
	server = mock.New(options(v))
	return nil
}

// serve runs one request in a goroutine (a callback must not block) and streams the response
// back: onHead(status, headers) once, onChunk(bytes) per write, then onEnd(error or null). It
// returns a function that cancels the request, as a client hanging up would.
func serve(this js.Value, args []js.Value) any {
	method, path, headers, body := args[0].String(), args[1].String(), args[2], args[3]
	onHead, onChunk, onEnd := args[4], args[5], args[6]

	var b []byte
	if body.Truthy() {
		b = make([]byte, body.Get("length").Int())
		js.CopyBytesToGo(b, body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(b))
	if err != nil {
		cancel()
		onEnd.Invoke(err.Error())
		return js.FuncOf(func(js.Value, []js.Value) any { return nil })
	}
	if headers.Type() == js.TypeObject {
		keys := js.Global().Get("Object").Call("keys", headers)
		for i := range keys.Length() {
			k := keys.Index(i).String()
			req.Header.Set(k, headers.Get(k).String())
		}
	}

	abort := js.FuncOf(func(js.Value, []js.Value) any {
		cancel()
		return nil
	})
	go func() {
		defer cancel()
		w := &writer{header: http.Header{}, onHead: onHead, onChunk: onChunk}
		server.ServeHTTP(w, req)
		w.WriteHeader(200)
		onEnd.Invoke(js.Null())
	}()
	return abort
}

// writer hands every write to JavaScript as it happens, so streams arrive token by token.
type writer struct {
	header          http.Header
	sent            bool
	onHead, onChunk js.Value
}

func (w *writer) Header() http.Header { return w.header }

func (w *writer) WriteHeader(status int) {
	if w.sent {
		return
	}
	w.sent = true
	h := map[string]any{}
	for k := range w.header {
		h[k] = w.header.Get(k)
	}
	w.onHead.Invoke(status, h)
}

func (w *writer) Write(p []byte) (int, error) {
	w.WriteHeader(200)
	chunk := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(chunk, p)
	w.onChunk.Invoke(chunk)
	return len(p), nil
}

// Flush is a no-op: Write already delivered everything.
func (w *writer) Flush() {}
