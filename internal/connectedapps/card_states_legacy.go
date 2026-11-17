package connectedapps

// Original pre-directory/default23/direct interpretation. Independent account
// views use this only before their inventory service's durable activation;
// current managed errors never enter it. Retain for named old consumers and
// delete when their graphs/references no longer require original semantics.
func AccountCardFacts(state State, toolkits []string) []CardState {
	out := make([]CardState, len(toolkits))
	for i, toolkit := range toolkits {
		for _, row := range state.Connections {
			if row.Toolkit != toolkit {
				continue
			}
			if row.Status != "disconnected" || !row.RevocationConfirmed {
				out[i].Managed = true
			}
			if row.Status == "active" {
				out[i].Connected = true
				out[i].PublicActive = row.Public
			}
			if row.Status == "pending" {
				out[i].Pending = true
			}
		}
	}
	return out
}
