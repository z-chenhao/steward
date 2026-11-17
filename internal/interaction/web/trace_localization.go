package web

import "github.com/z-chenhao/assistant/internal/observability"

// Owned copy is frozen with the selected Trace interpreter. Unknown labels keep
// the original text; shared templates retain the Host tr binding.
func traceTranslate(copy []observability.TraceTranslation, language, text string) string {
	if language != "zh-CN" {
		return text
	}
	for _, entry := range copy {
		if entry.English == text {
			return entry.Chinese
		}
	}
	return text
}
