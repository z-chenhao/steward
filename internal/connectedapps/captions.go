package connectedapps

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"unicode/utf8"
)

// CaptionSources never crosses the plugin boundary.
type CaptionSources struct{ Editorial, Provider string }

// ProjectCaptions validates the whole result before publishing any text. The
// closed source references and rune prefixes cannot substitute private facts.
func (s DirectorySelection) ProjectCaptions(ctx context.Context, sources []CaptionSources) ([]string, error) {
	if s.mode == directoryLegacy {
		return captionLegacyV1(sources), nil
	}
	if s.mode != directoryManaged || s.methods == nil || s.methods.Captions == nil {
		return nil, ErrUnavailable
	}
	result := make([]string, 0, len(sources))
	for start := 0; start < len(sources); start += contract.CaptionBatchSize {
		end := min(start+contract.CaptionBatchSize, len(sources))
		batch := sources[start:end]
		facts := make([]contract.CaptionFacts, len(batch))
		for i, src := range batch {
			facts[i] = contract.CaptionFacts{EditorialRunes: utf8.RuneCountInString(src.Editorial), ProviderRunes: utf8.RuneCountInString(src.Provider)}
		}
		plans, err := s.methods.Captions(ctx, facts)
		if err != nil {
			return nil, ErrUnavailable
		}
		if len(plans) != len(batch) {
			return nil, ErrUnavailable
		}
		for i, p := range plans {
			if p.End < 0 || !utf8.ValidString(p.Text) || len(p.Text) > 4096 {
				return nil, ErrUnavailable
			}
			var raw string
			switch p.Source {
			case "editorial":
				raw = batch[i].Editorial
			case "provider":
				raw = batch[i].Provider
			case "literal":
				if p.End != 0 {
					return nil, ErrUnavailable
				}
			default:
				return nil, ErrUnavailable
			}
			if p.End > utf8.RuneCountInString(raw) {
				return nil, ErrUnavailable
			}
			// Slice at a rune boundary without allocating a second full source copy.
			stop := len(raw)
			n := 0
			for offset := range raw {
				if n == p.End {
					stop = offset
					break
				}
				n++
			}
			result = append(result, raw[:stop]+p.Text)
		}
	}
	return result, nil
}
