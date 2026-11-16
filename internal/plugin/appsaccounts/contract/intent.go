// Package contract contains repository-private account intent facts, never the
// owning State, credentials, account IDs, Toolkit identity or authorization URL.
package contract

import (
	"encoding/binary"
	"errors"
)

const ConnectIntent = "steward.apps-connect-intent/1"
const ConnectInputID = "connect-intent"
const (
	Active byte = 1 << iota
	Pending
	Disconnected
	Public
)

type ConnectRow struct {
	Alias string
	Facts byte
}
type ConnectRequest struct {
	Count int    `json:"count"`
	Alias string `json:"alias"`
}
type ConnectPlan struct {
	Kind  string `json:"kind"`
	Alias string `json:"alias,omitempty"`
	Row   *int   `json:"row,omitempty"`
}

func EncodeConnectRows(rows []ConnectRow) []byte {
	var out []byte
	for _, row := range rows {
		out = append(out, row.Facts)
		out = binary.LittleEndian.AppendUint64(out, uint64(len(row.Alias)))
		out = append(out, row.Alias...)
	}
	return out
}
func DecodeConnectRows(raw []byte, count int) ([]ConnectRow, error) {
	if count < 0 || count > len(raw)/9 {
		return nil, errors.New("invalid connection count")
	}
	rows := make([]ConnectRow, count)
	for i := range rows {
		if len(raw) < 9 {
			return nil, errors.New("truncated connection facts")
		}
		bits := raw[0]
		status := bits & 7
		if bits&^byte(15) != 0 || status != 0 && status != Active && status != Pending && status != Disconnected {
			return nil, errors.New("invalid connection facts")
		}
		size := binary.LittleEndian.Uint64(raw[1:9])
		raw = raw[9:]
		if size > uint64(len(raw)) {
			return nil, errors.New("invalid original alias")
		}
		rows[i] = ConnectRow{Alias: string(raw[:int(size)]), Facts: bits}
		raw = raw[int(size):]
	}
	if len(raw) != 0 {
		return nil, errors.New("trailing connection facts")
	}
	return rows, nil
}
