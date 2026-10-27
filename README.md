# ollama-mock

A fake Ollama server for when you want to test against Ollama without actually running models.

ollama-mock speaks the same API on the same port, accepts any model name from the Ollama library, and gives you answers that are:

- **Deterministic.** The same request always gets the same answer, byte for byte if you want, so tests don't flake.
- **Varied.** Different prompts get different answers: prose, lists, tables, code, tool calls, JSON, thinking. You aren't testing against a single canned string.
- **Realistic in shape.** Model sizes, capabilities, context lengths, load times, token speeds and error messages follow what Ollama does for that model.

It doesn't need a GPU, doesn't download anything, and has no dependencies. It's a single Go binary.

## Install

With Go 1.24 or newer:

```sh
go install github.com/kinduff/ollama-mock/cmd/ollama-mock@latest
```

Or from a clone:

```sh
git clone https://github.com/kinduff/ollama-mock
cd ollama-mock
go build -o ollama-mock ./cmd/ollama-mock
```

## Try it

Start it with a few models installed:

```sh
ollama-mock -models qwen3:8b,llama3.2,nomic-embed-text
```

It listens on `127.0.0.1:11434`, the same as Ollama. If you already have the real thing running, pick another port with `-addr 127.0.0.1:11555`.

Ask it something and watch it stream:

```sh
curl -N localhost:11434/api/chat -d '{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "How do I keep a sourdough starter alive?"}]
}'
```

qwen3 is a thinking model, so you'll see `thinking` tokens first and then the answer, at about the speed an 8B model runs on a laptop. Run the same request again and you'll get the same answer.

Pull a model you don't have yet. Any name from the library works, and you get the progress stream:

```sh
curl -N localhost:11434/api/pull -d '{"model": "gemma3:4b"}'
```

The official `ollama` CLI works against it too, since it's only an HTTP client:

```sh
OLLAMA_HOST=127.0.0.1:11434 ollama run llama3.2 "Write a Go function that retries HTTP requests"
OLLAMA_HOST=127.0.0.1:11434 ollama ps
```

So does anything that speaks the OpenAI API. Point it at `http://localhost:11434/v1` with any API key.

## Use it in tests

### As a process

For CI you usually don't want to wait like a real model, so turn the delays off:

```sh
ollama-mock -latency 0 -models qwen3:8b,nomic-embed-text &
```

`-latency` is a time scale: `1` (the default) waits as long as a real model would, `0` never waits, and `0.1` is ten times faster. The durations in responses (`total_duration`, `eval_duration` and so on) always report the realistic numbers, whatever scale you run at.

If you'd rather not care about which models are installed, `-any` serves any name without a pull.

### From Go

The server is a plain `http.Handler`, so it drops into `httptest`:

```go
import "github.com/kinduff/ollama-mock/mock"

func TestSomething(t *testing.T) {
	srv := httptest.NewServer(mock.New(mock.Options{
		Models: []string{"qwen3:8b", "nomic-embed-text"},
	}))
	defer srv.Close()

	// point your client at srv.URL
}
```

`mock.Options` has the same knobs as the flags below. `mock.Describe("gpt-oss:20b")` tells you what the mock thinks a model is (family, size, capabilities, context length), which is handy for asserting against.

### Flags

| Flag | Default | What it does |
|---|---|---|
| `-addr` | `$OLLAMA_HOST` or `127.0.0.1:11434` | Where to listen |
| `-models` | | Models installed at start, comma-separated |
| `-any` | off | Serve any model name without a pull |
| `-latency` | `1` | Time scale for every wait (load, prompt, tokens, downloads) |
| `-max-loaded` | `3` | Models loaded at once; the least recently used is evicted |
| `-parallel` | `1` | Requests a single model handles at once; the rest queue |
| `-keep-alive` | `5m` | How long a model stays loaded after its last request |
| `-context` | `4096` | Context length when a request doesn't set `num_ctx` |
| `-pull-speed` | `100` | Download speed for pulls, in MB/s |
| `-fault-rate` | `0` | Share of generations that fail mid-stream |
| `-faults` | | JSON file with per-model failures (see below) |
| `-fixed-time` | | Stamp every response with this RFC 3339 time |
| `-version` | `0.40.6` | What `/api/version` reports |
| `-quiet` | off | Don't log requests |

## API support

### Native API

| Endpoint | Notes |
|---|---|
| `POST /api/chat` | Streaming or not. `tools`, `think` (bool or `low`/`medium`/`high`), `format` (`"json"` or a schema), `images`, `options` (`num_predict`, `stop`, `seed`, `num_ctx`), `keep_alive` |
| `POST /api/generate` | Same as chat, plus `system`, `suffix` (fill-in-the-middle), `raw` and `context` |
| `POST /api/embed` | String or list input, `truncate`, `dimensions`, `options.num_ctx` |
| `POST /api/embeddings` | The older single-prompt version |
| `GET /api/tags` | Installed models with digest, size and details |
| `GET /api/ps` | Loaded models with VRAM size, context length and `expires_at` |
| `POST /api/show` | Capabilities, details, `model_info`, template, parameters, modelfile |
| `POST /api/pull` | Streams progress per layer at `-pull-speed` |
| `POST /api/push` | Needs a namespaced name like `you/model`, as on ollama.com |
| `POST /api/create` | From an existing model, with `system`, `template`, `parameters` and `quantize` |
| `POST /api/copy`, `DELETE /api/delete` | |
| `HEAD`/`POST /api/blobs/:digest` | |
| `GET /api/version`, `GET /` | |

