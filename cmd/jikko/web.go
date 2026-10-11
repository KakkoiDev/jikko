package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// web serves the workspace like `serve` and opens it in the default browser
// once the port answers. It exists so that starting the UI is one memorable
// word; every serve flag is accepted and forwarded.
func web(args []string) error {
	var noOpen *bool
	var addr, maxUpload, runner *string
	var trustProxy, autoCommit *bool
	args, dir, _, err := flags("web", args, func(f *flag.FlagSet) {
		addr = f.String("addr", "127.0.0.1:8080", "listen address")
		maxUpload = f.String("max-upload", os.Getenv("JIKKO_MAX_UPLOAD"), "maximum upload size, such as 25M (or JIKKO_MAX_UPLOAD)")
		trustProxy = f.Bool("behind-proxy", false, "trust X-Forwarded-Proto from a reverse proxy when setting cookie Secure")
		autoCommit = f.Bool("auto-commit", false, "record every change as a Git commit with Jikko-Actor and Jikko-Operation trailers")
		runner = f.String("runner", os.Getenv("JIKKO_RUNNER"), "executable agents started from the browser run (or JIKKO_RUNNER)")
		noOpen = f.Bool("no-open", false, "do not open a browser")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("web")
	}
	forward := []string{"--dir", *dir, "--addr", *addr}
	if *maxUpload != "" {
		forward = append(forward, "--max-upload", *maxUpload)
	}
	if *trustProxy {
		forward = append(forward, "--behind-proxy")
	}
	if *autoCommit {
		forward = append(forward, "--auto-commit")
	}
	if *runner != "" {
		forward = append(forward, "--runner", *runner)
	}
	if !*noOpen {
		go openWhenUp(*addr)
	}
	return serve(forward)
}

// openWhenUp waits for the listener, then hands the URL to the OS.
func openWhenUp(addr string) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	url := "http://" + addr
	if strings.HasPrefix(addr, ":") {
		url = "http://127.0.0.1" + addr
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "jikko: could not open a browser (%v); visit %s\n", err, url)
	}
}
