// Command prview opens a GitHub PR for review in a local web page.
//
//	prview 482                                  PR 482 of the repo in this folder
//	prview owner/repo#482
//	prview https://github.com/owner/repo/pull/482
//	prview serve [-addr :7450]                  run the server in the foreground
//	prview stop                                 stop the server
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/claude"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/config"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/github"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/server"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			serve(os.Args[2:])
			return
		case "stop":
			stop()
			return
		}
	}
	open(os.Args[1:])
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", fmt.Sprintf(":%d", config.DefaultPort), "address to listen on (all interfaces by default)")
	fs.Parse(args)
	key, err := config.Key()
	if err != nil {
		log.Fatalf("access key: %v", err)
	}
	runner, err := claude.NewRunner()
	if err != nil {
		log.Printf("Claude panel off: %v", err)
		runner = nil
	}
	s := server.New(github.New(), runner, config.CacheDir(), key)
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	s.OnStop(func() {
		go func() {
			// Let the "ok" reach the caller, give open requests (a Claude
			// answer, say) a moment to finish, then close the rest.
			time.Sleep(100 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if srv.Shutdown(ctx) != nil {
				srv.Close()
			}
		}()
	})
	log.Printf("prview listening on %s", *addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	log.Print("prview stopped")
}

// stop asks the running server to shut down.
func stop() {
	if !serverUp() {
		fmt.Println("prview isn't running")
		return
	}
	key, err := config.Key()
	if err != nil {
		log.Fatalf("access key: %v", err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", config.DefaultPort)
	req, _ := http.NewRequest("POST", base+"/api/stop", nil)
	req.AddCookie(&http.Cookie{Name: server.CookieName, Value: key})
	req.Header.Set("Origin", base)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		log.Fatalf("stopping prview: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("stopping prview: the server answered %s", resp.Status)
	}
	for i := 0; i < 40 && serverUp(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("prview stopped")
}

func open(args []string) {
	fs := flag.NewFlagSet("prview", flag.ExitOnError)
	noOpen := fs.Bool("no-open", false, "print the link but don't open a browser")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: prview [-no-open] <number | owner/repo#number | PR link>\n       prview serve [-addr :7450]\n       prview stop")
	}
	fs.Parse(args)
	key, err := config.Key()
	if err != nil {
		log.Fatalf("access key: %v", err)
	}
	if err := ensureServer(); err != nil {
		log.Fatal(err)
	}
	pathPart := "/"
	if fs.NArg() > 0 {
		owner, repo, n, err := parsePR(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		pathPart = fmt.Sprintf("/%s/%s/pull/%d", owner, repo, n)
	}
	local := fmt.Sprintf("http://127.0.0.1:%d%s?key=%s", config.DefaultPort, pathPart, key)
	fmt.Println(local)
	if host, err := os.Hostname(); err == nil {
		fmt.Printf("from another machine: http://%s:%d%s?key=%s\n", host, config.DefaultPort, pathPart, key)
	}
	if !*noOpen && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "") {
		exec.Command("xdg-open", local).Start()
	}
}

var (
	urlRe   = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	shortRe = regexp.MustCompile(`^([^/\s]+)/([^#\s]+)#(\d+)$`)
)

// parsePR accepts a PR link, owner/repo#n, or a number (repo from the
// current folder's GitHub remote).
func parsePR(s string) (string, string, int, error) {
	if m := urlRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[3])
		return m[1], m[2], n, nil
	}
	if m := shortRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[3])
		return m[1], m[2], n, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "#"))
	if err != nil {
		return "", "", 0, fmt.Errorf("%q isn't a PR number, owner/repo#number or PR link", s)
	}
	out, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").Output()
	if err != nil {
		return "", "", 0, errors.New("can't tell which repo this folder is; use owner/repo#number or a PR link")
	}
	owner, repo, _ := strings.Cut(strings.TrimSpace(string(out)), "/")
	return owner, repo, n, nil
}

// ensureServer starts `prview serve` in the background if nothing answers
// on the port.
func ensureServer() error {
	if serverUp() {
		return nil
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", config.DefaultPort), time.Second)
	if err == nil {
		conn.Close()
		return fmt.Errorf("something else is using port %d", config.DefaultPort)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(config.CacheDir(), "server.log")
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "serve")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if serverUp() {
			return nil
		}
	}
	return fmt.Errorf("the server didn't start; see %s", logPath)
}

func serverUp() bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", config.DefaultPort))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b := make([]byte, 6)
	n, _ := resp.Body.Read(b)
	return string(b[:n]) == "prview"
}
