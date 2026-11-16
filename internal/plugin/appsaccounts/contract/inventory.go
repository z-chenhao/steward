package contract

import (
	"encoding/binary"
	"errors"
)

// Inventory is private to the independent owner account-management view.
// Input contains display labels and public toolkit slugs, never account IDs.
const Inventory = "steward.apps-account-inventory/1"
const InventoryInputID = "account-inventory"
const InventoryPageSize = 1024
const RevocationConfirmed byte = 1 << 4

type InventoryRow struct {
	Slug, Name string
	Facts      byte
}
type InventoryRequest struct {
	Count     int  `json:"count"`
	Offset    int  `json:"offset"`
	Connected bool `json:"connected"`
}
type InventoryItem struct {
	Row     int  `json:"row"`
	Active  *int `json:"active,omitempty"`
	Pending *int `json:"pending,omitempty"`
}
type InventoryPlan struct {
	Total int             `json:"total"`
	Items []InventoryItem `json:"items"`
}

// Length framing preserves original display bytes without JSON expansion.
func EncodeInventoryRows(rows []InventoryRow) []byte {
	var out []byte
	for _, row := range rows {
		out = append(out, row.Facts)
		for _, s := range []string{row.Slug, row.Name} {
			out = binary.LittleEndian.AppendUint64(out, uint64(len(s)))
			out = append(out, s...)
		}
	}
	return out
}
func DecodeInventoryRows(raw []byte, count int) ([]InventoryRow, error) {
	bad := errors.New("invalid account inventory facts")
	if count < 0 || count > len(raw)/17 {
		return nil, bad
	}
	rows := make([]InventoryRow, count)
	for i := range rows {
		if len(raw) < 17 {
			return nil, bad
		}
		bits := raw[0]
		raw = raw[1:]
		status := bits & 7
		if bits&^byte(31) != 0 || status != 0 && status != Active && status != Pending && status != Disconnected {
			return nil, bad
		}
		rows[i].Facts = bits
		for _, dest := range []*string{&rows[i].Slug, &rows[i].Name} {
			if len(raw) < 8 {
				return nil, bad
			}
			n := binary.LittleEndian.Uint64(raw)
			raw = raw[8:]
			if n > uint64(len(raw)) {
				return nil, bad
			}
			*dest = string(raw[:int(n)])
			raw = raw[int(n):]
		}
	}
	if len(raw) != 0 {
		return nil, bad
	}
	return rows, nil
}
