package contract

import (
	"reflect"
	"testing"
)

func TestInventoryFactsPreservePrivateDisplayBytesAndRejectMalformedFrames(t *testing.T) {
	rows := []InventoryRow{{Slug: "gmail", Name: "private\x00\xfflabel", Facts: Active | Public}, {Slug: "other", Name: "中文", Facts: Disconnected | RevocationConfirmed}}
	raw := EncodeInventoryRows(rows)
	got, err := DecodeInventoryRows(raw, len(rows))
	if err != nil || !reflect.DeepEqual(got, rows) {
		t.Fatal("changed exact display facts", got, err)
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], append(append([]byte{}, raw...), 0), {0}, append([]byte{Active | Pending}, raw[1:]...), append([]byte{128}, raw[1:]...)} {
		if _, err := DecodeInventoryRows(bad, len(rows)); err == nil {
			t.Fatal("accepted malformed account facts")
		}
	}
	if _, err := DecodeInventoryRows(raw, -1); err == nil {
		t.Fatal("accepted negative count")
	}
	if _, err := DecodeInventoryRows(nil, 1); err == nil {
		t.Fatal("invented absent row")
	}
}
