package contract

import (
	"encoding/binary"
	"errors"
	"io"
)

var ErrMetadata = errors.New("invalid immutable artifact metadata")

// Length-delimited records preserve the original strings, including bytes that
// JSON would replace. There is no catalogue-wide count or byte ceiling here.
func EncodeSearch(rows []SearchRow) []byte {
	var raw []byte
	for _, row := range rows {
		raw = AppendSearchRow(raw, row)
	}
	return raw
}

// AppendSearchRow uses the frozen wire encoding for one candidate.
func AppendSearchRow(raw []byte, row SearchRow) []byte {
	raw = binary.LittleEndian.AppendUint64(raw, uint64(row.CreatedAt))
	for _, value := range []string{row.Name, row.MediaType} {
		raw = binary.LittleEndian.AppendUint64(raw, uint64(len(value)))
		raw = append(raw, value...)
	}
	return raw
}
func ReadSearchRow(input io.Reader) (SearchRow, error) {
	var row SearchRow
	var stamp uint64
	if err := binary.Read(input, binary.LittleEndian, &stamp); err != nil {
		return row, ErrMetadata
	}
	row.CreatedAt = int64(stamp)
	for i, field := range []*string{&row.Name, &row.MediaType} {
		var size uint64
		if err := binary.Read(input, binary.LittleEndian, &size); err != nil || size > []uint64{255, 128}[i] {
			return row, ErrMetadata
		}
		raw := make([]byte, int(size))
		if _, err := io.ReadFull(input, raw); err != nil {
			return row, ErrMetadata
		}
		*field = string(raw)
	}
	return row, nil
}
func EncodeCleanup(rows []CleanupRow) []byte {
	var raw []byte
	for _, row := range rows {
		raw = binary.LittleEndian.AppendUint64(raw, uint64(row.ExpiresAt))
	}
	return raw
}
func ReadCleanupRow(input io.Reader) (CleanupRow, error) {
	var stamp uint64
	if err := binary.Read(input, binary.LittleEndian, &stamp); err != nil {
		return CleanupRow{}, ErrMetadata
	}
	return CleanupRow{ExpiresAt: int64(stamp)}, nil
}
func End(input io.Reader) error {
	var extra [1]byte
	n, err := input.Read(extra[:])
	if n != 0 || err != io.EOF {
		return ErrMetadata
	}
	return nil
}
