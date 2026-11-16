package contract

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestAppsAuthConfigFactsExactBytesAndRejectInvalidMetadata(t *testing.T) {
	expected := []byte{AuthToolkit | AuthEnabled, 2, 0, 0, 0, 0, 0, 0, 0, 'i', 'd', 5, 0, 0, 0, 0, 0, 0, 0, 'A', 0, 255, 206, 169}
	rows := []AuthRow{{ID: "id", Created: "A\x00\xffΩ", Facts: AuthToolkit | AuthEnabled}}
	if got := EncodeAuthRows(rows); !reflect.DeepEqual(got, expected) {
		t.Fatal("metadata framing changed", got)
	}
	if got, err := DecodeAuthRows(expected, 1); err != nil || !reflect.DeepEqual(got, rows) {
		t.Fatal("original metadata changed", got, err)
	}
	forged := append([]byte(nil), expected...)
	binary.LittleEndian.PutUint64(forged[1:9], ^uint64(0))
	badBits := append([]byte(nil), expected...)
	badBits[0] = 128
	for _, raw := range [][]byte{expected[:8], expected[:len(expected)-1], append(append([]byte(nil), expected...), 0), forged, badBits} {
		if _, err := DecodeAuthRows(raw, 1); err == nil {
			t.Fatal("malformed metadata accepted")
		}
	}
	for _, count := range []int{-1, 0, 2} {
		if _, err := DecodeAuthRows(expected, count); err == nil {
			t.Fatal("wrong count accepted")
		}
	}
}
