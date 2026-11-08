package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// ResolveCodeMemory expands only known CodeMode text blocks and direct memory
// envelopes (or arrays of envelopes). Arbitrary business objects/strings remain
// verbatim, including duplicate keys and large integer representations.
func ResolveCodeMemory(raw string, resolve func(MemoryText) (string, error)) (string, bool, error) {
	return rewriteCodeMemory(raw, func(m MemoryText) (string, error) {
		plain, err := resolve(m)
		if err != nil {
			return "", err
		}
		if MemoryEncodedBytes(plain) != m.EncodedBytes || !json.Valid([]byte(plain)) {
			return "", ModelIncompatible
		}
		return plain, nil
	})
}

// SummaryCodeMemory retains every non-memory result in a mixed CodeMode frame.
// Only the recognized encrypted subvalues become reopening hints.
func SummaryCodeMemory(raw string) (string, bool, error) {
	return rewriteCodeMemory(raw, func(m MemoryText) (string, error) {
		v := map[string]any{"memory_content_omitted": true}
		if m.Ref != "" {
			v["memory_ref"] = m.Ref
		} else {
			v["reopen"] = "memory_manage list"
		}
		b, e := json.Marshal(v)
		return string(b), e
	})
}

func rewriteCodeMemory(raw string, replace func(MemoryText) (string, error)) (string, bool, error) {
	var blocks []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Console bool   `json:"console,omitempty"`
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&blocks) != nil || d.Decode(new(any)) != io.EOF || len(blocks) == 0 {
		return raw, false, nil
	}
	changed := false
	var value func([]byte, int) ([]byte, bool, error)
	value = func(raw []byte, depth int) ([]byte, bool, error) {
		if depth > 4 {
			return raw, false, nil
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal(raw, &envelope) == nil && len(envelope) == 1 && envelope["memory_text"] != nil {
			var m MemoryText
			d := json.NewDecoder(bytes.NewReader(envelope["memory_text"]))
			d.DisallowUnknownFields()
			if d.Decode(&m) != nil || m.CodeMode || !strings.HasPrefix(m.Sealed, "sealed:v1:") {
				return raw, false, nil
			}
			plain, err := replace(m)
			if err != nil {
				return nil, false, err
			}
			return []byte(plain), true, nil
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil || list == nil {
			return raw, false, nil
		}
		parts := make([]string, len(list))
		found := false
		for i, item := range list {
			next, ok, err := value(item, depth+1)
			if err != nil {
				return nil, false, err
			}
			found = found || ok
			parts[i] = string(next)
		}
		if !found {
			return raw, false, nil
		}
		return []byte("[" + strings.Join(parts, ",") + "]"), true, nil
	}
	for i, b := range blocks {
		if b.Type != "text" {
			return raw, false, nil
		}
		next, ok, err := value([]byte(b.Text), 0)
		if err != nil {
			return "", false, err
		}
		if ok {
			blocks[i].Text = string(next)
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	body, err := json.Marshal(blocks)
	return string(body), true, err
}
