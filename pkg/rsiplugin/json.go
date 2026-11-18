package rsiplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/z-chenhao/assistant/internal/jsonwire"
)

// Decode rejects duplicate keys, unknown struct fields, trailing JSON and
// excessive nesting. Wire payloads never silently discard mandatory fields.
const MaxControlBytes = 12 << 20

func Decode(raw []byte, target any) error {
	return decodeBounded(raw, target, MaxFrameBytes)
}

// DecodeControl is for authenticated owner documents containing independent
// fixtures. It does not enlarge plugin RPC frames or model tool arguments.
func DecodeControl(raw []byte, target any) error { return decodeBounded(raw, target, MaxControlBytes) }

// DecodeInput decodes an explicitly scoped input after ReadInput verifies its
// byte budget and content identity. Transport frames stay separately bounded.
func DecodeInput(raw []byte, target any) error { return decodeBounded(raw, target, MaxInputBytes) }
func decodeBounded(raw []byte, target any, maxBytes int) error {
	if len(raw) > maxBytes {
		return errors.New("frame limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := jsonwire.Check(d); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(target)
}

// Canonical uses sorted object keys (encoding/json), preserves array order and
// UTF-8 strings without Unicode normalization. Number lexemes are preserved:
// 1 and 1.0 have different identities, and large integers are never rounded.
func Canonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var v any
	if err = DecodeControl(raw, &v); err != nil {
		return nil, err
	}
	var numbers func(any) error
	numbers = func(v any) error {
		switch n := v.(type) {
		case json.Number:
			if len(n) > 128 {
				return errors.New("number limit")
			}
			v, err := strconv.ParseFloat(string(n), 64)
			if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
				return errors.New("nonfinite number")
			}
		case []any:
			for _, x := range n {
				if err := numbers(x); err != nil {
					return err
				}
			}
		case map[string]any:
			for _, x := range n {
				if err := numbers(x); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err = numbers(v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func Digest(domain string, value any) (string, error) {
	b, err := Canonical(value)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(domain + "\n"))
	_, _ = h.Write(b)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
func FileDigest(value []byte) string {
	h := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(h[:])
}
