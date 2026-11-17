package agent

import (
	"context"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

func TestUIPreviewQueueCoalescesWithoutBlockingModelStream(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	projected := make(chan string, 8)
	published := make(chan string, 8)
	q := newUIPreviewQueue(context.Background(), func(ctx context.Context, raw string) (ui.Preview, bool) {
		projected <- raw
		if raw == "a1" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ui.Preview{}, false
			}
		}
		return ui.Preview{Title: raw}, true
	}, func(key string, value ui.Preview) { published <- key + ":" + value.Title })
	defer q.stop()
	q.submit("a", "a1")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("projector did not start")
	}
	submitted := make(chan struct{})
	go func() {
		q.submit("a", "a2")
		q.submit("a", "a3")
		q.submit("b", "b1")
		q.submit("b", "b2")
		close(submitted)
	}()
	select {
	case <-submitted:
	case <-time.After(time.Second):
		t.Fatal("slow preview blocked producer")
	}
	close(release)
	for _, expected := range []string{"a:a1", "a:a3", "b:b2"} {
		select {
		case value := <-published:
			if value != expected {
				t.Fatal("lost latest prefix or call order", value, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("latest prefix not published", expected)
		}
	}
	for _, expected := range []string{"a1", "a3", "b2"} {
		select {
		case value := <-projected:
			if value != expected {
				t.Fatal("superseded prefix executed", value, expected)
			}
		default:
			t.Fatal("missing projection", expected)
		}
	}
	select {
	case value := <-projected:
		t.Fatal("unexpected extra projection", value)
	default:
	}
}

func TestUIPreviewQueueStopCancelsAndJoinsWithoutLatePublish(t *testing.T) {
	entered := make(chan struct{})
	published := make(chan struct{}, 1)
	q := newUIPreviewQueue(context.Background(), func(ctx context.Context, raw string) (ui.Preview, bool) {
		close(entered)
		<-ctx.Done()
		return ui.Preview{Title: raw}, true
	}, func(string, ui.Preview) { published <- struct{}{} })
	q.submit("a", "pending")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("projection did not start")
	}
	q.submit("b", "must not execute")
	stopped := make(chan struct{})
	go func() { q.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("generation transition waited on uncancelled preview")
	}
	q.submit("c", "late")
	q.stop()
	select {
	case <-published:
		t.Fatal("cancelled generation published a late preview")
	default:
	}
}
