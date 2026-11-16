package contract

import (
	"bytes"
	"errors"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"unicode/utf8"
)

// The original8MiB JSON limit precedes key redaction and Unicode normalization.
// FE terminates a field; FD encodes literal [redacted]; FF encodes RuneError.
// All are impossible bytes in valid decoded UTF8. Two field terminators fit
// even an empty source JSON object+separator. Tokens undo only size expansion,
// not the original sanitization, so the original complete facts fit input.read.
func EncodeOrderFacts(facts []OrderFact) ([]byte, error) {
	var out []byte
	for _, fact := range facts {
		for _, text := range []string{fact.Name, fact.Slug} {
			if !utf8.ValidString(text) {
				return nil, errors.New("invalid catalogue text")
			}
			for i := 0; i < len(text); {
				if strings.HasPrefix(text[i:], "[redacted]") {
					out = append(out, 253)
					i += len("[redacted]")
				} else {
					r, n := utf8.DecodeRuneInString(text[i:])
					if r == utf8.RuneError {
						out = append(out, 255)
					} else {
						out = append(out, text[i:i+n]...)
					}
					i += n
				}
				if len(out) > rsiplugin.MaxInputBytes {
					return nil, errors.New("catalogue facts exceed input bound")
				}
			}
			out = append(out, 254)
			if len(out) > rsiplugin.MaxInputBytes {
				return nil, errors.New("catalogue facts exceed input bound")
			}
		}
	}
	return out, nil
}
func DecodeOrderFacts(raw []byte, count int) ([]OrderFact, error) {
	if len(raw) > rsiplugin.MaxInputBytes || count < 0 || count > rsiplugin.MaxInputBytes/2 {
		return nil, errors.New("invalid catalogue facts")
	}
	if len(raw) < count*2 {
		return nil, errors.New("catalogue fact count mismatch")
	}
	facts := make([]OrderFact, count)
	for i := range facts {
		values := [2]string{}
		for j := range values {
			end := bytes.IndexByte(raw, 254)
			if end < 0 {
				return nil, errors.New("catalogue fact count mismatch")
			}
			field := raw[:end]
			raw = raw[end+1:]
			text := strings.ReplaceAll(string(field), string([]byte{253}), "[redacted]")
			text = strings.ReplaceAll(text, string([]byte{255}), string(utf8.RuneError))
			if !utf8.ValidString(text) {
				return nil, errors.New("invalid catalogue text")
			}
			values[j] = text
		}
		facts[i] = OrderFact{Name: values[0], Slug: values[1]}
	}
	if len(raw) != 0 {
		return nil, errors.New("catalogue fact count mismatch")
	}
	return facts, nil
}
