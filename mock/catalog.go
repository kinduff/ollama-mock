package mock

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Model is what the mock knows about a model name: enough to answer /api/show, /api/tags and
// /api/ps the way Ollama would, and to pace generation like a model of that size.
type Model struct {
	Name            string   // normalized, always with a tag: "qwen3:8b"
	Family          string   // the catalog entry it matched: "qwen3", or "" for an unknown name
	Arch            string   // general.architecture
	Capabilities    []string // completion, tools, thinking, vision, insert, embedding, decision
	ContextLength   int      // the model's own maximum
	EmbeddingLength int      // hidden size
	Dimensions      int      // vector length, for embedding models
	Params          float64  // billions
	Active          float64  // billions active per token (MoE); equals Params for dense models
	Quant           string   // Q4_K_M, Q8_0, F16, MXFP4, ...
	Format          string   // gguf, or safetensors for an MLX tag
	Runner          string   // "mlx" for an MLX tag, else ""
	Size            int64    // bytes on disk
	Digest          string   // bare hex, as Ollama writes it
}

// Has reports whether the model has a capability.
func (m Model) Has(c string) bool { return slices.Contains(m.Capabilities, c) }

// ParameterSize is details.parameter_size: "8.2B", "596.05M".
func (m Model) ParameterSize() string {
	if m.Params < 1 {
		return strconv.FormatFloat(math.Round(m.Params*1e5)/100, 'f', -1, 64) + "M"
	}
	return fmt.Sprintf("%.1fB", m.Params)
}

// DecodeRate is tokens per second when generating, roughly an Apple M-series GPU.
func (m Model) DecodeRate() float64 { return 260 / (m.Active + 0.7) }

// PromptRate is tokens per second when reading the prompt.
func (m Model) PromptRate() float64 { return m.DecodeRate() * 14 }

// BlockCount is a plausible layer count for the size.
func (m Model) BlockCount() int {
	if m.EmbeddingLength == 0 {
		return 24
	}
	n := int(m.Params * 1e9 / (12 * float64(m.EmbeddingLength) * float64(m.EmbeddingLength)))
	return max(6, min(n, 128))
}

type family struct {
	prefix  string
	arch    string
	caps    []string
	context int
	embed   int     // hidden size
	params  float64 // for :latest
	active  float64 // MoE active params for :latest, 0 for dense
	quant   string  // default quantization
	dims    int     // vector length, embedding models
}

var (
	capsChat     = []string{"completion"}
	capsTools    = []string{"completion", "tools"}
	capsThinking = []string{"completion", "tools", "thinking"}
	capsVision   = []string{"completion", "vision"}
	capsCode     = []string{"completion", "tools", "insert"}
	capsEmbed    = []string{"embedding"}
)

