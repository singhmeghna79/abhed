package server

import (
	"context"
	"testing"

	"github.com/yuvrajsingh/abhed/internal/agent"
	"github.com/yuvrajsingh/abhed/internal/store"
)

// A store that also records sessions, so the test can prove the tap does not
// hide the optional interfaces the server asserts on the underlying store.
type recordingMem struct {
	*agent.MemStore
	created int
}

func (r *recordingMem) CreateSession(ctx context.Context, s store.SessionRecord) error {
	r.created++
	return nil
}
func (r *recordingMem) ListSessions(ctx context.Context, limit int) ([]store.SessionRecord, error) {
	return nil, nil
}

// The tap sees every appended event, and tapping a store must not cost it
// the capabilities the server discovers by type assertion. The second half is
// the one that bites: a wrapper that forwards only EventStore would make a
// durable store look like the memory driver, and session listing would
// silently vanish the day telemetry was switched on.
func TestEventTapSeesAppendsWithoutHidingTheStore(t *testing.T) {
	var seen []agent.EventType
	inner := &recordingMem{MemStore: agent.NewMemStore()}

	s := New(Options{
		Store:    inner,
		EventTap: func(ev agent.Event) { seen = append(seen, ev.Type) },
	})

	if s.sessions == nil {
		t.Fatal("tapping the store hid its SessionRecorder capability")
	}

	rec := agent.NewRecorder(s.store, "s1", "")
	if _, err := rec.Record(agent.EvSessionStarted, agent.ActorSystem, agent.Trusted, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Record(agent.EvUserMessage, agent.ActorUser, agent.Untrusted, agent.Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 || seen[0] != agent.EvSessionStarted || seen[1] != agent.EvUserMessage {
		t.Errorf("tap saw %v, want [session.started user.message]", seen)
	}
	// And the events reached the real store, in order.
	evs, _ := inner.Events("s1")
	if len(evs) != 2 {
		t.Errorf("underlying store holds %d events, want 2", len(evs))
	}
}

// No tap configured means no wrapper at all — not a wrapper around a nil
// function, which would panic on the first event.
func TestNoTapMeansNoWrapper(t *testing.T) {
	s := New(Options{Store: agent.NewMemStore()})
	if _, wrapped := s.store.(tapStore); wrapped {
		t.Error("store was wrapped with no tap configured")
	}
	rec := agent.NewRecorder(s.store, "s1", "")
	if _, err := rec.Record(agent.EvSessionStarted, agent.ActorSystem, agent.Trusted, nil); err != nil {
		t.Fatal(err)
	}
}
