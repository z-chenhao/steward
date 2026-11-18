package composio

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"time"
)

var threadID = regexp.MustCompile("^[a-fA-F0-9]{1,64}$")

func ValidThreadID(id string) bool { return threadID.MatchString(id) }

type CalendarEvent struct {
	ID    string `json:"id"`
	Title string `json:"summary"`
	Link  string `json:"htmlLink"`
	Start struct {
		DateTime time.Time `json:"dateTime"`
	} `json:"start"`
}
type MailThread struct {
	ID string `json:"id"`
}
type ThreadMetadata struct {
	ID       string `json:"id"`
	Messages []struct {
		Payload struct {
			Headers []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"headers"`
		} `json:"payload"`
	} `json:"messages"`
}

// Only the fixed product reads below can choose the upstream URL. The
// broker uses POST for transport; the actual provider operation is always GET.
func (c Client) meetingRead(ctx context.Context, user, account, endpoint string, query url.Values, out any) error {
	if user == "" || !ValidID(account) {
		return &Error{Status: 400}
	}
	var reply struct {
		Status int             `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	body := struct {
		User     string `json:"user_id"`
		Account  string `json:"connected_account_id"`
		Method   string `json:"method"`
		Endpoint string `json:"endpoint"`
	}{user, account, "GET", endpoint + "?" + query.Encode()}
	if err := c.request(ctx, "POST", "/tools/execute/proxy", nil, body, &reply); err != nil {
		return err
	}
	if reply.Status != 200 || len(reply.Data) == 0 {
		return &Error{Status: reply.Status}
	}
	if err := json.Unmarshal(reply.Data, out); err != nil {
		return &Error{Status: 502}
	}
	return nil
}
func (c Client) TomorrowEvents(ctx context.Context, user, account string, start, end time.Time) ([]CalendarEvent, error) {
	var result struct {
		Items []CalendarEvent `json:"items"`
	}
	query := url.Values{"timeMin": {start.Format(time.RFC3339)}, "timeMax": {end.Format(time.RFC3339)}, "orderBy": {"startTime"}, "singleEvents": {"true"}, "maxResults": {"20"}, "fields": {"items(id,summary,htmlLink,start/dateTime)"}}
	err := c.meetingRead(ctx, user, account, "https://www.googleapis.com/calendar/v3/calendars/primary/events", query, &result)
	return result.Items, err
}
func (c Client) RecentThreads(ctx context.Context, user, account string) ([]MailThread, error) {
	var result struct {
		Threads []MailThread `json:"threads"`
	}
	err := c.meetingRead(ctx, user, account, "https://gmail.googleapis.com/gmail/v1/users/me/threads", url.Values{"q": {"newer_than:7d -in:spam -in:trash"}, "maxResults": {"10"}, "fields": {"threads(id)"}}, &result)
	return result.Threads, err
}
func (c Client) ThreadSubject(ctx context.Context, user, account, id string) (string, error) {
	if !ValidThreadID(id) {
		return "", &Error{Status: 400}
	}
	var result ThreadMetadata
	err := c.meetingRead(ctx, user, account, "https://gmail.googleapis.com/gmail/v1/users/me/threads/"+id, url.Values{"format": {"metadata"}, "metadataHeaders": {"Subject"}, "fields": {"id,messages(payload/headers)"}}, &result)
	if err != nil {
		return "", err
	}
	if result.ID != id {
		return "", &Error{Status: 502}
	}
	for i := len(result.Messages) - 1; i >= 0; i-- {
		for _, h := range result.Messages[i].Payload.Headers {
			if h.Name == "Subject" || h.Name == "subject" {
				return h.Value, nil
			}
		}
	}
	return "", nil
}

// Gmail's account-specific browser link uses the authorized account's identity.
func (c Client) MailboxAddress(ctx context.Context, user, account string) (string, error) {
	var result struct {
		Address string `json:"emailAddress"`
	}
	err := c.meetingRead(ctx, user, account, "https://gmail.googleapis.com/gmail/v1/users/me/profile", url.Values{"fields": {"emailAddress"}}, &result)
	return result.Address, err
}
