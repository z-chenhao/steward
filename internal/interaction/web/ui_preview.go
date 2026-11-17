package web

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// Existing streamed prefixes are bounded at 128 KiB. Accepted Table views
// additionally retain escaped DOM, original-text transport and bounded Worker
// bytes; their private representation can exceed the former 512 KiB envelope.
// Count all private bytes, retaining a fixed cap and unchanged tool admission.
const uiPreviewMaxBytes = 2 << 20

type uiPreviewItem struct {
	Key string `json:"key"`
	ui.Preview
}
type uiPreviewFrame struct {
	Revision uint64          `json:"revision"`
	Key      string          `json:"key"`
	State    string          `json:"state"`
	ReplyKey string          `json:"reply_key,omitempty"`
	Items    []uiPreviewItem `json:"items,omitempty"`
}
type uiPreview struct {
	mu      sync.Mutex
	changed chan struct{}
	version uint64
	session string
	runID   string
	frame   uiPreviewFrame
}

func (s *Server) observeUIPreview(e agent.Event) {
	if e.RunKind != string(agent.MainRole) || e.PreviewID == "" {
		return
	}
	p := &s.uiPreviews
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.Kind == "ui_preview_start" {
		p.session = e.PreviewID
		p.runID = e.RunID
		p.frame = uiPreviewFrame{Key: replyKey(e.PreviewID), State: "draft"}
	} else {
		if p.session != e.PreviewID || p.runID != e.RunID || p.frame.State != "draft" {
			return
		}
		switch e.Kind {
		case "ui_preview_update", "ui_preview_remove":
			key := replyKey(e.PreviewKey)
			items := append([]uiPreviewItem(nil), p.frame.Items...)
			index := -1
			for i, item := range items {
				if item.Key == key {
					index = i
					break
				}
			}
			if e.Kind == "ui_preview_remove" {
				if index < 0 {
					return
				}
				items = append(items[:index], items[index+1:]...)
			} else {
				if e.UIPreview == nil {
					return
				}
				item := uiPreviewItem{Key: key, Preview: *e.UIPreview}
				if index >= 0 {
					items[index] = item
				} else {
					if len(items) >= 16 {
						return
					}
					items = append(items, item)
				}
			}
			ordered := len(items) > 0
			for _, item := range items {
				ordered = ordered && item.Order != ""
			}
			if ordered {
				sort.SliceStable(items, func(i, j int) bool { return items[i].Order < items[j].Order })
			}
			views := make([]map[int]ui.PlanView, len(items))
			cards := make([]map[int]ui.CardsView, len(items))
			tables := make([]map[int]ui.TableView, len(items))
			charts := make([]map[int]ui.ChartView, len(items))
			for i := range items {
				views[i] = items[i].PlanViews
				cards[i] = items[i].CardsViews
				tables[i] = items[i].TableViews
				charts[i] = items[i].ChartViews
			}
			raw, _ := json.Marshal(struct {
				Items  []uiPreviewItem
				Views  []map[int]ui.PlanView
				Cards  []map[int]ui.CardsView
				Tables []map[int]ui.TableView
				Charts []map[int]ui.ChartView
			}{items, views, cards, tables, charts})
			if len(raw) > uiPreviewMaxBytes {
				return
			}
			p.frame.Items = items
		case "ui_preview_committed":
			p.frame.State = "committed"
			p.frame.ReplyKey = replyKey(e.GenerationID)
			p.frame.Items = nil
		case "ui_preview_discarded":
			p.frame.State = "discarded"
			p.frame.Items = nil
		default:
			return
		}
	}
	p.version++
	p.frame.Revision = p.version
	if p.changed != nil {
		close(p.changed)
		p.changed = nil
	}
}
func (p *uiPreview) read(version *uint64) (uiPreviewFrame, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.changed == nil {
		p.changed = make(chan struct{})
	}
	if *version == p.version {
		return uiPreviewFrame{}, p.changed
	}
	*version = p.version
	frame := p.frame
	frame.Items = append([]uiPreviewItem(nil), frame.Items...)
	for i := range frame.Items {
		frame.Items[i].Components = append([]ui.Component(nil), frame.Items[i].Components...)
		for j, c := range frame.Items[i].Components {
			if c.Name == "generateSandboxedUi" {
				var visual struct {
					InitialHeight float64 `json:"initialHeight"`
					CSSComplete   bool    `json:"cssComplete"`
				}
				_ = json.Unmarshal(c.Props, &visual)
				frame.Items[i].Components[j].Props, _ = json.Marshal(visual)
			}
		}
	}
	return frame, p.changed
}
