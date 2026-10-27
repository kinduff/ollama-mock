// Command ollama-mock runs a fake Ollama server: same endpoints and shapes, deterministic answers
// for any model, no GPU and no downloads.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kinduff/ollama-mock/mock"
)

func main() {
	var (
		addr      = flag.String("addr", "", "listen address (default: $OLLAMA_HOST, else 127.0.0.1:11434)")
		models    = flag.String("models", "", "comma-separated models installed at start, e.g. qwen3:8b,nomic-embed-text")
		anyModel  = flag.Bool("any", false, "serve any model name without a pull")
		latency   = flag.Float64("latency", 1, "time scale: 1 waits like a real model, 0 never waits, 0.1 is 10x faster")
		maxLoaded = flag.Int("max-loaded", 3, "models loaded at once (OLLAMA_MAX_LOADED_MODELS)")
		parallel  = flag.Int("parallel", 1, "requests one model answers at once (OLLAMA_NUM_PARALLEL), 0 for no limit")
		keepAlive = flag.Duration("keep-alive", 5*time.Minute, "how long a model stays loaded (OLLAMA_KEEP_ALIVE), negative for forever")
		ctxLen    = flag.Int("context", 4096, "default context length (OLLAMA_CONTEXT_LENGTH)")
		pullSpeed = flag.Float64("pull-speed", 100, "simulated download speed, MB/s")
		version   = flag.String("version", "0.40.6", "version /api/version reports")
		faultRate = flag.Float64("fault-rate", 0, "share of generations that fail mid-stream (0 to 1), picked by request hash")
		faultFile = flag.String("faults", "", "JSON file with per-model faults (see README)")
		fixedTime = flag.String("fixed-time", "", "stamp responses with this RFC 3339 time instead of the clock")
		quiet     = flag.Bool("quiet", false, "don't log requests")
	)
	flag.Parse()

	opts := mock.Options{
		AnyModel: *anyModel, Latency: *latency, MaxLoaded: *maxLoaded, Parallel: *parallel,
		KeepAlive: *keepAlive, ContextLength: *ctxLen, PullSpeed: *pullSpeed * 1e6, Version: *version,
		FaultRate: *faultRate,
	}
	if *keepAlive < 0 {
		opts.KeepAlive = -1
	}
	for _, m := range strings.Split(*models, ",") {
		if m = strings.TrimSpace(m); m != "" {
			opts.Models = append(opts.Models, m)
		}
	}
	if *faultFile != "" {
		b, err := os.ReadFile(*faultFile)
		if err != nil {
			fatal(err)
		}
		if err := json.Unmarshal(b, &opts.Faults); err != nil {
			fatal(fmt.Errorf("%s: %w", *faultFile, err))
		}
	}
	if *fixedTime != "" {
		t, err := time.Parse(time.RFC3339, *fixedTime)
		if err != nil {
			fatal(fmt.Errorf("-fixed-time: %w", err))
		}
		opts.Clock = func() time.Time { return t }
	}
	if !*quiet {
		opts.Log = os.Stderr
	}

	listen := hostAddr(*addr)
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "ollama-mock %s listening on %s (latency %g, %d models installed)\n", *version, ln.Addr(), *latency, len(opts.Models))
	srv := &http.Server{Handler: mock.New(opts)}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

// hostAddr reads an address the way Ollama reads OLLAMA_HOST: "host", "host:port" or a URL.
func hostAddr(flagValue string) string {
	v := flagValue
	if v == "" {
		v = os.Getenv("OLLAMA_HOST")
	}
	if v == "" {
		return "127.0.0.1:11434"
	}
	if u, err := url.Parse(v); err == nil && u.Host != "" {
		v = u.Host
	}
	if _, _, err := net.SplitHostPort(v); err != nil {
		v = net.JoinHostPort(v, "11434")
	}
	return v
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ollama-mock:", err)
	os.Exit(1)
}
