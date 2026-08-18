// Package supervisor spawnt `opencode serve` als Child-Prozess und hält
// ihn am Leben. Der Server ist ein Kind des Daemons und stirbt mit ihm —
// er hängt nie als offener Endpoint herum (PLAN.md §2).
package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Config beschreibt, wie der opencode-Server zu starten ist.
type Config struct {
	// BauDir ist der Root des Baus. XDG_CONFIG_HOME wird auf
	// <BauDir>/.opencode-home gesetzt und das CWD des Servers liegt im
	// Bau — harte Invariante gegen die AGENTS.md-Leckage (PLAN.md §3).
	BauDir string

	// Binary ist der opencode-Befehl. Leer = "opencode" aus dem PATH.
	Binary string

	// HealthTimeout begrenzt das Warten auf Listen-Zeile und ersten
	// erfolgreichen Health-Check nach dem Spawn. Leer = 30s.
	HealthTimeout time.Duration

	// StopTimeout begrenzt das Warten auf ein sauberes Ende nach
	// SIGTERM, danach SIGKILL. Leer = 10s.
	StopTimeout time.Duration

	// Logf empfängt Diagnose-Zeilen (Restart, Backoff, Server-Output).
	// Leer = verworfen.
	Logf func(format string, args ...any)
}

// listenRe extrahiert den zugewiesenen Port aus der Startzeile des
// Servers. `--port 0` heißt „auto": bevorzugt 4096, bei Belegung ein
// ephemerer Port — deshalb wird der Port immer geparst und nie
// angenommen (PLAN.md §11.4).
var listenRe = regexp.MustCompile(`listening on (http://127\.0\.0\.1:\d+)`)

// proc ist ein gestarteter Server-Prozess. Genau eine Goroutine ruft
// cmd.Wait() auf; alle anderen warten über done.
type proc struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error // gültig nach close(done)
}

// Supervisor verwaltet genau einen opencode-Server-Prozess.
type Supervisor struct {
	cfg Config

	mu      sync.Mutex
	proc    *proc
	baseURL string
}

// New validiert die Config. BauDir muss existieren und ein Verzeichnis
// sein, inkl. .opencode-home/opencode/opencode.json — sonst liefe der
// Server unbemerkt mit der Alltags-Config des Nutzers.
func New(cfg Config) (*Supervisor, error) {
	if cfg.BauDir == "" {
		return nil, errors.New("supervisor: BauDir missing")
	}
	abs, err := filepath.Abs(cfg.BauDir)
	if err != nil {
		return nil, fmt.Errorf("supervisor: resolving BauDir: %w", err)
	}
	cfg.BauDir = abs
	if fi, err := os.Stat(cfg.BauDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("supervisor: BauDir %q is not a directory", cfg.BauDir)
	}
	confJSON := filepath.Join(cfg.BauDir, ".opencode-home", "opencode", "opencode.json")
	if _, err := os.Stat(confJSON); err != nil {
		return nil, fmt.Errorf("supervisor: isolated config missing (%s): %w", confJSON, err)
	}
	// AGENTS.md-Leckage (§3): Liegt eine Agent-Instruktionsdatei im
	// BauDir, ist das ein Code-Repo, kein Bau — der Server liefe mit
	// CWD neben den Entwickler-Instruktionen, und die Hasen läsen sie.
	// `hasenbau init` legt solche Dateien nie an.
	for _, marker := range []string{"AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(cfg.BauDir, marker)); err == nil {
			return nil, fmt.Errorf("supervisor: BauDir %q contains %s — that is a code repo, not a Bau (PLAN.md §3)", cfg.BauDir, marker)
		}
	}
	if cfg.Binary == "" {
		cfg.Binary = "opencode"
	}
	if cfg.HealthTimeout == 0 {
		cfg.HealthTimeout = 30 * time.Second
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 10 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Supervisor{cfg: cfg}, nil
}

// BaseURL liefert die Adresse des laufenden Servers, "" wenn keiner läuft.
func (s *Supervisor) BaseURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseURL
}

