package mock

import (
	"math"
	"strings"
	"unicode"
)

// embedVector is a deterministic, L2-normalized vector for a text. Words and character trigrams
// are hashed into the dimensions, so texts sharing words point the same way: similar texts score a
// higher cosine than unrelated ones, which is what tests of search and ranking need. A direction
// shared by every text of a model keeps unrelated texts around 0.3, as with real embedding models.
func embedVector(model, text string, dims int) []float32 {
	if dims <= 0 {
		dims = 768
	}
	features := make([]float64, dims)
	add := func(key string, weight float64) {
		h := hash64(model, key)
		i := int(h % uint64(dims))
		if h>>63 == 1 {
			weight = -weight
		}
		features[i] += weight
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, wd := range words {
		add("w:"+stem(wd), 1)
		padded := []rune(" " + wd + " ")
		for i := 0; i+3 <= len(padded); i++ {
			add("g:"+string(padded[i:i+3]), 0.3)
		}
	}
	normalize(features)

	shared := newRNG(hash64("shared", model))
	own := newRNG(hash64("own", model, text))
	v := make([]float64, dims)
	for i := range v {
		v[i] = 0.9*features[i] + (0.55*gauss(shared)+0.08*gauss(own))/math.Sqrt(float64(dims))
	}
	normalize(v)
	out := make([]float32, dims)
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

// truncateVector keeps the first n dimensions and normalizes again, as Ollama's `dimensions` does.
func truncateVector(v []float32, n int) []float32 {
	if n <= 0 || n >= len(v) {
		return v
	}
	f := make([]float64, n)
	for i := range f {
		f[i] = float64(v[i])
	}
	normalize(f)
	out := make([]float32, n)
	for i, x := range f {
		out[i] = float32(x)
	}
	return out
}

func stem(w string) string {
	for _, s := range []string{"ing", "ed", "es", "s"} {
		if len(w) > len(s)+3 && strings.HasSuffix(w, s) {
			return w[:len(w)-len(s)]
		}
	}
	return w
}

func normalize(v []float64) {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return
	}
	n := math.Sqrt(sum)
	for i := range v {
		v[i] /= n
	}
}

// gauss is a standard normal sample (Box-Muller).
func gauss(r *rng) float64 {
	u1 := math.Max(r.float(), 1e-12)
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*r.float())
}
