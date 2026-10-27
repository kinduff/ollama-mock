package mock

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
)

// rng is splitmix64: small, fast, and the same sequence on every platform, which is the whole point.
type rng struct{ state uint64 }

func newRNG(seed uint64) *rng { return &rng{state: seed} }

func (r *rng) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *rng) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

// between returns an int in [lo, hi].
func (r *rng) between(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + r.intn(hi-lo+1)
}

func (r *rng) float() float64 { return float64(r.next()>>11) / (1 << 53) }

func (r *rng) chance(p float64) bool { return r.float() < p }

// jitter returns a factor in [1-p, 1+p].
func (r *rng) jitter(p float64) float64 { return 1 - p + 2*p*r.float() }

func pick[T any](r *rng, xs []T) T { return xs[r.intn(len(xs))] }

func hash64(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

func sha256hex(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
