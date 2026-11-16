// Package contract is the private frozen-authorized tool metadata seam.
package contract

import (
	"encoding/binary"
	"errors"
)

const PluginID = "steward.tool-discovery"
const Contract = "steward.tool-discovery/1"
const InputID = "tool-metadata"

type Request struct {
	Count int `json:"count"`
	Limit int `json:"limit"`
}

// No Spec,permissions,scope,credentials,loaded flags or callable object is shared.
type Candidate struct{ Name, Description, Schema, Namespace, NamespaceDescription string }
type Facts struct {
	Query      string
	Candidates []Candidate
}
type Entry struct {
	Index    int  `json:"index"`
	Offset   int  `json:"offset"`
	Length   int  `json:"length"`
	Ellipsis bool `json:"ellipsis"`
}
type Plan struct {
	Entries []Entry `json:"entries"`
}

// Original metadata has no catalogue-wide byte cap. This private length-delimited
// encoding preserves original strings and streams under one exact Host descriptor.
func Encode(f Facts) []byte {
	var raw []byte
	appendString := func(v string) { raw = binary.LittleEndian.AppendUint64(raw, uint64(len(v))); raw = append(raw, v...) }
	appendString(f.Query)
	for _, c := range f.Candidates {
		for _, v := range []string{c.Name, c.Description, c.Schema, c.Namespace, c.NamespaceDescription} {
			appendString(v)
		}
	}
	return raw
}
func Decode(raw []byte, count int) (Facts, error) {
	bad := errors.New("invalid tool metadata input")
	if count < 0 || len(raw) < 8 || count > (len(raw)-8)/40 {
		return Facts{}, bad
	}
	take := func() (string, error) {
		if len(raw) < 8 {
			return "", bad
		}
		n := binary.LittleEndian.Uint64(raw[:8])
		raw = raw[8:]
		if n > uint64(len(raw)) {
			return "", bad
		}
		v := string(raw[:int(n)])
		raw = raw[int(n):]
		return v, nil
	}
	var f Facts
	var err error
	if f.Query, err = take(); err != nil {
		return Facts{}, err
	}
	f.Candidates = make([]Candidate, count)
	for i := range f.Candidates {
		c := &f.Candidates[i]
		for _, field := range []*string{&c.Name, &c.Description, &c.Schema, &c.Namespace, &c.NamespaceDescription} {
			if *field, err = take(); err != nil {
				return Facts{}, err
			}
		}
	}
	if len(raw) != 0 {
		return Facts{}, bad
	}
	return f, nil
}
