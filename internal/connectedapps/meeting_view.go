package connectedapps

type MeetingOption struct{ ID, Label string }
type MeetingBrief struct{ Event, EventLink, Start, Subject, MailLink string }
type MeetingView struct {
	Revision             int64
	Calendars, Mailboxes []MeetingOption
	Calendar, Mail, Zone string
	Enabled              bool
	Status               string
	Brief                MeetingBrief
}
