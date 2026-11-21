// Package github receives exact-PR observations. Its types are private to
// Steward's implementation, not a general source or workflow protocol.
package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/llm"
)

var ErrStopped = errors.New("GitHub watch stopped or changed; inspect github status")

type Config struct {
	Repository    string `yaml:"repository" json:"repository"`
	PullNumber    int64  `yaml:"pull_number" json:"pull_number"`
	CredentialRef string `yaml:"credential_ref" json:"-"`
	PollSeconds   int    `yaml:"poll_seconds" json:"-"`
}

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)

func (c Config) Validate() error {
	if !repositoryPattern.MatchString(c.Repository) || strings.HasSuffix(c.Repository, "/.") || strings.HasSuffix(c.Repository, "/..") || c.PullNumber < 1 || c.PullNumber > 1<<31-1 || !interaction.ValidCredentialRef(c.CredentialRef) || c.PollSeconds != 0 && (c.PollSeconds < 5 || c.PollSeconds > 3600) {
		return errors.New("github requires an exact owner/repository, pull_number, credential_ref and poll_seconds between 5 and 3600")
	}
	return nil
}

func ConfiguredScopes(watches []Config) ([]string, error) {
	scopes := make([]string, 0, len(watches))
	for _, watch := range watches {
		if err := watch.Validate(); err != nil {
			return nil, err
		}
		scopes = append(scopes, watch.Scope())
	}
	slices.Sort(scopes)
	for index := 1; index < len(scopes); index++ {
		if scopes[index] == scopes[index-1] {
			return nil, errors.New("duplicate configured GitHub PR")
		}
	}
	return scopes, nil
}

func (c Config) Scope() string { return strings.ToLower(c.Repository) + "#" + fmt.Sprint(c.PullNumber) }
func (c Config) Interval() time.Duration {
	if c.PollSeconds == 0 {
		return time.Minute
	}
	return time.Duration(c.PollSeconds) * time.Second
}

type Observation struct {
	Sequence   int64      `json:"sequence,omitempty"`
	Key        string     `json:"event_key"`
	Kind       string     `json:"kind"`
	SourceID   int64      `json:"source_id"`
	URL        string     `json:"url"`
	Actor      string     `json:"actor,omitempty"`
	Body       string     `json:"body,omitempty"`
	State      string     `json:"state,omitempty"`
	HeadSHA    string     `json:"head_sha,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
	Method     *Method    `json:"-"`
}

func (o *Observation) identify(sourceBody string) {
	// Reviews expose submitted_at, not an edit timestamp. Preserve the full
	// source version in the key even when its displayed body is truncated.
	bodyHash := sha256.Sum256([]byte(sourceBody))
	raw, _ := json.Marshal(struct {
		Observation
		SourceBodyHash string `json:"source_body_hash"`
	}{*o, hex.EncodeToString(bodyHash[:])})
	hash := sha256.Sum256(raw)
	o.Key = o.Kind + ":" + fmt.Sprint(o.SourceID) + ":" + hex.EncodeToString(hash[:])
}

type Batch struct {
	RepositoryID    int64
	HeadSHA         string
	Events          []Observation
	bytes           int
	IntervalSeconds int `json:"-"`
}

func (b *Batch) add(e Observation) error {
	b.bytes += len(e.Body) + len(e.URL) + len(e.Actor) + len(e.State) + 512
	if e.Method != nil {
		// The original per-row allowance covers the default frozen recipe.
		// Enlarged method text must spend the same finite scan budget, including
		// JSON escaping; it cannot multiply an unpriced payload by every row.
		raw, err := json.Marshal(e.Method)
		if err != nil {
			return err
		}
		if len(raw) > 512 {
			b.bytes += len(raw) - 512
		}
	}
	if b.bytes > 8<<20 {
		return &Failure{"HISTORY_LIMIT"}
	}
	b.Events = append(b.Events, e)
	return nil
}

type Ticket struct {
	Version              int64
	Epoch                int64
	BindingGeneration    int64
	IncludeIssueComments bool
	Managed              bool
	GraphRevision        string
}

type Status struct {
	Repository   string     `json:"repository,omitempty"`
	PullNumber   int64      `json:"pull_number,omitempty"`
	RepositoryID *int64     `json:"repository_id,omitempty"`
	Enabled      bool       `json:"enabled"`
	State        string     `json:"state"`
	FailureCode  string     `json:"failure_code,omitempty"`
	HeadSHA      string     `json:"head_sha,omitempty"`
	LastAttempt  *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccess  *time.Time `json:"last_success_at,omitempty"`
	EventCount   int64      `json:"event_count"`
}

// Store is defined by its sole consumer. A poll ticket fences concurrent polls,
// operator stops, and privacy barriers while network IO stays outside SQL.
type Store interface {
	BeginGitHubPoll(context.Context, Config) (Ticket, error)
	FinishGitHubPoll(context.Context, Config, Ticket, Batch, string) error
}

type Service struct {
	Config   Config
	Store    Store
	Client   Client
	ReadStep ReadStep
}

func (s Service) Poll(ctx context.Context) error {
	_, err := s.poll(ctx)
	return err
}
func (s Service) poll(ctx context.Context) (time.Duration, error) {
	interval := s.Config.Interval()
	if err := s.Config.Validate(); err != nil {
		return interval, err
	}
	ticket, err := s.Store.BeginGitHubPoll(ctx, s.Config)
	if err != nil {
		return interval, err
	}
	var batch Batch
	var pollErr error
	if ticket.Managed {
		var selected time.Duration
		batch, selected, pollErr = s.Client.readManaged(ctx, s.Config, ticket, s.ReadStep)
		if selected > 0 {
			interval = selected
			batch.IntervalSeconds = int(selected / time.Second)
		}
	} else {
		batch, pollErr = s.Client.readLegacyV1(ctx, s.Config)
	}
	if ctx.Err() != nil {
		return interval, ctx.Err() // shutdown does not commit an interrupted/partial scan
	}
	code := ""
	if pollErr != nil {
		code = "UNAVAILABLE"
		var failure *Failure
		if errors.As(pollErr, &failure) {
			code = failure.Code
		}
	}
	if err = s.Store.FinishGitHubPoll(ctx, s.Config, ticket, batch, code); err != nil {
		return interval, err
	}
	return interval, pollErr
}

// Run is canceled and joined with the foreground runtime. Failure is persisted
// and retried at a bounded interval; it never closes the user's control surface.
func (s Service) Run(ctx context.Context) {
	for ctx.Err() == nil {
		interval, _ := s.poll(ctx)
		// On unavailable policy, this remains the existing finite retry wait;
		// it never executes the legacy reader for a managed ticket.
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type Failure struct{ Code string }

func (e *Failure) Error() string { return "GitHub observation failed: " + e.Code }

// Client only uses fixed api.github.com GET endpoints. Redirects are rejected
// even on the same host so credentials can never follow a repository redirect.
type Client struct {
	Secrets llm.SecretStore
	HTTP    *http.Client
}
