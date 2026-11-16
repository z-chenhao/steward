package contract

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestInventoryOrderFactsExactOriginalBytesAndRejectMalformedInput(t *testing.T) {
	original := []InventoryOrderFact{{Name: "A\x00\xffΩ", Slug: "private\\name"}, {Name: "", Slug: ""}}
	// Expected framing is fixed independently of the encoder.
	expected := []byte{5, 0, 0, 0, 0, 0, 0, 0, 'A', 0, 255, 206, 169, 12, 0, 0, 0, 0, 0, 0, 0, 'p', 'r', 'i', 'v', 'a', 't', 'e', '\\', 'n', 'a', 'm', 'e'}
	expected = append(expected, make([]byte, 16)...)
	encoded := EncodeInventoryOrderFacts(original)
	if !reflect.DeepEqual(encoded, expected) {
		t.Fatal("original private bytes/framing changed", encoded)
	}
	decoded, err := DecodeInventoryOrderFacts(expected, 2)
	if err != nil || !reflect.DeepEqual(decoded, original) {
		t.Fatal("original private fields changed", decoded, err)
	}
	forged := append([]byte(nil), expected...)
	binary.LittleEndian.PutUint64(forged[:8], ^uint64(0))
	for _, raw := range [][]byte{expected[:7], expected[:len(expected)-1], append(append([]byte(nil), expected...), 0), forged} {
		if _, err := DecodeInventoryOrderFacts(raw, 2); err == nil {
			t.Fatal("malformed original fields accepted")
		}
	}
	for _, count := range []int{-1, 0, 3} {
		if _, err := DecodeInventoryOrderFacts(expected, count); err == nil {
			t.Fatal("wrong count accepted", count)
		}
	}
}
