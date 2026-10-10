package jikko

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Presence is the runtime state of an identity that is currently running as
// an agent: which process it is, whether it is working, and on what. It is
// derived state and lives under .data/presence, which Open never indexes and
// Git never tracks; a stale file simply means the process stopped without
// saying goodbye.
type Presence struct {
	Identity string    `json:"identity"`
	State    string    `json:"state"` // working or idle
	Task     string    `json:"task,omitempty"`
	Workdir  string    `json:"workdir,omitempty"`
	Since    time.Time `json:"since"` // when the current state began
	Seen     time.Time `json:"seen"`  // last heartbeat
	Started  time.Time `json:"started"`
	PID      int       `json:"pid"`
	Host     string    `json:"host"`
	Runner   string    `json:"runner,omitempty"`

	LastTask     string    `json:"last_task,omitempty"`
	LastOutcome  string    `json:"last_outcome,omitempty"`
	LastFinished time.Time `json:"last_finished,omitempty"`

	// Online is computed on read: a heartbeat within PresenceStale.
	Online bool `json:"online"`
}

// PresenceStale is how long after its last heartbeat a presence still counts
// as online. Heartbeats are expected several times per period.
const PresenceStale = 90 * time.Second

func (w *Workspace) presenceDir() string { return filepath.Join(w.Root, ".data", "presence") }

func presenceFile(identity string) string {
	stem := strings.TrimSuffix(strings.TrimSpace(filepath.ToSlash(identity)), ".md")
	return strings.ReplaceAll(stem, "/", "--") + ".json"
}

// WritePresence records p for p.Identity, replacing any earlier record. The
// write is atomic so a reader never sees a torn file.
func (w *Workspace) WritePresence(p Presence) error {
	ref, ok := w.ResolveIdentity(p.Identity)
	if !ok {
		return fmt.Errorf("identity %q not found or ambiguous", p.Identity)
	}
	p.Identity = strings.TrimSuffix(ref.Path, ".md")
	p.Online = false
	if err := os.MkdirAll(w.presenceDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(w.presenceDir(), presenceFile(p.Identity))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// ClearPresence forgets an identity's presence. A missing record is not an error.
func (w *Workspace) ClearPresence(identity string) error {
	if p, ok := w.ResolveIdentity(identity); ok {
		identity = strings.TrimSuffix(p.Path, ".md")
	}
	err := os.Remove(filepath.Join(w.presenceDir(), presenceFile(identity)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Presences returns every recorded presence, online or not, in identity order.
// Records whose identity page no longer exists are skipped.
func (w *Workspace) Presences() []Presence {
	entries, err := os.ReadDir(w.presenceDir())
	if err != nil {
		return nil
	}
	now := time.Now()
	var out []Presence
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(w.presenceDir(), e.Name()))
		if err != nil {
			continue
		}
		var p Presence
		if json.Unmarshal(data, &p) != nil || p.Identity == "" {
			continue
		}
		if _, ok := w.ResolveIdentity(p.Identity); !ok {
			continue
		}
		p.Online = !p.Seen.IsZero() && now.Sub(p.Seen) <= PresenceStale
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity < out[j].Identity })
	return out
}

// PresenceOf returns the recorded presence of one identity.
func (w *Workspace) PresenceOf(identity string) (Presence, bool) {
	if p, ok := w.ResolveIdentity(identity); ok {
		identity = strings.TrimSuffix(p.Path, ".md")
	}
	for _, p := range w.Presences() {
		if p.Identity == identity {
			return p, true
		}
	}
	return Presence{}, false
}
