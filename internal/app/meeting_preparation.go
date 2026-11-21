package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/meetingprep/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type meetingCache struct {
	mu                        sync.Mutex
	grant, day, graph, status string
	retry                     time.Time
	brief                     connectedapps.MeetingBrief
}

func tomorrowWindow(now time.Time, zone string) (time.Time, time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	t := now.In(loc)
	start := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1), nil
}
func (a *App) selectedMeetingGraph(ctx context.Context) (string, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return "", err
		}
	}
	host := a.Plugins()
	s, err := host.Store.Status(ctx)
	if err != nil {
		return "", err
	}
	if s.Stopped || s.Revision == "" {
		return "", rsi.ErrRevoked
	}
	if err = host.Store.CheckGraph(ctx, s.Revision); err != nil {
		return "", err
	}
	_, err = host.ServiceInstance(ctx, s.Revision, contract.Select)
	return s.Revision, err
}
func safeCalendarLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return ""
	}
	if u.Host != "calendar.google.com" && !(u.Host == "www.google.com" && u.Path == "/calendar/event") {
		return ""
	}
	return u.String()
}
func (a *App) prepareMeeting(ctx context.Context, g connectedapps.MeetingGrant, start, end time.Time, graph string) (connectedapps.MeetingBrief, bool, error) {
	var brief connectedapps.MeetingBrief
	host := a.Plugins()
	fence := func(ctx context.Context) error {
		s, e := host.Store.Status(ctx)
		if e != nil {
			return e
		}
		if s.Stopped || s.Revision != graph {
			return rsi.ErrRevoked
		}
		return host.Store.CheckGraph(ctx, graph)
	}
	evidence, err := a.connectedApps.ReadMeetingEvidence(ctx, g, start, end, fence)
	if err != nil {
		return brief, false, err
	}
	input := contract.Input{Meetings: []string{}, Subjects: append([]string{}, evidence.Subjects...)}
	for _, event := range evidence.Events {
		input.Meetings = append(input.Meetings, event.Title)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return brief, false, err
	}
	reply, err := a.Plugins().ReadService(ctx, graph, contract.Select, raw, nil)
	if err != nil {
		return brief, false, err
	}
	var pair contract.Pair
	if err = rsiplugin.DecodeResult(reply, &pair); err != nil {
		return brief, false, err
	}
	if pair.Meeting == -1 && pair.Thread == -1 {
		return brief, false, nil
	}
	if pair.Meeting < 0 || pair.Meeting >= len(evidence.Events) || pair.Thread < -1 || pair.Thread >= len(evidence.Threads) {
		return brief, false, rsi.ErrInvalid
	}
	event := evidence.Events[pair.Meeting]
	link := safeCalendarLink(event.Link)
	if link == "" {
		return brief, false, rsi.ErrInvalid
	}
	brief = connectedapps.MeetingBrief{Event: event.Title, EventLink: link, Start: event.Start.DateTime.In(start.Location()).Format("2006-01-02 15:04 MST")}
	if pair.Thread >= 0 {
		brief.Subject = evidence.Subjects[pair.Thread]
		brief.MailLink = "https://mail.google.com/mail/u/" + url.PathEscape(evidence.Mailbox) + "/#all/" + evidence.Threads[pair.Thread].ID
	}
	return brief, true, nil
}
func (a *App) refreshMeeting(ctx context.Context, now time.Time) {
	cache := &a.meetingCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	st, err := a.connectedApps.State(ctx)
	if err != nil || st.Meeting == nil {
		cache.grant = ""
		cache.brief = connectedapps.MeetingBrief{}
		return
	}
	g := *st.Meeting
	start, end, err := tomorrowWindow(now, g.Zone)
	if err != nil {
		return
	}
	day := start.Format("2006-01-02")
	if cache.grant == g.ID && cache.day == day && now.Before(cache.retry) {
		return
	}
	graph, err := a.selectedMeetingGraph(ctx)
	if err == nil && graph == cache.graph && g.ID == cache.grant && day == cache.day && (cache.status == "ready" || cache.status == "empty") {
		return
	}
	cache.grant, cache.day, cache.graph, cache.status = g.ID, day, graph, "unavailable"
	cache.brief = connectedapps.MeetingBrief{}
	cache.retry = now.Add(5 * time.Minute)
	if err != nil {
		return
	}
	brief, matched, err := a.prepareMeeting(ctx, g, start, end, graph)
	if err == nil {
		err = a.connectedApps.ValidateMeeting(ctx, g)
	}
	if err == nil {
		var current string
		current, err = a.selectedMeetingGraph(ctx)
		if err == nil && current != graph {
			err = rsi.ErrRevoked
		}
	}
	if err != nil {
		if errors.Is(err, connectedapps.ErrDenied) {
			cache.status = "revoked"
		}
		return
	}
	cache.status = "empty"
	if matched {
		cache.status = "ready"
		cache.brief = brief
	}
	cache.retry = time.Time{}
}
func (a *App) meetingLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		run, cancel := context.WithTimeout(ctx, 40*time.Second)
		a.refreshMeeting(run, time.Now())
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (a *App) ConfigureMeeting(ctx context.Context, revision int64, cal, mail, zone string, disable bool) error {
	if err := a.connectedApps.SetMeetingGrant(ctx, revision, cal, mail, zone, disable); err != nil {
		return err
	}
	run, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	a.refreshMeeting(run, time.Now())
	return nil
}
func (a *App) MeetingPreparation(ctx context.Context) (connectedapps.MeetingView, error) {
	view := connectedapps.MeetingView{Zone: "UTC", Status: "off"}
	st, err := a.connectedApps.State(ctx)
	if err != nil {
		return view, err
	}
	view.Revision = st.Revision
	for _, c := range st.Connections {
		if c.Status != "active" || c.Public {
			continue
		}
		opt := connectedapps.MeetingOption{ID: c.ID, Label: c.Name + " / " + c.Alias}
		switch c.Toolkit {
		case "googlecalendar":
			view.Calendars = append(view.Calendars, opt)
		case "gmail":
			view.Mailboxes = append(view.Mailboxes, opt)
		}
	}
	if st.Meeting == nil {
		return view, nil
	}
	g := *st.Meeting
	view.Enabled = true
	view.Calendar, view.Mail, view.Zone = g.Calendar.ID, g.Mail.ID, g.Zone
	view.Status = "pending"
	cache := &a.meetingCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	day, _, err := tomorrowWindow(time.Now(), g.Zone)
	if err != nil {
		return view, err
	}
	if cache.grant != g.ID || cache.day != day.Format("2006-01-02") {
		return view, nil
	}
	view.Status = cache.status
	if view.Status != "ready" {
		return view, nil
	}
	graph, err := a.selectedMeetingGraph(ctx)
	if err != nil || graph != cache.graph {
		view.Status = "unavailable"
		return view, nil
	}
	if err = a.connectedApps.ValidateMeeting(ctx, g); err != nil {
		view.Status = "unavailable"
		if errors.Is(err, connectedapps.ErrDenied) {
			view.Status = "revoked"
		}
		return view, nil
	}
	view.Brief = cache.brief
	return view, nil
}
