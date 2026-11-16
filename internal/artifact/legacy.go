package artifact

import (
	"mime"
	"strings"
)

// Consumers are original NULL-method Artifact references, including retained
// historical Runs/channels/publications. New semantic facts never use current
// MIME policy to reinterpret them. Remove only after those refs are retired.
func legacyContentType(mediaType string) string {
	media, _, _ := mime.ParseMediaType(mediaType)
	for _, kind := range []string{"image", "audio", "video"} {
		if strings.HasPrefix(media, kind+"/") {
			return kind
		}
	}
	return "file"
}
