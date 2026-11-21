// Package interaction owns authenticated channel identities and normalized input.
// It does not execute models or Tools; accepted messages enter the existing Actor.
package interaction

import (
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/conversation"
	apps "github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
)

// The local Web channel identity is shared by admission and its adapter.
const WebSubject = "browser"
const WebDestination = "local"

var ErrUnauthorized = errors.New("channel identity is not bound")

type Binding struct {
	ID          string `json:"binding_id"`
	Channel     string `json:"channel"`
	Subject     string `json:"subject"`
	Destination string `json:"destination"`
	Enabled     bool   `json:"enabled"`
	Version     int64  `json:"version"`
}

// InboundEvent contains transport-verified identity. Callers cannot choose an
// Agent, Conversation, Run, Tool policy, request key, or notification recipient.
type InboundEvent struct {
	Channel     string
	Subject     string
	Destination string
	EventID     string
	SourceID    string
	Content     []conversation.ContentPart
}

func ValidIdentity(channel, subject, destination string) bool {
	if channel != "web" && channel != "feishu" {
		return false
	}
	for _, s := range []string{subject, destination} {
		if strings.TrimSpace(s) == "" || len(s) > 256 || strings.ContainsAny(s, "\r\n\x00") {
			return false
		}
	}
	return true
}

func (e InboundEvent) Validate() error {
	if !ValidIdentity(e.Channel, e.Subject, e.Destination) || e.SourceID == "" || e.EventID == "" || len(e.SourceID) > 256 || len(e.EventID) > 256 {
		return ErrUnauthorized
	}
	_, err := conversation.Normalize(e.Content)
	return err
}

// Approval is an authenticated control projection. The hash and final arguments
// are required to make a decision; raw invocation traces and Run IDs stay private.
type Approval struct {
	MailReview *apps.FrozenMailReview `json:"-"`
	ID         string                 `json:"approval_id"`
	Tool       string                 `json:"tool"`
	Arguments  string                 `json:"arguments"`
	Parameters string                 `json:"action_parameters,omitempty"`
	Hash       string                 `json:"frozen_arguments_hash"`
	Status     string                 `json:"status"`
	ExpiresAt  int64                  `json:"expires_at"`
}

type Preference struct {
	Channel   string  `json:"channel"`
	BindingID *string `json:"binding_id"`
	Version   int64   `json:"version"`
}

// Credential references name a SecretStore entry; literal credential values are
// not accepted as process configuration.
func ValidCredentialRef(ref string) bool {
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		if len(name) == 0 || len(name) > 128 {
			return false
		}
		for i, c := range name {
			if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
				return false
			}
		}
		return true
	}
	name, ok := strings.CutPrefix(ref, "credential:")
	return ok && name != "" && len(name) <= 256 && !strings.ContainsAny(name, " \r\n\t/\\\x00")
}
