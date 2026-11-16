package web

// Original native/default23, direct template callers and explicit pre-card graph
// references still consume this sequence. Delete when those consumers retire.
// It is never used for an opted-in current missing/invalid card contribution.
func appCardProgramLegacyV1() []string {
	return []string{"begin-card", "begin-summary", "icon", "begin-copy", "name", "description", "end-copy", "end-summary", "manage", "connect", "end-card"}
}