// Start spawnt den Server und kehrt zurück, sobald er den Health-Check
// besteht. Der Prozess läuft danach weiter; Stop beendet ihn.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.proc != nil {
		s.mu.Unlock()
		return errors.New("supervisor: server already running")
	}
	s.mu.Unlock()

	cmd := exec.Command(s.cfg.Binary, "serve", "--port", "0", "--hostname", "127.0.0.1")
	// Invariante (PLAN.md §3): CWD immer im Bau, nie im Projekt-Root.
	cmd.Dir = s.cfg.BauDir
	cmd.Env = isolatedEnv(s.cfg.BauDir)
	// Eigene Prozessgruppe, damit Stop auch Kindprozesse des Servers
	// erwischt und keine Zombies bleiben.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("supervisor: stdout: %w", err)
	}
	cmd.Stderr = cmd.Stdout // stderr in dieselbe Pipe (Warnungen, Panics)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("supervisor: %s starten: %w", s.cfg.Binary, err)
	}
	p := &proc{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()

	urlCh := make(chan string, 1)
	go s.scanOutput(stdout, urlCh)

	baseURL, err := s.awaitListen(ctx, p, urlCh)
	if err == nil {
		err = s.awaitHealthy(ctx, p, baseURL)
	}
	if err != nil {
		terminate(p, s.cfg.StopTimeout)
		return err
	}

	s.mu.Lock()
	s.proc = p
	s.baseURL = baseURL
	s.mu.Unlock()
	s.cfg.Logf("supervisor: Server bereit auf %s (pid %d)", baseURL, cmd.Process.Pid)
	return nil
}

// scanOutput loggt Server-Zeilen und meldet die Listen-URL.
func (s *Supervisor) scanOutput(r io.Reader, urlCh chan<- string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		s.cfg.Logf("opencode: %s", line)
		if m := listenRe.FindStringSubmatch(line); m != nil {
			select {
			case urlCh <- m[1]:
			default:
			}
		}
	}
}

// awaitListen wartet auf die Listen-Zeile oder das vorzeitige Ende des
// Prozesses.
func (s *Supervisor) awaitListen(ctx context.Context, p *proc, urlCh <-chan string) (string, error) {
	select {
	case u := <-urlCh:
		return u, nil
	case <-p.done:
		return "", fmt.Errorf("supervisor: Server endete vor dem Listen: %v", p.waitErr)
	case <-time.After(s.cfg.HealthTimeout):
		return "", errors.New("supervisor: timed out waiting for the listen line")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// awaitHealthy pollt /config, bis der Server antwortet.
func (s *Supervisor) awaitHealthy(ctx context.Context, p *proc, baseURL string) error {
	deadline := time.Now().Add(s.cfg.HealthTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fmt.Errorf("supervisor: server exited before the health check: %v", p.waitErr)
		default:
		}
		resp, err := client.Get(baseURL + "/config")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("supervisor: health check on %s/config failed", baseURL)
}

// Stop beendet den Server sauber: SIGTERM an die Prozessgruppe, nach
// StopTimeout SIGKILL. Kein Fehler, wenn kein Server läuft.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	p := s.proc
	s.proc = nil
	s.baseURL = ""
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	return terminate(p, s.cfg.StopTimeout)
}

// Run hält den Server am Leben, bis ctx endet: Crash → Restart mit
// exponentiellem Backoff (1s..30s), zurückgesetzt nach 60s stabiler
// Laufzeit. Beim ctx-Ende wird der Server sauber gestoppt.
func (s *Supervisor) Run(ctx context.Context) error {
	backoff := time.Second
	const backoffMax = 30 * time.Second
	const stableAfter = 60 * time.Second

	for {
		started := time.Now()
		err := s.Start(ctx)
		if err == nil {
			err = s.waitExit(ctx)
		}
		if ctx.Err() != nil {
			stopErr := s.Stop()
			return errors.Join(ctx.Err(), stopErr)
		}
		if time.Since(started) > stableAfter {
			backoff = time.Second
		}
		s.cfg.Logf("supervisor: Server weg (%v), Restart in %s", err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return errors.Join(ctx.Err(), s.Stop())
		}
		if backoff *= 2; backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// waitExit blockiert, bis der laufende Server von selbst endet oder ctx
// abbricht.
func (s *Supervisor) waitExit(ctx context.Context) error {
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	if p == nil {
		return errors.New("supervisor: no running server")
	}
	select {
	case <-p.done:
		s.mu.Lock()
		if s.proc == p {
			s.proc = nil
			s.baseURL = ""
		}
		s.mu.Unlock()
		return p.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isolatedEnv erbt die Environment des Daemons, biegt aber XDG_CONFIG_HOME
// auf den Bau um. XDG_DATA_HOME bleibt geerbt → auth.json wird mit dem
// täglichen opencode geteilt, sonst nichts (PLAN.md §3).
func isolatedEnv(bauDir string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "XDG_CONFIG_HOME="+filepath.Join(bauDir, ".opencode-home"))
}

// terminate schickt SIGTERM an die Prozessgruppe und eskaliert nach
// timeout zu SIGKILL. Wartet den Prozess in jedem Fall ab (keine Zombies).
func terminate(p *proc, timeout time.Duration) error {
	pgid := -p.cmd.Process.Pid // negative pid = Prozessgruppe
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	select {
	case <-p.done:
		return nil
	case <-time.After(timeout):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-p.done
		return errors.New("supervisor: SIGTERM ignoriert, SIGKILL geschickt")
	}
}
