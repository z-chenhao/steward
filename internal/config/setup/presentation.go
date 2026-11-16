package setup

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/legacyv1"
	"strconv"
	"strings"
	"unicode/utf8"
)

type PresentationReader func(context.Context, contract.PresentationFacts) (contract.Presentation, error)

func (m *Manager) presentation(ctx context.Context) (*contract.Presentation, error) {
	facts := contract.PresentationFacts{Root: m.active.App.DataRoot}
	var out contract.Presentation
	if m.Presentation == nil {
		out = legacyv1.ProjectPresentation(facts)
	} else {
		var err error
		out, err = m.Presentation(ctx, facts)
		if err != nil {
			return nil, err
		}
	}
	valid := func(s string, max int) bool {
		return utf8.ValidString(s) && s != "" && len(s) <= max && !strings.ContainsRune(s, 0)
	}
	seen := map[string]bool{}
	if len(out.Descriptions) != len(kinds) {
		return nil, errors.New("invalid capability presentation")
	}
	for _, v := range out.Descriptions {
		if !known(v.ID) || seen[v.ID] || !valid(v.Title, 256) || !valid(v.Description, 4096) || !valid(v.Help, 16384) || !valid(v.Example, 65536) {
			return nil, errors.New("invalid capability presentation")
		}
		seen[v.ID] = true
	}
	for _, s := range []string{out.Copy.Automatic, out.Copy.MemoryKeyUnavailable, out.Copy.ExecutionManaged, out.Copy.VerificationExpired, out.Copy.ProbeAutomatic, out.Copy.ProbeExecutionConflict, out.Copy.ProbeDisabled, out.Copy.ProbeMemoryFailed, out.Copy.ProbeMemoryReady, out.Copy.ProbeEmbeddingFailed, out.Copy.ProbeEmbeddingReady, out.Copy.ProbeSkillsFailed, out.Copy.ProbeSkillsReady, out.Copy.ProbeMCPFailed, out.Copy.ProbeMCPReady, out.Copy.ProbeExecutionFailed, out.Copy.ProbeExecutionInvalid, out.Copy.ProbeExecutionReady, out.Copy.ProbeJavaScriptMissing, out.Copy.ProbeJavaScriptFailed, out.Copy.ProbeComputerStoreFailed, out.Copy.ProbeComputerFailed, out.Copy.ProbeComputerReady, out.Copy.ProbeUnknown} {
		if !valid(s, 16384) {
			return nil, errors.New("invalid capability presentation")
		}
	}
	return &out, nil
}
func probeMessage(result probeResult, copy contract.Copy) string {
	var text string
	switch result.Code {
	case "ProbeAutomatic":
		text = copy.ProbeAutomatic
	case "ProbeExecutionConflict":
		text = copy.ProbeExecutionConflict
	case "ProbeDisabled":
		text = copy.ProbeDisabled
	case "ProbeMemoryFailed":
		text = copy.ProbeMemoryFailed
	case "ProbeMemoryReady":
		text = copy.ProbeMemoryReady
	case "ProbeEmbeddingFailed":
		text = copy.ProbeEmbeddingFailed
	case "ProbeEmbeddingReady":
		text = copy.ProbeEmbeddingReady
	case "ProbeSkillsFailed":
		text = copy.ProbeSkillsFailed
	case "ProbeSkillsReady":
		text = copy.ProbeSkillsReady
	case "ProbeMCPFailed":
		text = copy.ProbeMCPFailed
	case "ProbeMCPReady":
		text = copy.ProbeMCPReady
	case "ProbeExecutionFailed":
		text = copy.ProbeExecutionFailed
	case "ProbeExecutionInvalid":
		text = copy.ProbeExecutionInvalid
	case "ProbeExecutionReady":
		text = copy.ProbeExecutionReady
	case "ProbeJavaScriptMissing":
		text = copy.ProbeJavaScriptMissing
	case "ProbeJavaScriptFailed":
		text = copy.ProbeJavaScriptFailed
	case "ProbeComputerStoreFailed":
		text = copy.ProbeComputerStoreFailed
	case "ProbeComputerFailed":
		text = copy.ProbeComputerFailed
	case "ProbeComputerReady":
		text = copy.ProbeComputerReady
	case "ProbeUnknown":
		text = copy.ProbeUnknown
	}
	text = strings.ReplaceAll(text, "{{dimensions}}", strconv.Itoa(result.Dimensions))
	text = strings.ReplaceAll(text, "{{providers}}", strconv.Itoa(result.Providers))
	return strings.ReplaceAll(text, "{{tools}}", strconv.Itoa(result.Tools))
}
