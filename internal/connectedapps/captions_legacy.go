package connectedapps

// Exact pre-caption Web interpretation for native/pre-directory and explicit
// direct/unbundled consumers only. Never used on managed error or disable.
// Delete after those named compositions migrate and no original consumer remains.
func captionLegacyV1(sources []CaptionSources) []string {
	result := make([]string, len(sources))
	for i, src := range sources {
		text := src.Editorial
		if text == "" {
			text = src.Provider
		}
		if text == "" {
			text = "Bring this app into your everyday work"
		}
		caption := []rune(text)
		if len(caption) > 150 {
			text = string(caption[:147]) + "…"
		}
		result[i] = text
	}
	return result
}
