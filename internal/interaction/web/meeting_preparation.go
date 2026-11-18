package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/z-chenhao/assistant/internal/connectedapps"
)

type meetingOwner interface {
	MeetingPreparation(context.Context) (connectedapps.MeetingView, error)
	ConfigureMeeting(context.Context, int64, string, string, string, bool) error
}

func (s *Server) meetingPage(ctx context.Context, p *page) error {
	p.Settings = &settingsView{Section: "meeting"}
	owner, ok := s.Core.(meetingOwner)
	if !ok {
		return connectedapps.ErrUnavailable
	}
	v, err := owner.MeetingPreparation(ctx)
	p.Meeting = &v
	return err
}
func (s *Server) configureMeeting(w http.ResponseWriter, r *http.Request) error {
	owner, ok := s.Core.(meetingOwner)
	if !ok {
		fail(w, 503)
		return nil
	}
	rev, err := strconv.ParseInt(r.PostFormValue("revision"), 10, 64)
	action := r.PostFormValue("action")
	if err != nil || rev < 0 || action != "enable" && action != "disable" {
		fail(w, 400)
		return nil
	}
	err = owner.ConfigureMeeting(r.Context(), rev, r.PostFormValue("calendar"), r.PostFormValue("mail"), r.PostFormValue("zone"), action == "disable")
	if err != nil {
		code := 503
		switch {
		case errors.Is(err, connectedapps.ErrConflict):
			code = 409
		case errors.Is(err, connectedapps.ErrInvalid):
			code = 400
		case errors.Is(err, connectedapps.ErrDenied):
			code = 403
		}
		fail(w, code)
		return nil
	}
	http.Redirect(w, r, "/settings/meeting-preparation", http.StatusSeeOther)
	return nil
}
