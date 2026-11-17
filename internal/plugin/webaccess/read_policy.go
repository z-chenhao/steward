package webaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Private, experimental ports for the existing get_search_content consumer.
// Artifact identity, scope, provenance and the interpreter revision stay in Host.
const ReadContract = "steward.web-content-read/1"
const EvidenceContract = "steward.web-evidence/1"
const EvidencePartBytes = 12 << 20

type ReadArguments struct {
	ResponseID string `json:"responseId"`
	ReadRequest
}
type ReadRequest struct {
	URLIndex   int    `json:"urlIndex"`
	QueryIndex int    `json:"queryIndex"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	FindText   string `json:"findText"`
	FindMode   string `json:"findMode"`
}
type EvidenceRequest struct {
	Part int `json:"part"`
}
type EvidenceReply struct {
	Data       []byte `json:"data"`
	TotalBytes int    `json:"total_bytes"`
}
type ReadPlan struct {
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	MatchStart int    `json:"match_start"`
	MatchEnd   int    `json:"match_end"`
	Found      *bool  `json:"found,omitempty"`
	ErrorCode  string `json:"error_code"`
}

func readService(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request ReadRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	var data []byte
	total := -1
	for part := 0; part < 2; part++ {
		var reply EvidenceReply
		if err := rsiplugin.CallResource(ctx, EvidenceContract, fmt.Sprintf("evidence:%d", part), EvidenceRequest{Part: part}, &reply); err != nil {
			return rsiplugin.Result{}, err
		}
		if reply.TotalBytes < 0 || reply.TotalBytes > MaxEvidenceBytes || (total >= 0 && reply.TotalBytes != total) || len(reply.Data) != min(EvidencePartBytes, reply.TotalBytes-part*EvidencePartBytes) {
			return rsiplugin.Result{}, errors.New("invalid evidence part")
		}
		total = reply.TotalBytes
		data = append(data, reply.Data...)
		if len(data) == total {
			break
		}
	}
	var document Document
	if json.Unmarshal(data, &document) != nil {
		return rsiplugin.JSONResult(ctx, ReadPlan{ErrorCode: "INVALID_ARGUMENT"})
	}
	return rsiplugin.JSONResult(ctx, planContentRead(document, request))
}

// The candidate owns window defaults, match context and the actual search.
// Host accepts only ranges proved against the exact original document/intent.
func planContentRead(d Document, q ReadRequest) ReadPlan {
	text, err := EvidenceText(d, q.URLIndex, q.QueryIndex)
	if err != nil {
		return ReadPlan{ErrorCode: err.Error()}
	}
	limit := q.Limit
	if limit == 0 {
		limit = 8000
	}
	if q.Offset < 0 || q.Offset > len(text) || limit < 1 || limit > 32000 || len(q.FindText) > 500 {
		return ReadPlan{ErrorCode: "INVALID_ARGUMENT"}
	}
	plan := ReadPlan{Offset: q.Offset}
	if q.FindText != "" {
		start, end, err := FindSpanV1(text, q.FindText, q.FindMode)
		if err != nil {
			return ReadPlan{ErrorCode: err.Error()}
		}
		found := start >= 0
		plan.Found = &found
		if !found {
			plan.Offset = 0
			return plan
		}
		plan.MatchStart, plan.MatchEnd = start, end
		plan.Offset = max(0, start-400)
		if end-plan.Offset > limit {
			limit = min(32000, end-plan.Offset+400)
		}
	}
	for plan.Offset > 0 && plan.Offset < len(text) && !utf8.RuneStart(text[plan.Offset]) {
		plan.Offset--
	}
	plan.NextOffset = min(len(text), plan.Offset+limit)
	for plan.NextOffset < len(text) && plan.NextOffset > plan.Offset && !utf8.RuneStart(text[plan.NextOffset]) {
		plan.NextOffset--
	}
	return plan
}
