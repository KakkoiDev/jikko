package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	jikko "github.com/KakkoiDev/jikko"
)

// The web interface can start and stop an agent identity. What it runs is
// fixed by whoever started the server (--runner or JIKKO_RUNNER), never by
// page content, so writing Markdown cannot make the server execute anything.
// Only someone who may write an agent's identity page may start or stop it.

// canRunAgent reports whether actor may start or stop the agent identity p.
func (s *server) canRunAgent(ws *jikko.Workspace, actor string, p *jikko.Page) bool {
	return s.runner != "" && actor != "" && p.Kind == jikko.Identity && len(p.Members) == 0 &&
		isAgent(ws, strings.TrimSuffix(p.Path, ".md")) && ws.Allowed(actor, p, jikko.Write)
}

func (s *server) agentControl(w http.ResponseWriter, r *http.Request) {
	ws, actor, ok := s.mutationContext(w, r)
	if !ok {
		return
	}
	pages, err := ws.ReadMany(actor, r.PathValue("path"))
	if err != nil {
		s.refuse(w, r, http.StatusNotFound, actor, msgNotFound)
		return
	}
	p := pages[0]
	if s.runner == "" {
		s.refuse(w, r, http.StatusConflict, actor, "This server was started without a runner. Restart it with `jikko web --runner PATH` to start agents from the browser.")
		return
	}
	if !s.canRunAgent(ws, actor, p) {
		s.refuse(w, r, http.StatusForbidden, actor, "Only someone who may edit an agent's identity page may start or stop it.")
		return
	}
	ref := strings.TrimSuffix(p.Path, ".md")
	var flash string
	switch r.PostFormValue("op") {
	case "start":
		err = s.startAgent(ws, ref)
		flash = p.Title + " started."
	case "stop":
		err = stopAgent(ws, ref)
		flash = p.Title + " is stopping; a task in progress is interrupted."
	default:
		s.refuse(w, r, http.StatusBadRequest, actor, "Unknown agent operation.")
		return
	}
	if err != nil {
		s.respond(w, r, http.StatusConflict, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Error: err.Error(), Detail: s.detail(ws, actor, p)})
		return
	}
	s.done(w, r, ws, actor, p.Path, flash)
}

// startAgent launches `jikko identity <ref> start` in its own process group,
// so it outlives the web server, with its output in .data/logs.
func (s *server) startAgent(ws *jikko.Workspace, ref string) error {
	if pr, ok := ws.PresenceOf(ref); ok && pr.Online {
		return fmt.Errorf("%s is already running (pid %d)", ref, pr.PID)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logs := filepath.Join(ws.Root, ".data", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(logs, filepath.Base(ref)+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command(self, "identity", ref, "start", "--dir", ws.Root, "--runner", s.runner)
	cmd.Dir = ws.Root
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// Wait for its presence record, so the page shows it running.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("the agent exited at once (%v); see .data/logs/%s.log", err, filepath.Base(ref))
		case <-deadline:
			return nil
		case <-time.After(100 * time.Millisecond):
			if pr, ok := ws.PresenceOf(ref); ok && pr.Online {
				return nil
			}
		}
	}
}

func stopAgent(ws *jikko.Workspace, ref string) error {
	pr, ok := ws.PresenceOf(ref)
	if !ok || !pr.Online {
		return errors.New(ref + " is not running")
	}
	if host, _ := os.Hostname(); pr.Host != host {
		return fmt.Errorf("%s runs on %s; stop it there", ref, pr.Host)
	}
	if err := syscall.Kill(pr.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop pid %d: %w", pr.PID, err)
	}
	// The loop clears its presence on exit; wait briefly so the page agrees.
	for i := 0; i < 30; i++ {
		if p, ok := ws.PresenceOf(ref); !ok || !p.Online {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}
