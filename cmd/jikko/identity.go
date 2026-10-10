package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	jikko "github.com/KakkoiDev/jikko"
)

const identityUsage = `usage:
  jikko identity list [--json]
  jikko identity <name> status [--json]
  jikko identity <name> start [--runner <cmd>] [--sandbox <dir>] [--interval 60s] [--once] [--token <t>]
  jikko identity <name> stop

list shows every identity that is or was running as an agent: online or not,
idle or working, and on which task.

start runs an agent as the identity: every task page assigned to it (directly or
through a group) whose status is todo is handed to the runner, one at a time,
then the loop sleeps --interval and looks again. --once does a single pass.
While it runs it records its presence under .data/presence so the UI and
"identity list" can show it; stop sends it SIGTERM.

The runner is any executable (--runner, or JIKKO_RUNNER). It is started once per
task with the task's project directory as working directory and:
  JIKKO_DIR        the workspace
  JIKKO_TOKEN      a credential for the identity (--token, or minted)
  JIKKO_IDENTITY   the identity name
  JIKKO_TASK       the task reference, e.g. tasks/wc-cli
  JIKKO_BIN        this binary
Project directories are <sandbox>/<task stem>; --sandbox defaults to
<workspace>/.data/work/<identity>, which Jikko never indexes.

status shows the identity's presence and the tasks start would run.`

func identity(args []string) error {
	if len(args) >= 1 && args[0] == "list" {
		return identityList(args[1:])
	}
	if len(args) < 2 {
		return errors.New(identityUsage)
	}
	name, verb := args[0], args[1]
	switch verb {
	case "start":
		return identityStart(name, args[2:])
	case "status":
		return identityStatus(name, args[2:])
	case "stop":
		return identityStop(name, args[2:])
	}
	return errors.New(identityUsage)
}

