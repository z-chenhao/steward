package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"unicode/utf8"
)

type experiencePolicy struct {
	Managed      bool
	Revision     string
	RequiredRuns int
	Introduction string
}

var errExperienceUnavailable = errors.New("experience policy unavailable")

func minimumExperienceRuns(reason string) int {
	if reason == "success" {
		return 2
	}
	return 1
}
func (r *Runs) prepareExperiencePolicy(ctx context.Context, reason string) (experiencePolicy, error) {
	p := experiencePolicy{RequiredRuns: minimumExperienceRuns(reason)}
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		p.Managed, err = managedService(ctx, tx, contract.Contract)
		if err != nil || !p.Managed {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&p.Revision)
	})
	if err != nil || !p.Managed {
		return p, err
	}
	if r.plugins == nil || p.Revision == "" {
		return p, errExperienceUnavailable
	}
	h := *r.plugins
	raw, _ := json.Marshal(contract.Request{Reason: reason})
	result, err := h.ReadService(ctx, p.Revision, contract.Contract, raw, nil)
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) {
		return p, errExperienceUnavailable
	}
	if err != nil {
		return p, err
	}
	var policy contract.Policy
	if err = rsiplugin.DecodeResult(result, &policy); err != nil {
		return p, err
	}
	if !validExperiencePolicy(reason, policy) {
		return p, rsi.ErrInvalid
	}
	p.RequiredRuns = policy.RequiredRuns
	p.Introduction = policy.Introduction
	return p, nil
}
func validExperiencePolicy(reason string, p contract.Policy) bool {
	return p.RequiredRuns >= minimumExperienceRuns(reason) && p.RequiredRuns <= 128 && utf8.ValidString(p.Introduction) && utf8.RuneCountInString(p.Introduction) <= 1024 && !memory.HasSecret(p.Introduction)
}
func experiencePolicyFence(ctx context.Context, tx *sql.Tx, p experiencePolicy) error {
	if !p.Managed {
		managed, err := managedService(ctx, tx, contract.Contract)
		if err != nil {
			return err
		}
		if managed {
			return agent.ErrConflict
		}
		return nil
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation WHERE json_extract(receipt_json,'$.graph_revision')=?) AND NOT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?) AND NOT (SELECT stopped FROM rsi_control WHERE singleton=1)`, p.Revision, p.Revision).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return errExperienceUnavailable
	}
	return nil
}
