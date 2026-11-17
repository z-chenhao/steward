package contract

import (
	"encoding/binary"
	"errors"
)

// InventoryOrder is private account presentation, never public catalogue data.
const InventoryOrder = "steward.apps-inventory-order/1"
const InventoryOrderInputID = "inventory-order"

type InventoryOrderFact struct{ Name, Slug string }
type InventoryOrderRequest struct {
	Count     int  `json:"count"`
	Offset    int  `json:"offset"`
	Connected bool `json:"connected"`
}

// Length-delimited original display strings preserve even raw Go string bytes.
// No whole-source byte cap is added to the existing uncapped account state.
func EncodeInventoryOrderFacts(facts []InventoryOrderFact) []byte {
	var out []byte
	for _, fact := range facts {
		for _, value := range []string{fact.Name, fact.Slug} {
			out = binary.LittleEndian.AppendUint64(out, uint64(len(value)))
			out = append(out, value...)
		}
	}
	return out
}
func DecodeInventoryOrderFacts(raw []byte, count int) ([]InventoryOrderFact, error) {
	if count < 0 || count > len(raw)/16 {
		return nil, errors.New("invalid inventory count")
	}
	facts := make([]InventoryOrderFact, count)
	for i := range facts {
		for _, field := range []*string{&facts[i].Name, &facts[i].Slug} {
			if len(raw) < 8 {
				return nil, errors.New("truncated inventory field")
			}
			size := binary.LittleEndian.Uint64(raw[:8])
			raw = raw[8:]
			if size > uint64(len(raw)) {
				return nil, errors.New("invalid inventory field")
			}
			*field = string(raw[:int(size)])
			raw = raw[int(size):]
		}
	}
	if len(raw) != 0 {
		return nil, errors.New("trailing inventory facts")
	}
	return facts, nil
}
