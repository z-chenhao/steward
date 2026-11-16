package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (s Inspector) ProjectedToolOutput(ctx context.Context, q observability.BodyQuery) (observability.Body, *observability.ToolPresentation, bool, error) {
	if err := q.Validate(); err != nil {
		return observability.Body{}, nil, false, err
	}
	if q.Field != "output" {
		return observability.Body{}, nil, false, observability.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return observability.Body{}, nil, false, err
	}
	defer tx.Rollback()
	o, err := selectedObservation(ctx, tx, q.ObservationQuery)
	if err != nil {
		return observability.Body{}, nil, false, err
	}
	if o.Type != "tool" {
		return observability.Body{}, nil, false, observability.ErrInvalid
	}
	body, err := s.observationBody(ctx, tx, q)
	if err != nil {
		return body, nil, false, err
	}
	data, err := json.Marshal(contract.ToolFacts{Name: o.Name, Available: body.Available, Complete: body.Next == nil, Offset: q.Offset, Content: body.Content})
	if err != nil {
		return body, nil, false, err
	}
	raw, _, managed, err := s.traceService(ctx, tx, contract.ToolContract, data, q.ReaderRevision)
	if err != nil || !managed {
		return body, nil, managed, err
	}
	var preview *contract.ToolPreview
	if err = json.Unmarshal(raw, &preview); err != nil {
		return body, nil, true, err
	}
	if preview == nil {
		return body, nil, true, nil
	}
	// Links and errors retain their saved receipt authority. The package owns
	// text interpretation, never the identity or access scope of an Artifact.
	var saved tool.Result
	if json.Unmarshal([]byte(body.Content), &saved) != nil {
		return body, nil, true, rsi.ErrInvalid
	}
	allowedArtifacts, allowedImages := map[string]bool{}, map[string]bool{}
	for _, r := range saved.ArtifactRefs {
		allowedArtifacts[r] = true
	}
	for _, r := range saved.ImageRefs {
		allowedImages[r] = true
	}
	for _, r := range preview.Artifacts {
		if !allowedArtifacts[r] {
			return body, nil, true, rsi.ErrInvalid
		}
	}
	for _, r := range preview.Images {
		if !allowedImages[r] {
			return body, nil, true, rsi.ErrInvalid
		}
	}
	return body, &observability.ToolPresentation{Texts: preview.Texts, Artifacts: preview.Artifacts, Images: preview.Images, Error: saved.Error}, true, nil
}
