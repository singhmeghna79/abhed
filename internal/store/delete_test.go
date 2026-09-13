package store

import (
	"context"
	"errors"
	"testing"

	"github.com/yuvrajsingh/titan/internal/agent"
)

// A deleted session must be gone from every read path — list, get, events —
// while its rows stay in the table, because the events trigger forbids DELETE
// and that guarantee is the point of this store. Both halves are checked: a
// delete that hides nothing is a privacy bug, and one that removed rows would
// mean the append-only trigger is not doing its job.
func TestDeleteSessionHidesEverywhereButKeepsRows(t *testing.T) {
	p := openStore(t, "t-del")
	ctx := context.Background()
	id := "sess-del-" + t.Name()
	newSession(t, p, id, "t-del")
	if err := p.Append(ev(id, 1, agent.EvUserMessage, agent.Trusted, agent.Message{Text: "secret"})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if evs, _ := p.Events(id); len(evs) != 1 {
		t.Fatalf("precondition: expected 1 event, got %d", len(evs))
	}

	if err := p.DeleteSession(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := p.DeleteSession(id); err != nil {
		t.Fatalf("second delete should be a no-op, got %v", err)
	}

	if evs, err := p.Events(id); err != nil || len(evs) != 0 {
		t.Errorf("events still readable after delete: %d events, err=%v", len(evs), err)
	}
	if _, err := p.GetSession(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession after delete: want ErrNotFound, got %v", err)
	}
	recs, err := p.ListSessions(ctx, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range recs {
		if r.ID == id {
			t.Errorf("deleted session still listed")
		}
	}

	// The rows are still there for the audit: the marking is the record of
	// the delete, not a way around the append-only log.
	var events int
	var deletedBy string
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE session_id = $1`, id).Scan(&events); err != nil {
		t.Fatalf("count: %v", err)
	}
	if err := p.pool.QueryRow(ctx, `SELECT deleted_by FROM sessions WHERE id = $1 AND deleted_at IS NOT NULL`, id).Scan(&deletedBy); err != nil {
		t.Fatalf("tombstone missing: %v", err)
	}
	if events != 1 || deletedBy != "tester" {
		t.Errorf("audit rows: events=%d deleted_by=%q, want 1 and \"tester\"", events, deletedBy)
	}
}
