package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	TypeSafeSystemOne   API = "typesafe-system-one"
	CloudflareSystemOne API = "cloudflare-workers-ai-system-one"
	LlamaCPPClassify    API = "llama-cpp-classify"
	OpenAIDecisions     API = "openai-decisions"
)

type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria"` // map[string]string for choice/bool; []string for score.
}
type ClassifierContext struct {
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
	Images    []Content           `json:"images,omitempty"`
}
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Probability   float64            `json:"probability,omitempty"`
}
type ClassifierResult struct {
	API       API               `json:"api"`
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	Answers   map[string]Answer `json:"answers"`
	Usage     Usage             `json:"usage"`
	Timestamp int64             `json:"timestamp"`
}

func questionNames(in ClassifierContext) []string {
	names := make([]string, 0, len(in.Questions))
	for id := range in.Questions {
		names = append(names, id)
	}
	sort.Strings(names)
	return names
}
func criteria(q Question) (map[string]string, []string, error) {
	b, err := json.Marshal(q.Criteria)
	if err != nil {
		return nil, nil, ErrIncompatible
	}
	if q.Type == "score" {
		var levels []string
		if json.Unmarshal(b, &levels) != nil || len(levels) < 2 {
			return nil, nil, ErrIncompatible
		}
		return nil, levels, nil
	}
	var choices map[string]string
	if json.Unmarshal(b, &choices) != nil {
		return nil, nil, ErrIncompatible
	}
	if q.Type != "bool" && q.Type != "choice" {
		return nil, nil, ErrIncompatible
	}
	if q.Type == "choice" && len(choices) < 2 {
		return nil, nil, ErrIncompatible
	}
	return choices, nil, nil
}
func (c *Client) Classify(ctx context.Context, m Model, in ClassifierContext, o Options) (ClassifierResult, error) {
	result := ClassifierResult{API: m.API, Provider: m.Provider, Model: m.ID, Answers: map[string]Answer{}, Timestamp: time.Now().UnixMilli()}
	if len(in.Questions) == 0 {
		return result, ErrIncompatible
	}
	for _, q := range in.Questions {
		if _, _, err := criteria(q); err != nil {
			return result, err
		}
	}
	if m.API == LlamaCPPClassify {
		return c.classifyLlama(ctx, m, in, o)
	}
	path := "/systemone"
	body := map[string]any{"model": m.ID, "state": in.State}
	questions := map[string]any{}
	names := questionNames(in)
	for _, id := range names {
		q := in.Questions[id]
		kind := q.Type
		if kind == "bool" {
			kind = "noul"
		}
		questions[id] = map[string]any{"type": kind, "instructions": q.Instructions, "criteria": q.Criteria}
	}
	body["questions"] = questions
	switch m.API {
	case TypeSafeSystemOne, CloudflareSystemOne:
		if len(in.Images) > 0 {
			if err := classifierImages(m, in, &body, &o); err != nil {
				return result, err
			}
		}
		if m.API == CloudflareSystemOne {
			path = "/run"
			delete(body, "model")
			body = map[string]any{"model": m.ID, "input": body}
		}
	case OpenAIDecisions:
		path = "/decisions"
		state, err := json.Marshal(in.State)
		if err != nil {
			return result, ErrIncompatible
		}
		var input any = string(state)
		if len(in.Images) > 128 {
			return result, ErrIncompatible
		}
		if len(in.Images) > 0 {
			parts := []any{map[string]any{"type": "input_text", "text": string(state)}}
			for _, img := range in.Images {
				parts = append(parts, map[string]any{"type": "input_image", "image_url": imageURL(img)})
			}
			input = []any{map[string]any{"role": "user", "content": parts}}
		}
		qs := []any{}
		for _, id := range names {
			q := in.Questions[id]
			options, levels, _ := criteria(q)
			w := map[string]any{"type": q.Type, "name": id, "instructions": q.Instructions}
			switch q.Type {
			case "choice":
				keys := sortedKeys(options)
				choices := []any{}
				for _, key := range keys {
					entry := map[string]any{"value": key}
					if options[key] != "" {
						entry["description"] = options[key]
					}
					choices = append(choices, entry)
				}
				w["choices"] = choices
			case "score":
				entries := []any{}
				for _, level := range levels {
					entries = append(entries, map[string]any{"label": level})
				}
				w["levels"] = entries
			case "bool":
				w["type"] = "predicate"
				text := q.Instructions
				meaning := []string{}
				if options["true"] != "" {
					meaning = append(meaning, "True means: "+options["true"])
				}
				if options["false"] != "" {
					meaning = append(meaning, "False means: "+options["false"])
				}
				if len(meaning) > 0 {
					text += "\n\n" + strings.Join(meaning, "\n")
				}
				w["instructions"] = text
			}
			qs = append(qs, w)
		}
		body = map[string]any{"model": m.ID, "input": input, "questions": qs}
	default:
		return result, ErrIncompatible
	}
	response, err := c.JSON(ctx, m, http.MethodPost, path, body, o)
	if err != nil {
		return result, err
	}
	if m.API == CloudflareSystemOne {
		if response["success"] == false {
			return result, ErrInvalidResponse
		}
		response = record(response["result"])
		if response["answers"] == nil {
			if response["state"] != "Completed" {
				return result, ErrInvalidResponse
			}
			response = record(response["result"])
		}
	}
	if u := record(response["usage"]); len(u) > 0 {
		result.Usage = Usage{Input: integer(u["input_tokens"]), Output: integer(u["output_tokens"]), Known: true}
		CalculateCost(m, &result.Usage)
	}
	answers := record(response["answers"])
	if m.API == OpenAIDecisions {
		answers = map[string]any{}
		for _, v := range list(response["answers"]) {
			a := record(v)
			id := str(a["name"])
			if id == "" || answers[id] != nil {
				return result, ErrInvalidResponse
			}
			answers[id] = a
		}
	}
	parsed := map[string]Answer{}
	for _, id := range names {
		q := in.Questions[id]
		a := record(answers[id])
		answer, err := parseAnswer(q, a, m.API)
		if err != nil {
			return result, err
		}
		parsed[id] = answer
	}
	result.Answers = parsed
	return result, nil
}
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func number(v any) (float64, error) {
	var f float64
	switch n := v.(type) {
	case json.Number:
		var err error
		f, err = n.Float64()
		if err != nil {
			return 0, ErrInvalidResponse
		}
	case float64:
		f = n
	default:
		return 0, ErrInvalidResponse
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, ErrInvalidResponse
	}
	return f, nil
}
func parseAnswer(q Question, a map[string]any, api API) (Answer, error) {
	answer := Answer{Type: q.Type}
	expected := q.Type
	if q.Type == "bool" {
		expected = "noul"
		if api == OpenAIDecisions {
			expected = "predicate"
		}
	}
	if a["type"] != expected {
		return answer, ErrInvalidResponse
	}
	var err error
	switch q.Type {
	case "bool":
		field := "noul"
		if api == OpenAIDecisions {
			field = "probability"
		}
		answer.Probability, err = number(a[field])
	case "score":
		answer.Score, err = number(a["score"])
		if err == nil {
			answer.Confidence, err = number(a["confidence"])
		}
	case "choice":
		answer.Choice = str(a["choice"])
		if answer.Choice == "" {
			return answer, ErrInvalidResponse
		}
		answer.Confidence, err = number(a["confidence"])
		if err != nil {
			return answer, err
		}
		values := record(a["probabilities"])
		if api == OpenAIDecisions {
			values = map[string]any{}
			for _, entry := range list(a["probabilities"]) {
				v := record(entry)
				values[str(v["value"])] = v["probability"]
			}
		}
		if len(values) == 0 {
			return answer, ErrInvalidResponse
		}
		answer.Probabilities = map[string]float64{}
		for k, v := range values {
			f, e := number(v)
			if e != nil {
				return answer, e
			}
			answer.Probabilities[k] = f
		}
	}
	return answer, err
}
func classifierImages(m Model, in ClassifierContext, body *map[string]any, o *Options) error {
	supports := false
	for _, kind := range m.Input {
		supports = supports || kind == "image"
	}
	if !supports {
		return ErrIncompatible
	}
	format := stringCompat(m, "imageInput", "")
	if format == "" {
		format = "assets"
		if m.Provider == "openrouter" || strings.Contains(m.BaseURL, "openrouter.ai") {
			format = "state-image-urls"
		}
		if strings.HasPrefix(m.ID, "@cf/cloudflare/clef") {
			format = "images"
		}
	}
	request := *body
	switch format {
	case "images":
		if len(in.Images) > 4 {
			return ErrIncompatible
		}
		images := []string{}
		for _, img := range in.Images {
			if img.MIMEType != "image/png" && img.MIMEType != "image/jpeg" && img.MIMEType != "image/webp" {
				return ErrIncompatible
			}
			images = append(images, imageURL(img))
		}
		request["images"] = images
	case "state-image-urls":
		state := []any{}
		for _, img := range in.Images {
			state = append(state, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL(img)}})
		}
		request["state"] = append(state, in.State)
	case "assets":
		assets := map[string]any{}
		state := []any{}
		for i, img := range in.Images {
			format := map[string]string{"image/png": "png", "image/jpeg": "jpeg"}[img.MIMEType]
			if format == "" {
				return ErrIncompatible
			}
			key := fmtImageKey(i)
			assets[key] = map[string]any{"type": "image", "format": format, "data_base64": img.Data}
			state = append(state, key)
		}
		request["assets"] = assets
		request["state"] = append(state, in.State)
	default:
		return ErrIncompatible
	}
	if boolCompat(m, "sendImageOptInHeader", m.API == TypeSafeSystemOne && format == "assets") {
		headers := map[string]string{"TypeSafe-Danger-Zone-Images": "true"}
		for k, v := range o.Headers {
			headers[k] = v
		}
		o.Headers = headers
	}
	return nil
}

func fmtImageKey(i int) string { return "<image-" + strconv.Itoa(i+1) + ">" }
