package mock

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// POST /v1/systemone: TypeSafe's System One, served by its Ollama build for decision models (kev,
// nimble, clef). Typed questions about a state, each answered with probabilities:
//
//	noul    yes or no:        {"type":"noul","noul":0.97}
//	choice  one of criteria:  {"type":"choice","choice":"technical","probabilities":{...},"confidence":0.9}
//	score   levels, lowest first: {"type":"score","score":1.6,"legend":{"0":"Routine",...},"probabilities":{...},"confidence":0.4}

type question struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria"`
}

func (q question) labels() []string {
	var list []string
	if json.Unmarshal(q.Criteria, &list) == nil {
		return list
	}
	var named map[string]any
	if json.Unmarshal(q.Criteria, &named) == nil {
		for k := range named {
			list = append(list, k)
		}
		sort.Strings(list)
	}
	return list
}

func (s *Server) handleSystemOne(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string              `json:"model"`
		State     json.RawMessage     `json:"state"`
		Questions map[string]question `json:"questions"`
		KeepAlive json.RawMessage     `json:"keep_alive"`
	}
	if err := decode(r, &body); err != nil {
		oaError(w, httpError{400, err.Error()})
		return
	}
	noteModel(r, body.Model)
	if len(body.State) == 0 || string(body.State) == "null" {
		oaError(w, httpError{400, "state is required"})
		return
	}
	if len(body.Questions) == 0 {
		oaError(w, httpError{400, "questions is required"})
		return
	}
	for name, q := range body.Questions {
		switch q.Type {
		case "noul":
		case "choice", "score":
			if len(q.labels()) < 2 {
				oaError(w, httpError{400, fmt.Sprintf("question %q: criteria needs at least two labels", name)})
				return
			}
		default:
			oaError(w, httpError{400, fmt.Sprintf("question %q: unknown type %q", name, q.Type)})
			return
		}
	}
	in, err := s.startInference(r.Context(), body.Model, body.KeepAlive, 0, "decision")
	if err != nil {
		oaError(w, err)
		return
	}
	defer s.finish(in)

	names := make([]string, 0, len(body.Questions))
	for n := range body.Questions {
		names = append(names, n)
	}
	sort.Strings(names)
	tokens := len(tokenize(string(body.State))) + 12
	answers := map[string]any{}
	for _, n := range names {
		q := body.Questions[n]
		tokens += len(tokenize(q.Instructions)) + 6
		rg := newRNG(hash64(in.m.Name, string(body.State), n, q.Type, q.Instructions, string(q.Criteria)))
		answers[n] = decide(rg, q)
	}
	took := float64(tokens) / in.m.PromptRate()
	if !s.sleep(r.Context(), time.Duration((took+0.02*float64(len(names)))*float64(time.Second))) {
		return
	}
	writeJSON(w, 200, map[string]any{
		"model": in.m.Name, "answers": answers,
		"usage": map[string]any{"input_tokens": tokens, "output_tokens": len(names)},
	})
}

func decide(r *rng, q question) map[string]any {
	if q.Type == "noul" {
		// Mostly confident, like a trained classifier: near 0 or near 1, sometimes unsure.
		var p float64
		switch u := r.float(); {
		case u < 0.42:
			p = 0.85 + 0.149*r.float()
		case u < 0.84:
			p = 0.01 + 0.14*r.float()
		default:
			p = 0.3 + 0.4*r.float()
		}
		return map[string]any{"type": "noul", "noul": round4(p)}
	}
	labels := q.labels()
	weights := make([]float64, len(labels))
	top := r.intn(len(labels))
	sum := 0.0
	for i := range weights {
		weights[i] = 0.05 + r.float()
		if i == top {
			weights[i] += 1.5 + 3*r.float()
		}
		sum += weights[i]
	}
	probs := map[string]any{}
	best, second := 0.0, 0.0
	expected := 0.0
	for i := range weights {
		p := round4(weights[i] / sum)
		key := labels[i]
		if q.Type == "score" {
			key = strconv.Itoa(i)
			expected += float64(i) * p
		}
		probs[key] = p
		if p > best {
			best, second = p, best
		} else if p > second {
			second = p
		}
	}
	confidence := round4(best - second)
	if q.Type == "choice" {
		return map[string]any{"type": "choice", "choice": labels[top], "probabilities": probs, "confidence": confidence}
	}
	legend := map[string]string{}
	for i, l := range labels {
		legend[strconv.Itoa(i)] = l
	}
	return map[string]any{"type": "score", "score": math.Round(expected*100) / 100, "legend": legend, "probabilities": probs, "confidence": confidence}
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
