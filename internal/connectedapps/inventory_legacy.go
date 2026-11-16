package connectedapps

// Original pre-directory/default23/direct interpretation. Independent account
// views use this only before their inventory service's durable activation;
// current managed errors never enter it. Retain for named old consumers and
// delete when their graphs/references no longer require original semantics.
func AccountInventoryFacts(state State) []int {
	var out []int
	seen := map[string]bool{}
	for i, row := range state.Connections {
		if seen[row.Toolkit] || row.Status == "disconnected" && row.RevocationConfirmed {
			continue
		}
		seen[row.Toolkit] = true
		out = append(out, i)
	}
	return out
}
