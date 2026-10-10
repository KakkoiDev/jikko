package jikko

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPresenceRoundTrip(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "identity"), 0o755))
	must(os.WriteFile(filepath.Join(root, "identity", "bot.md"), []byte("---\ntype: identity\n---\n\n# Bot\n"), 0o644))
	w, err := Open(root)
	must(err)

	if got := w.Presences(); len(got) != 0 {
		t.Fatalf("expected no presences, got %d", len(got))
	}
	now := time.Now()
	must(w.WritePresence(Presence{Identity: "bot", State: "working", Task: "tasks/x", Since: now, Seen: now, Started: now, PID: 1}))

	got := w.Presences()
	if len(got) != 1 {
		t.Fatalf("expected 1 presence, got %d", len(got))
	}
	if got[0].Identity != "identity/bot" || !got[0].Online || got[0].State != "working" || got[0].Task != "tasks/x" {
		t.Fatalf("unexpected presence %+v", got[0])
	}
	if _, ok := w.PresenceOf("identity/bot"); !ok {
		t.Fatal("PresenceOf by path should find the record")
	}

	// A heartbeat older than the stale window is offline but still listed.
	old := now.Add(-2 * PresenceStale)
	must(w.WritePresence(Presence{Identity: "identity/bot", State: "idle", Since: old, Seen: old, Started: old, PID: 1}))
	if p, _ := w.PresenceOf("bot"); p.Online {
		t.Fatal("stale heartbeat should read as offline")
	}

	must(w.ClearPresence("bot"))
	must(w.ClearPresence("bot")) // idempotent
	if got := w.Presences(); len(got) != 0 {
		t.Fatalf("expected presence cleared, got %d", len(got))
	}
	if err := w.WritePresence(Presence{Identity: "nobody"}); err == nil {
		t.Fatal("writing presence for an unknown identity should fail")
	}
}
