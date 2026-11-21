package connectedapps

import (
	"context"
	"errors"
	"testing"
)

func TestConnectedAppsMeetingLegacyGrantRequiresMailbox(t *testing.T) {
	ctx := context.Background()
	cal := Connection{ID: "cal", Toolkit: "googlecalendar", AccountID: "ca_cal", Status: "active"}
	store := &closeGoldenStore{state: State{Connections: []Connection{cal}}}
	service := Service{Store: store}
	// A missing mailbox in an older two-account grant is a broken binding.
	// Only a new, explicit grant can switch the consent to calendar only.
	g := MeetingGrant{ID: "older", Calendar: SourcePin{cal.ID, BindingHash(store.state, cal)}, Zone: "UTC"}
	store.state.Meeting = &g
	if err := service.ValidateMeeting(ctx, g); !errors.Is(err, ErrDenied) {
		t.Fatalf("legacy grant with missing mailbox: %v", err)
	}
	if err := service.save(ctx, store.state); err != nil || store.state.Meeting != nil {
		t.Fatalf("broken grant retained after connection update: %v", err)
	}
	if err := service.SetMeetingGrant(ctx, store.state.Revision, "cal", "", "UTC", false); err != nil {
		t.Fatal(err)
	}
	if err := service.save(ctx, store.state); err != nil || store.state.Meeting == nil || !store.state.Meeting.CalendarOnly {
		t.Fatalf("explicit calendar-only grant lost after connection update: %v", err)
	}
	store.state.Connections[0].AccountID = "ca_replacement"
	if err := service.save(ctx, store.state); err != nil || store.state.Meeting != nil {
		t.Fatalf("calendar rotation retained grant: %v", err)
	}
}