func identityList(args []string) error {
	var asJSON *bool
	args, dir, _, err := flags("identity list", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New(identityUsage)
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	ps := w.Presences()
	if *asJSON {
		if ps == nil {
			ps = []jikko.Presence{}
		}
		return json.NewEncoder(os.Stdout).Encode(ps)
	}
	if len(ps) == 0 {
		fmt.Println("no agents have run in this workspace")
		return nil
	}
	for _, p := range ps {
		state := "offline"
		if p.Online {
			state = p.State
		}
		line := fmt.Sprintf("%-24s %-8s", p.Identity, state)
		if p.Online && p.State == "working" {
			line += fmt.Sprintf(" %s (for %s)", p.Task, since(p.Since))
		} else if p.LastTask != "" {
			line += fmt.Sprintf(" last: %s %s, %s ago", p.LastTask, p.LastOutcome, since(p.LastFinished))
		}
		fmt.Println(line)
	}
	return nil
}

func identityStatus(name string, args []string) error {
	var asJSON *bool
	args, dir, _, err := flags("identity status", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New(identityUsage)
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := resolveIndividual(w, name)
	if err != nil {
		return err
	}
	tasks := queuedTasks(w, actor)
	if *asJSON {
		var presence *jikko.Presence
		if p, ok := w.PresenceOf(actor); ok {
			presence = &p
		}
		if tasks == nil {
			tasks = []*jikko.Page{}
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Presence *jikko.Presence `json:"presence,omitempty"`
			Queue    []*jikko.Page   `json:"queue"`
		}{presence, tasks})
	}
	if p, ok := w.PresenceOf(actor); ok {
		switch {
		case p.Online && p.State == "working":
			fmt.Printf("%s: online, working on %s for %s (pid %d on %s)\n", actor, p.Task, since(p.Since), p.PID, p.Host)
		case p.Online:
			fmt.Printf("%s: online, idle for %s (pid %d on %s)\n", actor, since(p.Since), p.PID, p.Host)
		default:
			fmt.Printf("%s: offline, last seen %s ago\n", actor, since(p.Seen))
		}
	} else {
		fmt.Printf("%s: not running\n", actor)
	}
	if len(tasks) == 0 {
		fmt.Println("queue: empty")
		return nil
	}
	fmt.Println("queue:")
	for _, t := range tasks {
		fmt.Printf("  %s\t%s\n", strings.TrimSuffix(t.Path, ".md"), t.Title)
	}
	return nil
}

func identityStop(name string, args []string) error {
	args, dir, _, err := flags("identity stop", args, nil)
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New(identityUsage)
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := resolveIndividual(w, name)
	if err != nil {
		return err
	}
	p, ok := w.PresenceOf(actor)
	if !ok || !p.Online {
		return fmt.Errorf("%s is not running", actor)
	}
	host, _ := os.Hostname()
	if p.Host != host {
		return fmt.Errorf("%s runs on %s (pid %d); stop it there", actor, p.Host, p.PID)
	}
	if err := syscall.Kill(p.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop pid %d: %w", p.PID, err)
	}
	fmt.Printf("sent SIGTERM to %s (pid %d)\n", actor, p.PID)
	return nil
}

func identityStart(name string, args []string) error {
	var runner, sandbox *string
	var interval *time.Duration
	var once *bool
	// --token comes from the shared flag set: here it is the credential the
	// runner receives, minted when absent.
	args, dir, token, err := flags("identity start", args, func(f *flag.FlagSet) {
		runner = f.String("runner", os.Getenv("JIKKO_RUNNER"), "executable run once per task (or JIKKO_RUNNER)")
		sandbox = f.String("sandbox", "", "where task project directories are created")
		interval = f.Duration("interval", time.Minute, "pause between passes")
		once = f.Bool("once", false, "run one pass and exit")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New(identityUsage)
	}
	if *runner == "" {
		return errors.New("identity start: --runner (or JIKKO_RUNNER) is required")
	}
	runnerPath, err := exec.LookPath(*runner)
	if err != nil {
		return fmt.Errorf("identity start: runner %q: %w", *runner, err)
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	w, err := openWorkspace(root)
	if err != nil {
		return err
	}
	actor, err := resolveIndividual(w, name)
	if err != nil {
		return err
	}
	ident := filepath.Base(actor)
	if p, ok := w.PresenceOf(actor); ok && p.Online {
		return fmt.Errorf("%s is already running (pid %d on %s); stop it first", actor, p.PID, p.Host)
	}
	if *sandbox == "" {
		*sandbox = filepath.Join(root, ".data", "work", ident)
	}
	if *token == "" {
		t, err := w.CreateCredential(actor)
		if err != nil {
			return fmt.Errorf("identity start: %w", err)
		}
		*token = t
		fmt.Fprintf(os.Stderr, "jikko: minted a credential for %s; revoke all of its credentials with `jikko auth revoke %s`\n", ident, ident)
	}
	self, _ := os.Executable()
	host, _ := os.Hostname()

	// Presence: written now, refreshed by a heartbeat, cleared on exit.
	now := time.Now()
	beat := &heartbeat{w: w, p: jikko.Presence{
		Identity: actor, State: "idle", Since: now, Seen: now, Started: now,
		PID: os.Getpid(), Host: host, Runner: runnerPath,
	}}
	beat.write()
	stopBeat := beat.start(15 * time.Second)
	defer func() {
		stopBeat()
		_ = w.ClearPresence(actor)
	}()

	// SIGINT/SIGTERM ends the loop, forwards the signal to a running runner,
	// and still clears the presence record through the deferred call.
	var current struct {
		sync.Mutex
		cmd *exec.Cmd
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	stopping := make(chan struct{})
	go func() {
		<-sigs
		close(stopping)
		current.Lock()
		if current.cmd != nil && current.cmd.Process != nil {
			_ = current.cmd.Process.Signal(syscall.SIGTERM)
		}
		current.Unlock()
	}()

	for {
		w, err := openWorkspace(root) // reload: humans and runs change files between passes
		if err != nil {
			return err
		}
		tasks := queuedTasks(w, actor)
		if len(tasks) == 0 {
			fmt.Fprintf(os.Stderr, "jikko: %s: nothing to do\n", ident)
		}
		for _, t := range tasks {
			select {
			case <-stopping:
				return nil
			default:
			}
			ref := strings.TrimSuffix(t.Path, ".md")
			work := filepath.Join(*sandbox, filepath.Base(ref))
			if err := os.MkdirAll(work, 0o755); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "jikko: %s: %s -> %s\n", ident, ref, work)
			beat.set(func(p *jikko.Presence) { p.State, p.Task, p.Workdir, p.Since = "working", ref, work, time.Now() })

			cmd := exec.Command(runnerPath)
			cmd.Dir = work
			// No stdin: a runner that reads piped input (Pi does) would otherwise
			// wait forever when jikko itself runs without a terminal.
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			cmd.Env = append(os.Environ(),
				"JIKKO_DIR="+root,
				"JIKKO_TOKEN="+*token,
				"JIKKO_IDENTITY="+ident,
				"JIKKO_TASK="+ref,
				"JIKKO_BIN="+self,
			)
			current.Lock()
			current.cmd = cmd
			current.Unlock()
			started := time.Now()
			runErr := cmd.Run()
			current.Lock()
			current.cmd = nil
			current.Unlock()
			outcome := "ok"
			if runErr != nil {
				outcome = runErr.Error()
			}
			fmt.Fprintf(os.Stderr, "jikko: %s: %s finished in %s (%s)\n", ident, ref, time.Since(started).Round(time.Second), outcome)
			beat.set(func(p *jikko.Presence) {
				p.State, p.Task, p.Workdir, p.Since = "idle", "", "", time.Now()
				p.LastTask, p.LastOutcome, p.LastFinished = ref, outcome, time.Now()
			})
		}
		if *once {
			return nil
		}
		select {
		case <-stopping:
			return nil
		case <-time.After(*interval):
		}
	}
}

// heartbeat keeps a presence record fresh from a goroutine.
type heartbeat struct {
	mu sync.Mutex
	w  *jikko.Workspace
	p  jikko.Presence
}

func (h *heartbeat) write() {
	h.mu.Lock()
	h.p.Seen = time.Now()
	p := h.p
	h.mu.Unlock()
	if err := h.w.WritePresence(p); err != nil {
		fmt.Fprintf(os.Stderr, "jikko: presence: %v\n", err)
	}
}

func (h *heartbeat) set(f func(*jikko.Presence)) {
	h.mu.Lock()
	f(&h.p)
	h.mu.Unlock()
	h.write()
}

func (h *heartbeat) start(every time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				h.write()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// since renders how long ago t was, coarsely.
func since(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// resolveIndividual maps a name to an individual Identity's reference.
func resolveIndividual(w *jikko.Workspace, name string) (string, error) {
	p, ok := w.ResolveIdentity(name)
	if !ok {
		return "", fmt.Errorf("identity %q not found or ambiguous", name)
	}
	if len(p.Members) != 0 {
		return "", fmt.Errorf("identity %q is a group; start an individual", name)
	}
	return strings.TrimSuffix(p.Path, ".md"), nil
}

// queuedTasks returns readable todo tasks assigned to actor, directly or
// through a group it belongs to, in path order.
func queuedTasks(w *jikko.Workspace, actor string) []*jikko.Page {
	var out []*jikko.Page
	for _, p := range w.List(jikko.Task, "") {
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(p.Metadata["status"])), "todo") {
			continue
		}
		if !w.Allowed(actor, p, jikko.Read) {
			continue
		}
		for _, a := range assignees(p) {
			if assignedTo(w, actor, a) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

func assignees(p *jikko.Page) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				out = append(out, strings.TrimPrefix(s, "@"))
			}
		case []any:
			for _, i := range x {
				walk(i)
			}
		}
	}
	walk(p.Metadata["assignee"])
	return out
}

func assignedTo(w *jikko.Workspace, actor, ref string) bool {
	target, ok := w.ResolveIdentity(ref)
	if !ok {
		return false
	}
	tref := strings.TrimSuffix(target.Path, ".md")
	if tref == actor {
		return true
	}
	return len(target.Members) != 0 && w.MemberOf(actor, tref)
}
