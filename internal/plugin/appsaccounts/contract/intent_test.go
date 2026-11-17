package contract

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestAppsAccountIntentFactsPreserveExactPrivateAliasBytes(t *testing.T) {
	expected := []byte{Pending | Public, 5, 0, 0, 0, 0, 0, 0, 0, 'a', 0, 255, 206, 169, Disconnected, 0, 0, 0, 0, 0, 0, 0, 0}
	rows := []ConnectRow{{Alias: "a\x00\xffΩ", Facts: Pending | Public}, {Facts: Disconnected}}
	if got := EncodeConnectRows(rows); !reflect.DeepEqual(got, expected) {
		t.Fatal("private original framing changed", got)
	}
	got, err := DecodeConnectRows(expected, 2)
	if err != nil || !reflect.DeepEqual(got, rows) {
		t.Fatal("original aliases changed", got, err)
	}
	forged := append([]byte(nil), expected...)
	binary.LittleEndian.PutUint64(forged[1:9], ^uint64(0))
	invalidBits := append([]byte(nil), expected...)
	invalidBits[0] = Active | Pending
	for _, data := range [][]byte{expected[:8], expected[:len(expected)-1], append(append([]byte(nil), expected...), 0), forged, invalidBits} {
		if _, err := DecodeConnectRows(data, 2); err == nil {
			t.Fatal("invalid facts accepted")
		}
	}
	for _, count := range []int{-1, 0, 3} {
		if _, err := DecodeConnectRows(expected, count); err == nil {
			t.Fatal("wrong count accepted", count)
		}
	}
}