// families is matched by the longest prefix of the model's base name, so "qwen3-embedding" wins
// over "qwen3". Numbers are close to the library's, not exact.
var families = []family{
	{"llama4", "llama4", []string{"completion", "vision", "tools"}, 10485760, 5120, 109, 17, "Q4_K_M", 0},
	{"llama3.3", "llama", capsTools, 131072, 8192, 70.6, 0, "Q4_K_M", 0},
	{"llama3.2-vision", "mllama", capsVision, 131072, 4096, 10.7, 0, "Q4_K_M", 0},
	{"llama3.2", "llama", capsTools, 131072, 3072, 3.2, 0, "Q4_K_M", 0},
	{"llama3.1", "llama", capsTools, 131072, 4096, 8.0, 0, "Q4_K_M", 0},
	{"llama3", "llama", capsChat, 8192, 4096, 8.0, 0, "Q4_0", 0},
	{"llama2", "llama", capsChat, 4096, 4096, 6.7, 0, "Q4_0", 0},
	{"codellama", "llama", []string{"completion", "insert"}, 16384, 4096, 6.7, 0, "Q4_0", 0},
	{"tinyllama", "llama", capsChat, 2048, 2048, 1.1, 0, "Q4_0", 0},
	{"qwen3.5", "qwen35", capsThinking, 262144, 2560, 4.0, 0, "Q4_K_M", 0},
	{"qwen3-embedding", "qwen3", capsEmbed, 32768, 1024, 0.6, 0, "Q8_0", 1024},
	{"qwen3-coder", "qwen3moe", capsCode, 262144, 2048, 30.5, 3.3, "Q4_K_M", 0},
	{"qwen3-vl", "qwen3vl", []string{"completion", "vision", "tools", "thinking"}, 262144, 4096, 8.8, 0, "Q4_K_M", 0},
	{"qwen3", "qwen3", capsThinking, 40960, 4096, 8.2, 0, "Q4_K_M", 0},
	{"qwq", "qwen2", capsThinking, 40960, 5120, 32.8, 0, "Q4_K_M", 0},
	{"qwen2.5vl", "qwen25vl", capsVision, 128000, 3584, 8.3, 0, "Q4_K_M", 0},
	{"qwen2.5-coder", "qwen2", capsCode, 32768, 3584, 7.6, 0, "Q4_K_M", 0},
	{"qwen2.5", "qwen2", capsTools, 32768, 3584, 7.6, 0, "Q4_K_M", 0},
	{"qwen2", "qwen2", capsTools, 32768, 3584, 7.6, 0, "Q4_0", 0},
	{"deepseek-r1", "qwen3", capsThinking, 131072, 4096, 8.2, 0, "Q4_K_M", 0},
	{"deepseek-v3", "deepseek2", capsChat, 163840, 7168, 671, 37, "Q4_K_M", 0},
	{"deepseek-coder-v2", "deepseek2", []string{"completion", "insert"}, 163840, 2048, 15.7, 2.4, "Q4_0", 0},
	{"gpt-oss", "gptoss", capsThinking, 131072, 2880, 20.9, 3.6, "MXFP4", 0},
	{"gemma3n", "gemma3n", capsChat, 32768, 2048, 6.9, 0, "Q4_K_M", 0},
	{"gemma3", "gemma3", capsVision, 131072, 2560, 4.3, 0, "Q4_K_M", 0},
	{"gemma2", "gemma2", capsChat, 8192, 3584, 9.2, 0, "Q4_0", 0},
	{"gemma", "gemma", capsChat, 8192, 3072, 8.5, 0, "Q4_0", 0},
	{"embeddinggemma", "gemma3", capsEmbed, 2048, 768, 0.3, 0, "BF16", 768},
	{"mistral-small", "mistral3", []string{"completion", "vision", "tools"}, 131072, 5120, 24, 0, "Q4_K_M", 0},
	{"mistral-nemo", "llama", capsTools, 1024000, 5120, 12.2, 0, "Q4_0", 0},
	{"mistral", "llama", capsTools, 32768, 4096, 7.2, 0, "Q4_K_M", 0},
	{"mixtral", "llama", capsTools, 32768, 4096, 46.7, 12.9, "Q4_0", 0},
	{"magistral", "llama", capsThinking, 40960, 5120, 23.6, 0, "Q4_K_M", 0},
	{"devstral", "llama", capsTools, 131072, 5120, 23.6, 0, "Q4_K_M", 0},
	{"phi4-mini", "phi3", capsTools, 131072, 3072, 3.8, 0, "Q4_K_M", 0},
	{"phi4", "phi3", capsChat, 16384, 5120, 14.7, 0, "Q4_K_M", 0},
	{"phi3", "phi3", capsChat, 131072, 3072, 3.8, 0, "Q4_0", 0},
	{"granite-embedding", "bert", capsEmbed, 512, 768, 0.278, 0, "F16", 768},
	{"granite", "granite", capsTools, 131072, 4096, 8.2, 0, "Q4_K_M", 0},
	{"smollm2", "llama", capsTools, 8192, 2048, 1.7, 0, "Q8_0", 0},
	{"starcoder2", "starcoder2", []string{"completion", "insert"}, 16384, 3072, 3.0, 0, "Q4_0", 0},
	{"command-r", "command-r", capsTools, 131072, 8192, 32.3, 0, "Q4_0", 0},
	{"cogito", "llama", capsTools, 131072, 4096, 8.0, 0, "Q4_K_M", 0},
	{"olmo2", "olmo2", capsChat, 4096, 4096, 7.3, 0, "Q4_K_M", 0},
	{"llava", "llama", capsVision, 32768, 4096, 7.2, 0, "Q4_0", 0},
	{"bakllava", "llama", capsVision, 32768, 4096, 7.2, 0, "Q4_0", 0},
	{"moondream", "phi2", capsVision, 2048, 2048, 1.9, 0, "Q4_0", 0},
	{"minicpm-v", "qwen2", capsVision, 32768, 3584, 7.6, 0, "Q4_0", 0},
	{"nomic-embed-text", "nomic-bert", capsEmbed, 2048, 768, 0.137, 0, "F16", 768},
	{"mxbai-embed-large", "bert", capsEmbed, 512, 1024, 0.335, 0, "F16", 1024},
	{"all-minilm", "bert", capsEmbed, 512, 384, 0.023, 0, "F16", 384},
	{"bge-m3", "bert", capsEmbed, 8192, 1024, 0.567, 0, "F16", 1024},
	{"bge-large", "bert", capsEmbed, 512, 1024, 0.335, 0, "F16", 1024},
	{"snowflake-arctic-embed2", "bert", capsEmbed, 8192, 1024, 0.568, 0, "F16", 1024},
	{"snowflake-arctic-embed", "bert", capsEmbed, 512, 1024, 0.335, 0, "F16", 1024},
	{"paraphrase-multilingual", "bert", capsEmbed, 512, 768, 0.278, 0, "F16", 768},
	// TypeSafe's System One decision models: answered through POST /v1/systemone only.
	{"kev", "qwen3", []string{"decision"}, 73728, 1024, 0.8, 0, "Q8_0", 0},
	{"nimble", "qwen3", []string{"decision"}, 73728, 1024, 0.8, 0, "Q8_0", 0},
	{"clef", "qwen3", []string{"decision"}, 73728, 1024, 0.8, 0, "Q8_0", 0},
}

