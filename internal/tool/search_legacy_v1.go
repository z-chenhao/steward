package tool

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"unicode"
)

// SearchToolsLegacyV1 serves native embeddings (including native Skill catalogs),
// selected prior23 installations, and retained pre-opt-in Runs/descendants.
// Remove only after these consumers retire; managed Runs cannot fall back here.
// It ranks frozen, unactivated capabilities. BM25, identifier terms,
// namespace hints and exact-name preference follow Pi 1.5.0's discovery model.
func SearchToolsLegacyV1(specs []Spec, loaded []string, query string, limit int) SearchResult {
	active := map[string]bool{}
	for _, name := range loaded {
		active[name] = true
	}
	type document struct {
		spec            Spec
		name, namespace map[string]bool
		local           []string
		text            map[string]int
		length          int
	}
	set := func(terms []string) map[string]bool {
		out := map[string]bool{}
		for _, term := range terms {
			out[term] = true
		}
		return out
	}
	docs := []document{}
	average := 0.0
	for _, spec := range specs {
		if !spec.Searchable() || active[spec.Name] {
			continue
		}
		text := spec.Description
		var schema any
		if json.Unmarshal([]byte(spec.InputSchema), &schema) == nil {
			text += " " + schemaSearchText(schema)
		}
		terms := searchTerms(text)
		counts := map[string]int{}
		for _, term := range terms {
			counts[term]++
		}
		parts := strings.Split(spec.Name, "__")
		docs = append(docs, document{spec: spec, name: set(searchTerms(spec.Name)), namespace: set(searchTerms(spec.Provider + " " + spec.NamespaceDescription)), local: searchTerms(parts[len(parts)-1]), text: counts, length: len(terms)})
		average += float64(len(terms))
	}
	result := SearchResult{Loaded: []string{}, Tools: []ToolMatch{}}
	terms := set(searchTerms(query))
	if len(docs) == 0 || len(terms) == 0 || limit < 1 {
		return result
	}
	average = math.Max(1, average/float64(len(docs)))
	orderedTerms := make([]string, 0, len(terms))
	for term := range terms {
		orderedTerms = append(orderedTerms, term)
	}
	sort.Strings(orderedTerms)
	idf := map[string]float64{}
	bonus := 0.0
	for _, term := range orderedTerms {
		frequency := 0
		for _, d := range docs {
			if d.name[term] || d.text[term] > 0 || d.namespace[term] {
				frequency++
			}
		}
		idf[term] = math.Log(1 + (float64(len(docs)-frequency)+0.5)/(float64(frequency)+0.5))
		bonus += idf[term]
	}
	type match struct {
		spec    Spec
		score   float64
		covered int
		exact   bool
	}
	matches := []match{}
	for _, d := range docs {
		score, covered := 0.0, 0
		norm := 1.2 * (0.25 + 0.75*float64(d.length)/average)
		for _, term := range orderedTerms {
			count := float64(d.text[term])
			weight := count * 2.2 / (count + norm)
			if d.name[term] {
				weight += 3
			}
			if d.namespace[term] {
				weight += 0.5
			}
			if weight > 0 {
				covered++
			}
			score += idf[term] * weight
		}
		if score == 0 {
			continue
		}
		local := len(d.local) > 0
		for _, term := range d.local {
			local = local && terms[term]
		}
		if local {
			score += bonus
		}
		name, exactQuery := strings.ToLower(d.spec.Name), strings.ToLower(strings.TrimSpace(query))
		matches = append(matches, match{d.spec, score, covered, name == exactQuery || strings.HasSuffix(name, "__"+exactQuery)})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.exact != b.exact {
			return a.exact
		}
		if a.covered != b.covered {
			return a.covered > b.covered
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.spec.Name < b.spec.Name
	})
	for _, m := range matches[:min(limit, len(matches))] {
		line := strings.SplitN(strings.TrimSpace(m.spec.Description), "\n", 2)[0]
		if len([]rune(line)) > 256 {
			line = string([]rune(line)[:256]) + "…"
		}
		if !m.spec.CodeModeOnly {
			result.Loaded = append(result.Loaded, m.spec.Name)
		}
		result.Tools = append(result.Tools, ToolMatch{Name: m.spec.Name, Description: line})
	}
	return result
}

func schemaSearchText(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	parts := []string{}
	if description, ok := object["description"].(string); ok {
		parts = append(parts, description)
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		keys := make([]string, 0, len(properties))
		for name := range properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			parts = append(parts, name, schemaSearchText(properties[name]))
		}
	}
	parts = append(parts, schemaSearchText(object["items"]))
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if variants, ok := object[key].([]any); ok {
			for _, v := range variants {
				parts = append(parts, schemaSearchText(v))
			}
		}
	}
	return strings.Join(parts, " ")
}

func searchTerms(text string) []string {
	var expanded strings.Builder
	var previous rune
	for _, r := range text {
		if unicode.IsUpper(r) && (unicode.IsLower(previous) || unicode.IsDigit(previous)) {
			expanded.WriteByte(' ')
		}
		expanded.WriteRune(unicode.ToLower(r))
		previous = r
	}
	terms := strings.FieldsFunc(expanded.String(), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := []string{}
	for _, term := range terms {
		switch term {
		case "a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "in", "is", "it", "of", "on", "or", "that", "the", "this", "to", "was", "with":
			continue
		}
		if len(term) > 4 && strings.HasSuffix(term, "ies") {
			term = strings.TrimSuffix(term, "ies") + "y"
		} else if len(term) > 4 && (strings.HasSuffix(term, "ches") || strings.HasSuffix(term, "shes") || strings.HasSuffix(term, "sses") || strings.HasSuffix(term, "xes") || strings.HasSuffix(term, "zes")) {
			term = strings.TrimSuffix(term, "es")
		} else if len(term) > 3 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss") {
			term = strings.TrimSuffix(term, "s")
		}
		out = append(out, term)
		// CJK character terms let a Chinese capability query match descriptions
		// without a separate tokenizer dependency.
		for _, r := range term {
			if unicode.Is(unicode.Han, r) {
				out = append(out, string(r))
			}
		}
	}
	return out
}
