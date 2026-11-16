package computer

// nativeTargetMatchV1 serves only frozen Runs without target-match/1 and the
// explicitly unbundled facade-v1 carrier. Remove it after no retained execution
// depends on that boundary; managed dispatch never falls back here on failure.
func nativeTargetMatchV1(kind, selector string, values []Target) *Target {
	var selected *Target
	for _, v := range values {
		if selector == "" && kind == "browsers" || selector == v.ID || selector == v.Name || selector != "" && selector == v.URL {
			if selected != nil {
				return nil
			}
			copy := v
			selected = &copy
		}
	}
	return selected
}