// Normalize writes a name the way Ollama stores it: lowercase, no default registry or
// "library/" namespace, and a tag (":latest" when none is given).
func Normalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "registry.ollama.ai/")
	n = strings.TrimPrefix(n, "library/")
	if n == "" {
		return ""
	}
	if !strings.Contains(n[strings.LastIndex(n, "/")+1:], ":") {
		n += ":latest"
	}
	return n
}

func splitTag(name string) (base, tag string) {
	i := strings.LastIndex(name, ":")
	if i < 0 || i < strings.LastIndex(name, "/") {
		return name, "latest"
	}
	return name[:i], name[i+1:]
}

func matchFamily(short string) (family, bool) {
	best, found := family{}, false
	for _, f := range families {
		if strings.HasPrefix(short, f.prefix) && len(f.prefix) > len(best.prefix) {
			best, found = f, true
		}
	}
	return best, found
}

// unknownFamily guesses from the name when the catalog has no entry for it.
func unknownFamily(short string) family {
	f := family{arch: "llama", caps: []string{"completion"}, context: 8192, embed: 4096, params: 7, quant: "Q4_K_M"}
	switch {
	case strings.Contains(short, "embed"):
		f = family{arch: "bert", caps: capsEmbed, context: 512, embed: 768, params: 0.11, quant: "F16", dims: 768}
	case strings.Contains(short, "vision") || strings.Contains(short, "-vl") || strings.Contains(short, "llava"):
		f.caps = capsVision
	case strings.Contains(short, "coder") || strings.Contains(short, "code"):
		f.caps = capsCode
	case strings.Contains(short, "r1") || strings.Contains(short, "reason") || strings.Contains(short, "think"):
		f.caps = capsThinking
	}
	return f
}

var (
	reMoE    = regexp.MustCompile(`(?:^|[-_])(\d+)x(\d+(?:\.\d+)?)b(?:$|[-_])`)
	reActive = regexp.MustCompile(`-a(\d+(?:\.\d+)?)b(?:$|[-_])`)
	reParams = regexp.MustCompile(`(?:^|[-_])e?(\d+(?:\.\d+)?)([bm])(?:$|[-_])`)
	reQuant  = regexp.MustCompile(`(?i)(?:^|[-_])(q[2-8](?:_k)?(?:_[a-z0-9]+)?|iq[1-4]_[a-z]+|fp16|f16|bf16|fp32|mxfp4)(?:$|[-_])`)
)

func parseParams(tag string, f family) (params, active float64) {
	if m := reMoE.FindStringSubmatch(tag); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		each, _ := strconv.ParseFloat(m[2], 64)
		return n * each * 0.78, each * 1.9
	}
	if m := reParams.FindStringSubmatch(tag); m != nil {
		params, _ = strconv.ParseFloat(m[1], 64)
		if m[2] == "m" {
			params /= 1000
		}
		if params == math.Trunc(params) && f.params-params >= 0 && f.params-params < 1 {
			params = f.params // "8b" is the library's 8.2B
		}
	} else {
		params = f.params
	}
	switch {
	case reActive.MatchString(tag):
		active, _ = strconv.ParseFloat(reActive.FindStringSubmatch(tag)[1], 64)
	case f.active > 0 && params == f.params:
		active = f.active
	case f.active > 0:
		active = f.active * math.Sqrt(params/f.params)
	default:
		active = params
	}
	return params, active
}

