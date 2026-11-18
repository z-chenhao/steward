package jsonwire

import (
	"encoding/json"
	"errors"
	"io"
)

// Check consumes one JSON value, with at most 32 levels, and rejects duplicate
// object names and trailing data. Callers choose how the decoder reads numbers.
func Check(d *json.Decoder) error {
	if err := unique(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func unique(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON depth limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return errors.New("duplicate JSON key")
				}
				seen[s] = true
				if err := unique(d, depth+1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := unique(d, depth+1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	return nil
}
