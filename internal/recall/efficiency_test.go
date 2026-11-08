package recall_test

import (
	"context"
	"github.com/z-chenhao/assistant/internal/recall"
	"testing"
)

func TestRecallEvidenceFirstKeepsOriginalBehindCompleteWiki(t *testing.T) {
	ctx := context.Background()
	scope := recall.Scope{UserID: "u", AgentID: "a"}
	wiki := recall.Hit{SourceRef: "memory:wiki", SourceType: "memory", Layer: "representations", Content: "Meeting overview. A related topic is not an original decision.", Sources: []string{"conversation:original"}, Relevance: 1}
	original := recall.Hit{SourceRef: "conversation:original", SourceType: "conversation", Content: "The meeting was cancelled; another event remains tentative.", Relevance: .9}
	service := recall.Service{Memory: provider{hits: []recall.Hit{wiki}}, Conversation: provider{hits: []recall.Hit{original}}}
	legacy, err := service.Search(ctx, scope, recall.Query{Query: "meeting", Limit: 6}, 8192)
	if err != nil || len(legacy) != 1 {
		t.Fatal(legacy, err)
	}
	current, err := service.Search(ctx, scope, recall.Query{Query: "meeting", Limit: 6, EvidenceFirst: true, ExcerptBytes: 1400}, 8192)
	if err != nil || len(current) != 2 || current[1].SourceRef != original.SourceRef {
		t.Fatal("derived summary hid original", current, err)
	}
	wiki.Relevance = .9
	service.Memory = provider{hits: []recall.Hit{wiki, {SourceType: "memory", SourceRef: "memory:injected", Layer: "injected", Content: "meeting", Relevance: 1}}}
	current, err = service.Search(ctx, scope, recall.Query{Query: "meeting", Limit: 6, EvidenceFirst: true, ExcerptBytes: 1400}, 8192)
	if err != nil || len(current) != 2 || current[0].SourceRef != original.SourceRef {
		t.Fatal("equal relevance preferred broad derived text or echoed injection", current, err)
	}
}
func TestRecallRelevantQuotesKeepQuerySpecificOriginalAttribution(t *testing.T) {
	quotes := []recall.Evidence{{SourceRef: "port", Quote: "The port is 8642."}, {SourceRef: "csv", Quote: "Import encoding is UTF-8."}, {SourceRef: "login", Author: "user", Quote: "Login recovery succeeded by starting on this device."}}
	selected := recall.RelevantEvidence(quotes, "login recovery", 2)
	if len(selected) != 2 || selected[0].SourceRef != "login" || selected[0].Author != "user" || quotes[0].SourceRef != "port" {
		t.Fatal(selected)
	}
}

func TestRecallEvidenceFirstRetainsExplicitExclusionAndBudget(t *testing.T) {
	scope := recall.Scope{UserID: "u", AgentID: "a"}
	service := recall.Service{Memory: provider{hits: []recall.Hit{{SourceType: "memory", SourceRef: "memory:echo", Content: "changed current request", Sources: []string{"conversation:current"}, Relevance: 1}}}, Conversation: provider{hits: []recall.Hit{{SourceType: "conversation", SourceRef: "conversation:current", Content: "current request", Relevance: 1}, {SourceType: "conversation", SourceRef: "conversation:past", Content: "past original", Relevance: .8}}}}
	hits, err := service.Search(context.Background(), scope, recall.Query{Query: "original", Limit: 6, EvidenceFirst: true, ExcludeRefs: []string{"conversation:current"}, ExcerptBytes: 1400}, 512)
	if err != nil || len(hits) != 1 || hits[0].SourceRef != "conversation:past" {
		t.Fatal(hits, err)
	}
}

func TestRecallSeenReferenceDoesNotHideOtherDerivedContent(t *testing.T) {
	service := recall.Service{Memory: provider{hits: []recall.Hit{{SourceRef: "memory:combined", SourceType: "memory", Layer: "representations", Content: "Decision and additional source context", Sources: []string{"conversation:seen", "conversation:other"}, Relevance: 1}}}, Conversation: provider{hits: []recall.Hit{{SourceRef: "conversation:seen", SourceType: "conversation", Content: "Already visible source", Relevance: 1}, {SourceRef: "conversation:other", SourceType: "conversation", Content: "Additional source", Relevance: .9}}}}
	hits, err := service.Search(context.Background(), recall.Scope{UserID: "u", AgentID: "a"}, recall.Query{Query: "source", Limit: 5, EvidenceFirst: true, SeenRefs: []string{"conversation:seen"}}, 8192)
	if err != nil || len(hits) != 2 || hits[0].SourceRef != "memory:combined" {
		t.Fatal("visible original removed unseen derived context", hits, err)
	}
}

func TestRecallOriginalSourceRestrictionSkipsDerivedProviderWithoutChangingScope(t *testing.T) {
	s := recall.Service{Memory: provider{err: recall.ErrUnavailable}, Conversation: provider{hits: []recall.Hit{{SourceType: "conversation", SourceRef: "conversation:one", Content: "Original report"}}}}
	hits, err := s.Search(context.Background(), recall.Scope{UserID: "u", AgentID: "a"}, recall.Query{Query: "report", SourceType: "conversation", Limit: 10, EvidenceFirst: true}, 8192)
	if err != nil || len(hits) != 1 || hits[0].SourceType != "conversation" {
		t.Fatal(hits, err)
	}
	if _, err = recall.Parse(`{"query":"report","source_type":"task"}`); err == nil {
		t.Fatal("broadened source authority")
	}
}
