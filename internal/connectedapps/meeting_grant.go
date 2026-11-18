package connectedapps

import (
	"context"
	"crypto/rand"
	"net/mail"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/connector/composio"
)

type SourcePin struct {
	ID   string `json:"id"`
	Hash string `json:"binding_hash"`
}
type MeetingGrant struct {
	ID       string    `json:"grant_id"`
	Calendar SourcePin `json:"calendar"`
	Mail     SourcePin `json:"mail"`
	Zone     string    `json:"zone"`
}
type MeetingEvidence struct {
	Mailbox  string
	Events   []composio.CalendarEvent
	Threads  []composio.MailThread
	Subjects []string
}

func (s *Service) SetMeetingGrant(ctx context.Context, revision int64, calendar, mail, zone string, disable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.State(ctx)
	if err != nil {
		return err
	}
	if st.Revision != revision {
		return ErrConflict
	}
	if disable {
		st.Meeting = nil
		return s.Store.SaveApps(ctx, st, revision)
	}
	if zone == "" || zone == "Local" || len(zone) > 100 {
		return ErrInvalid
	}
	if _, e := time.LoadLocation(zone); e != nil {
		return ErrInvalid
	}
	pin := func(id, toolkit string) (SourcePin, error) {
		c := find(&st, id)
		if c == nil || c.Toolkit != toolkit || c.Public || c.AccountID == "" || c.Status != "active" {
			return SourcePin{}, ErrDenied
		}
		return SourcePin{c.ID, BindingHash(st, *c)}, nil
	}
	cal, err := pin(calendar, "googlecalendar")
	if err != nil {
		return err
	}
	inbox, err := pin(mail, "gmail")
	if err != nil {
		return err
	}
	st.Meeting = &MeetingGrant{ID: rand.Text(), Calendar: cal, Mail: inbox, Zone: zone}
	return s.Store.SaveApps(ctx, st, revision)
}

// Re-read both exact bindings before every content read and before publishing
// its result; another process may have revoked or rotated either connection.
func (s *Service) withMeetingAccounts(ctx context.Context, g MeetingGrant, read func(composio.Client, string, Connection, Connection) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, err := s.State(ctx)
	if err != nil {
		return err
	}
	if st.Meeting == nil || *st.Meeting != g {
		return ErrDenied
	}
	client := s.client(st)
	check := func(pin SourcePin, toolkit string) (Connection, error) {
		c := find(&st, pin.ID)
		if c == nil || c.Public || c.Toolkit != toolkit || c.Status != "active" || BindingHash(st, *c) != pin.Hash {
			return Connection{}, ErrDenied
		}
		return *c, nil
	}
	cal, err := check(g.Calendar, "googlecalendar")
	if err != nil {
		return err
	}
	mail, err := check(g.Mail, "gmail")
	if err != nil {
		return err
	}
	for _, c := range []Connection{cal, mail} {
		if err = checkExecutionAccount(ctx, client, st, c); err != nil {
			return err
		}
	}
	latest, err := s.State(ctx)
	if err != nil {
		return err
	}
	if latest.Revision != st.Revision {
		return ErrDenied
	}
	if read == nil {
		return nil
	}
	return read(client, st.UserID, cal, mail)
}
func (s *Service) ValidateMeeting(ctx context.Context, g MeetingGrant) error {
	return s.withMeetingAccounts(ctx, g, nil)
}

func usableTitle(x string) bool { return strings.TrimSpace(x) != "" && len([]rune(x)) <= 300 }
func (s *Service) ReadMeetingEvidence(ctx context.Context, g MeetingGrant, start, end time.Time, fence func(context.Context) error) (MeetingEvidence, error) {
	var out MeetingEvidence
	run := func(f func(composio.Client, string, Connection, Connection) error) error {
		if e := fence(ctx); e != nil {
			return e
		}
		return s.withMeetingAccounts(ctx, g, f)
	}
	err := run(func(c composio.Client, user string, cal, _ Connection) error {
		rows, e := c.TomorrowEvents(ctx, user, cal.AccountID, start, end)
		if e != nil {
			return e
		}
		if len(rows) > 20 {
			return ErrInvalid
		}
		for _, event := range rows {
			if event.ID != "" && usableTitle(event.Title) && !event.Start.DateTime.Before(start) && event.Start.DateTime.Before(end) {
				out.Events = append(out.Events, event)
			}
		}
		return nil
	})
	if err != nil || len(out.Events) == 0 {
		return out, err
	}
	var threads []composio.MailThread
	err = run(func(c composio.Client, user string, _, mail Connection) error {
		var e error
		threads, e = c.RecentThreads(ctx, user, mail.AccountID)
		return e
	})
	if err != nil {
		return MeetingEvidence{}, err
	}
	if len(threads) > 10 {
		return MeetingEvidence{}, ErrInvalid
	}
	for _, thread := range threads {
		if !composio.ValidThreadID(thread.ID) {
			return MeetingEvidence{}, ErrInvalid
		}
		var subject string
		err = run(func(c composio.Client, user string, _, mail Connection) error {
			var e error
			subject, e = c.ThreadSubject(ctx, user, mail.AccountID, thread.ID)
			return e
		})
		if err != nil {
			return MeetingEvidence{}, err
		}
		if usableTitle(subject) {
			out.Threads = append(out.Threads, thread)
			out.Subjects = append(out.Subjects, subject)
		}
	}
	if len(out.Subjects) > 0 {
		err = run(func(c composio.Client, user string, _, inbox Connection) error {
			address, e := c.MailboxAddress(ctx, user, inbox.AccountID)
			if e != nil {
				return e
			}
			m, e := mail.ParseAddress(address)
			if e != nil || m.Address != address {
				return ErrInvalid
			}
			out.Mailbox = address
			return nil
		})
		if err != nil {
			return MeetingEvidence{}, err
		}
	}
	return out, nil
}
