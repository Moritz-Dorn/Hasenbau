package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// tempBau legt einen minimalen Bau mit isolierter opencode-Config in
// t.TempDir() an — bewusst außerhalb des Repos (PLAN.md §3).
func tempBau(t *testing.T) string {
	t.Helper()
	bau := t.TempDir()
	confDir := filepath.Join(bau, ".opencode-home", "opencode")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := `{"$schema":"https://opencode.ai/config.json","plugin":[],"instructions":["MARKER-ISOLATION-OK"]}`
	if err := os.WriteFile(filepath.Join(confDir, "opencode.json"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return bau
}

func requireOpencode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode nicht im PATH — Integrationstest übersprungen")
	}
}

func TestNewValidiertBau(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New ohne BauDir muss fehlschlagen")
	}
	if _, err := New(Config{BauDir: filepath.Join(t.TempDir(), "gibtsnicht")}); err == nil {
		t.Error("New mit fehlendem Verzeichnis muss fehlschlagen")
	}
	// Verzeichnis ohne isolierte Config: Start würde die Alltags-Config
	// des Nutzers laden — muss abgelehnt werden.
	if _, err := New(Config{BauDir: t.TempDir()}); err == nil {
		t.Error("New ohne .opencode-home/opencode/opencode.json muss fehlschlagen")
	}
}

func TestStartHealthIsolationStop(t *testing.T) {
	requireOpencode(t)
	bau := tempBau(t)
	s, err := New(Config{BauDir: bau, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	base := s.BaseURL()
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("unerwartete BaseURL %q", base)
	}
	pid := s.proc.cmd.Process.Pid

	// Isolation: /config muss den Marker aus dem Bau zeigen, nicht die
	// Alltags-Config des Nutzers.
	resp, err := http.Get(base + "/config")
	if err != nil {
		t.Fatal(err)
	}
	var conf struct {
		Plugin       []string `json:"plugin"`
		Instructions []string `json:"instructions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&conf); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(conf.Plugin) != 0 {
		t.Errorf("plugin nicht leer: %v — Isolation verletzt", conf.Plugin)
	}
	if len(conf.Instructions) != 1 || conf.Instructions[0] != "MARKER-ISOLATION-OK" {
		t.Errorf("Marker fehlt in instructions: %v — Isolation verletzt", conf.Instructions)
	}

	// CWD-Invariante: der Serverprozess läuft im Bau.
	if cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd"); err == nil {
		resolvedBau, _ := filepath.EvalSymlinks(bau)
		if cwd != bau && cwd != resolvedBau {
			t.Errorf("Server-CWD %q liegt nicht im Bau %q", cwd, bau)
		}
	}

	if err := s.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if s.BaseURL() != "" {
		t.Error("BaseURL nach Stop nicht geleert")
	}
	// Kein Zombie, Prozess wirklich weg: Signal 0 an die pid.
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("Prozess %d lebt nach Stop noch", pid)
	}
}

func TestRunRestartetNachCrash(t *testing.T) {
	requireOpencode(t)
	bau := tempBau(t)
	// Kurzer Health-Timeout, damit der Test nicht ewig läuft.
	s, err := New(Config{BauDir: bau, HealthTimeout: 30 * time.Second, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	waitURL := func() string {
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			if u := s.BaseURL(); u != "" {
				return u
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("Server wurde nicht (wieder) bereit")
		return ""
	}
	waitURL()

	// Crash simulieren: Prozessgruppe hart killen.
	s.mu.Lock()
	pid := s.proc.cmd.Process.Pid
	s.mu.Unlock()
	_ = syscall.Kill(-pid, syscall.SIGKILL)

	// Run muss neu starten (Backoff 1s) — warten bis wieder bereit.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		var newPid int
		if s.proc != nil {
			newPid = s.proc.cmd.Process.Pid
		}
		s.mu.Unlock()
		if newPid != 0 && newPid != pid && s.BaseURL() != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if u := s.BaseURL(); u == "" {
		t.Fatal("kein Restart nach Crash")
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(15 * time.Second):
		t.Fatal("Run kehrte nach ctx-Cancel nicht zurück")
	}
	if u := s.BaseURL(); u != "" {
		t.Errorf("Server läuft nach Run-Ende noch auf %s", u)
	}
}
