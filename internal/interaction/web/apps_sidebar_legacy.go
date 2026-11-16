package web

// Native/default23,direct callers,actual pre-sidebar and captured/retained
// references still consume this sequence. Delete when those consumers retire.
// It is never a current opted-in missing/invalid view fallback.
func appSidebarProgramLegacyV1() []string {
	return []string{"begin-entry", "icon", "name", "pending", "end-entry"}
}
