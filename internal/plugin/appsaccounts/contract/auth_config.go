package contract

import (
	"encoding/binary"
	"errors"
)

const AuthConfig = "steward.apps-auth-config/1"
const AuthConfigInputID = "auth-config"
const (
	AuthToolkit byte = 1 << iota
	AuthEnabled
	AuthRouter
	AuthUnmanaged
	AuthValidID
	AuthExplicitMatch
)

type AuthRow struct {
	ID, Created string
	Facts       byte
}
type AuthRequest struct {
	Step       string `json:"step"`
	Count      int    `json:"count"`
	Page       int    `json:"page"`
	Explicit   bool   `json:"explicit"`
	Saved      bool   `json:"saved"`
	Override   bool   `json:"override"`
	NoHosted   bool   `json:"no_hosted"`
	HasNext    bool   `json:"has_next"`
	CursorSeen bool   `json:"cursor_seen"`
	Status     int    `json:"status"`
}
type AuthPlan struct {
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Row    *int   `json:"row,omitempty"`
}

func EncodeAuthRows(rows []AuthRow) []byte {
	var out []byte
	for _, row := range rows {
		out = append(out, row.Facts)
		for _, value := range []string{row.ID, row.Created} {
			out = binary.LittleEndian.AppendUint64(out, uint64(len(value)))
			out = append(out, value...)
		}
	}
	return out
}
func DecodeAuthRows(raw []byte, count int) ([]AuthRow, error) {
	if count < 0 || count > len(raw)/17 {
		return nil, errors.New("invalid auth metadata count")
	}
	rows := make([]AuthRow, count)
	for i := range rows {
		if len(raw) < 1 {
			return nil, errors.New("truncated auth facts")
		}
		rows[i].Facts = raw[0]
		raw = raw[1:]
		if rows[i].Facts&^byte(63) != 0 {
			return nil, errors.New("invalid auth facts")
		}
		for _, field := range []*string{&rows[i].ID, &rows[i].Created} {
			if len(raw) < 8 {
				return nil, errors.New("truncated auth metadata")
			}
			size := binary.LittleEndian.Uint64(raw[:8])
			raw = raw[8:]
			if size > uint64(len(raw)) {
				return nil, errors.New("invalid auth metadata")
			}
			*field = string(raw[:int(size)])
			raw = raw[int(size):]
		}
	}
	if len(raw) != 0 {
		return nil, errors.New("trailing auth metadata")
	}
	return rows, nil
}