func parseQuant(tag, fallback string) string {
	m := reQuant.FindStringSubmatch(tag)
	if m == nil {
		return fallback
	}
	q := strings.ToUpper(m[1])
	if q == "FP16" {
		q = "F16"
	}
	if q == "FP32" {
		q = "F32"
	}
	return q
}

// bytesPerParam for a quantization, including the usual overhead.
func bytesPerParam(q string) float64 {
	switch {
	case strings.HasPrefix(q, "Q2"), strings.HasPrefix(q, "IQ1"), strings.HasPrefix(q, "IQ2"):
		return 0.36
	case strings.HasPrefix(q, "Q3"), strings.HasPrefix(q, "IQ3"):
		return 0.47
	case q == "Q4_0", strings.HasPrefix(q, "IQ4"):
		return 0.57
	case strings.HasPrefix(q, "Q4"):
		return 0.61
	case q == "MXFP4":
		return 0.62
	case strings.HasPrefix(q, "Q5"):
		return 0.71
	case strings.HasPrefix(q, "Q6"):
		return 0.82
	case strings.HasPrefix(q, "Q8"):
		return 1.07
	case q == "F32":
		return 4
	default: // F16, BF16
		return 2
	}
}

// fileType is general.file_type for a quantization (llama.cpp's enum).
func fileType(q string) int {
	switch q {
	case "F32":
		return 0
	case "F16":
		return 1
	case "Q4_0":
		return 2
	case "Q8_0":
		return 7
	case "Q2_K":
		return 10
	case "Q3_K_M":
		return 12
	case "Q4_K_S":
		return 14
	case "Q4_K_M":
		return 15
	case "Q5_K_M":
		return 17
	case "Q6_K":
		return 18
	case "BF16":
		return 32
	default:
		return 15
	}
}

// Describe returns what the mock knows about any model name. It never fails: a name missing from
// the catalog gets a plausible description guessed from the name.
func Describe(name string) Model {
	name = Normalize(name)
	base, tag := splitTag(name)
	short := base[strings.LastIndex(base, "/")+1:]
	f, known := matchFamily(short)
	if !known {
		f = unknownFamily(short)
	}
	params, active := parseParams(tag, f)
	m := Model{
		Name: name, Arch: f.arch, Capabilities: slices.Clone(f.caps), ContextLength: f.context,
		EmbeddingLength: f.embed, Dimensions: f.dims, Params: params, Active: active,
		Quant: parseQuant(tag, f.quant), Format: "gguf",
	}
	if known {
		m.Family = f.prefix
	}
	// Sizes in one family differ in shape too.
	switch {
	case m.Family == "gemma3" && params < 4:
		m.Capabilities, m.ContextLength = capsChat, 32768
	case m.Family == "qwen3-embedding" && params > 5:
		m.Dimensions, m.EmbeddingLength = 4096, 4096
	case m.Family == "qwen3-embedding" && params > 1:
		m.Dimensions, m.EmbeddingLength = 2560, 2560
	case m.Family == "granite-embedding" && params < 0.1:
		m.Dimensions, m.EmbeddingLength = 384, 384
	case m.Dimensions == 0 && f.params > 0 && params != f.params:
		m.EmbeddingLength = roundTo(float64(f.embed)*math.Pow(params/f.params, 0.4), 256)
	}
	if strings.Contains(tag, "mlx") {
		m.Format, m.Runner = "safetensors", "mlx"
	}
	r := newRNG(hash64("size", name))
	bytes := params * 1e9 * bytesPerParam(m.Quant) * r.jitter(0.03)
	if m.Has("vision") {
		bytes += 0.6e9 * r.jitter(0.2)
	}
	m.Size = int64(bytes) + int64(r.between(2_000_000, 9_000_000))
	m.Digest = sha256hex("ollama-mock", "model", name)
	return m
}

func roundTo(x float64, step int) int {
	n := int(math.Round(x/float64(step))) * step
	return max(n, step)
}