A chat or generate request with nothing to generate loads the model, and with `keep_alive: 0` unloads it. The response says which in `done_reason` (`load` or `unload`), as Ollama does.

### OpenAI-compatible API

| Endpoint | Notes |
|---|---|
| `POST /v1/chat/completions` | Streaming with `stream_options.include_usage`, `tools`, `response_format` (`json_object` and `json_schema`), `reasoning_effort`, `max_tokens`, `stop`, `seed`. Reasoning arrives in `reasoning` deltas |
| `POST /v1/completions` | Including `suffix` |
| `POST /v1/embeddings` | Float or `base64`, `dimensions` |
| `GET /v1/models`, `GET /v1/models/:model` | |

The last streamed chunk carries `usage` and Ollama's `timings` object (prompt and decode tokens per second), the same as a real server.

### System One

`POST /v1/systemone` answers typed decision questions (`noul`, `choice`, `score`) for decision models like `kev`, `nimble` and `clef`. Each answer comes back with probabilities that sum to 1.

### Errors

Things a model can't do fail the way Ollama fails them, with the same status codes and messages:

- an uninstalled model: `404 model "x" not found, try pulling it first`
- generating with an embedding model: `400 "nomic-embed-text:latest" does not support generate`
- tools on a model without tool support: `400 registry.ollama.ai/library/gemma2:latest does not support tools`
- `think` on a model that can't think: `400 "gemma2:latest" does not support thinking`

## How answers are made

Each answer is seeded by a hash of everything that shapes it: the model, the messages, the system prompt, the tools, the format, `think` and `seed`. Change any of those and you get a different answer. `num_predict`, `max_tokens` and `stop` are left out of the hash on purpose, so a truncated answer is always a prefix of the full one, which is also how a real model behaves.

From there:

- **Shape follows the prompt.** A greeting gets a greeting, a yes/no question gets a short answer, and "briefly" gets one sentence. Asking for code, a comparison or a list gets a code block, a table or a list. Anything else is some mix of paragraphs, headings and bullets.
- **Echo prompts work.** `Say "pong"` answers exactly `pong`, which is handy for health checks.
- **Words come from your prompt.** Its subjects become the topic. Names and numbers end up in tool arguments, so "What's the weather in Paris?" calls `get_weather` with `city: "Paris"`, and "a table for 4" sets `party_size: 4`.
- **Tools get called about 70% of the time** when you offer them, using whichever tool's name and description best match the prompt. Once there's a tool result in the conversation, the model answers in text instead of calling again.
- **Structured output** produces JSON that matches your schema: types, required fields, enums, min/max, arrays and common string formats.
- **Thinking models think** unless you pass `think: false`. `low` and `high` change how much.
- **Embeddings** are normalized vectors with the model's real dimension count. They're built from the words and character trigrams of the input, so texts that share words land close together. In practice similar sentences score around 0.5 cosine and unrelated ones around 0.25, which is enough to test search and ranking code.

The text itself reads like plausible filler, not like a smart model. That's intentional: it's there to exercise your code, not to be right.

### Models

Model metadata comes from a built-in catalog of about 60 library families (llama, qwen, gemma, mistral, phi, deepseek, gpt-oss, embedding models, vision models, and more), matched by name. The tag sets the rest. `qwen3-coder:30b-a3b-q8_0`, for example, comes out as a 30.5B MoE with 3B active parameters at Q8_0, sized and paced to match.

Names the catalog doesn't know still work. The mock guesses from the words in them: `embed` makes an embedding model, `vision` or `-vl` a vision model, `coder` a code model, and `r1` or `think` a thinking model.

## Breaking things on purpose

Your code should survive a model dying mid-answer, so you can make that happen. Write a faults file:

```json
{
  "models": {
    "qwen3:8b": { "abort_after": 5 },
    "llama3.2": { "load_error": "out of memory" },
    "gemma3:4b": { "status": 503, "error": "server busy, please try again" },
    "phi4": { "delete_error": "remove phi4: operation not permitted" }
  },
  "missing": ["does-not-exist:1b"]
}
```

and pass it with `-faults faults.json`.

- `abort_after` cuts every stream after that many tokens. The native API ends with an error line, the OpenAI API ends without `[DONE]`, and non-streaming requests get a 500.
- `load_error`, `pull_error` and `delete_error` fail those operations.
- `status` and `error` answer every inference request with that status.
- Names under `missing` fail to pull with `file does not exist`.

For random-looking failures, use `-fault-rate 0.05`. The requests that fail are chosen by hash, so it's always the same ones, and you can still reproduce a failure.

## Poking at it while it runs

These endpoints aren't part of Ollama's API. They're there for tests that need to inspect or reset the mock.

| Endpoint | What it does |
|---|---|
| `GET /_mock/state` | Installed models, loaded models and current faults |
| `GET /_mock/requests` | The last 1,000 requests: method, path, model, status and duration |
| `PUT /_mock/faults` | Replace the faults (same JSON as the file) |
| `POST /_mock/reset` | Back to the starting models, with no loads and no history |

## What it doesn't do

- Answers aren't correct, only plausible.
- Images are counted, not looked at.
- Prompt caching, GPU placement and `OLLAMA_MAX_QUEUE` aren't simulated.
- State lives in memory and is gone when the process stops.
