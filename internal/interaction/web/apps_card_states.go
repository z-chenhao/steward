package web

import (
	"github.com/z-chenhao/assistant/internal/connectedapps"
)

func applyCardStates(cards []*appCard, states []connectedapps.CardState) {
	for i, card := range cards {
		state := states[i]
		card.Managed = state.Managed
		card.Connected = state.Connected
		card.PublicActive = state.PublicActive
		card.Pending = state.Pending
	}
}
