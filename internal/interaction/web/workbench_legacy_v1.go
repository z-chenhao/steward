package web

// Explicit unbundled Web embeddings and published headerless updates v0 retain
// this recipe. Managed pages never select it when their producer is unavailable.
func legacyWorkbenchLivePath(view, path string) string {
	switch view {
	case "conversation":
		return "/"
	case "tasks", "task", "attention", "settings":
		return path
	}
	return ""
}

// Count only rows rendered on this historical page, including first-page schedules.
func legacyWorkbenchPageCountV1(v *taskView) int {
	if v == nil {
		return 0
	}
	if !v.Configurations {
		return len(v.Executions.Items)
	}
	count := len(v.Scheduled)
	for _, entry := range v.List.Items {
		if entry.Schedule == nil {
			count++
		}
	}
	return count
}

func legacyWorkbenchHistoryLabelV1(kind string) string {
	switch kind {
	case "instruction":
		return "Request updated"
	case "completed":
		return "Work round completed"
	case "failed":
		return "Work attempt failed"
	case "needs_input":
		return "Input requested"
	case "unknown":
		return "Outcome unconfirmed"
	}
	return kind
}

// Only explicit native workbench embeddings retain the complete old stylesheet.
// Current unrelated features consume the evolving shared primitives, plus their
// own version-pinned styles, rather than a frozen second shared component skin.
func workbenchLegacyStyles(p page) bool {
	return p.Workbench == nil && !p.WorkbenchUnavailable && workbenchOwnedView(p.View)
}
