package supervisor

// Tests gegen einen Fake-Server (Helper-Process-Pattern): laufen ohne
// installiertes opencode und decken die Fehlerpfade ab, die sich mit dem
// echten Server nicht provozieren lassen.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const fakeModeEnv = "SUPERVISOR_FAKE_MODE"

// TestHelperProcess ist kein Test: Die Fake-Binaries re-exec'en das
// Test-Binary mit gesetztem SUPERVISOR_FAKE_MODE hierher.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(fakeModeEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "die": // stirbt vor der Listen-Zeile
		os.Exit(3)
	case "mute": // lebt, sagt aber nie, wo er lauscht
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "stubborn": // ignoriert SIGTERM
		signal.Ignore(syscall.SIGTERM)
		fallthrough
	case "ok":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(1)
		}
		fmt.Printf("opencode server listening on http://%s\n", ln.Addr())
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}
	os.Exit(0)
}

// fakeBinary erzeugt ein Wrapper-Skript, das das Test-Binary als
// Fake-Server im gewünschten Modus startet.
func fakeBinary(t *testing.T, mode string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "fake-opencode")
	content := "#!/bin/sh\n" + fakeModeEnv + "=" + mode + " exec " + self + " -test.run=TestHelperProcess\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func newFake(t *testing.T, mode string, cfg Config) *Supervisor {
	t.Helper()
	cfg.BauDir = tempBau(t)
	cfg.Binary = fakeBinary(t, mode)
	if cfg.Logf == nil {
		cfg.Logf = t.Logf
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFakeStartStopUndDoppelstart(t *testing.T) {
	s := newFake(t, "ok", Config{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	resp, err := http.Get(s.BaseURL() + "/config")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Health-Endpoint: Status %d", resp.StatusCode)
	}

	if err := s.Start(context.Background()); err == nil {
		t.Error("zweiter Start muss fehlschlagen, solange der Server läuft")
	}

	// CWD-Invariante (§3): der Serverprozess startet immer im Bau.
	s.mu.Lock()
	dir := s.proc.cmd.Dir
	s.mu.Unlock()
	if dir != s.cfg.BauDir {
		t.Errorf("Server-CWD %q ist nicht der Bau %q", dir, s.cfg.BauDir)
	}

	if err := s.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Errorf("Stop muss idempotent sein: %v", err)
	}
}

func TestFakeServerStirbtVorListen(t *testing.T) {
	s := newFake(t, "die", Config{HealthTimeout: 5 * time.Second})
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "endete vor dem Listen") {
		t.Fatalf("erwartet 'endete vor dem Listen', bekam: %v", err)
	}
	if s.BaseURL() != "" {
		t.Error("BaseURL gesetzt trotz Fehlstart")
	}
}

func TestFakeTimeoutOhneListenZeile(t *testing.T) {
	s := newFake(t, "mute", Config{
		HealthTimeout: 700 * time.Millisecond,
		StopTimeout:   3 * time.Second,
	})
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for the listen line") {
		t.Fatalf("erwartet Listen-Timeout, bekam: %v", err)
	}
}

func TestFakeStopEskaliertZuSigkill(t *testing.T) {
	s := newFake(t, "stubborn", Config{StopTimeout: time.Second})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	pid := s.proc.cmd.Process.Pid
	s.mu.Unlock()

	err := s.Stop()
	if err == nil || !strings.Contains(err.Error(), "SIGKILL") {
		t.Fatalf("erwartet SIGKILL-Eskalation, bekam: %v", err)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Errorf("Prozess %d lebt nach SIGKILL-Eskalation noch", pid)
	}
}

func TestListenRe(t *testing.T) {
	m := listenRe.FindStringSubmatch("opencode server listening on http://127.0.0.1:44695")
	if m == nil || m[1] != "http://127.0.0.1:44695" {
		t.Errorf("Listen-Zeile nicht erkannt: %v", m)
	}
	if listenRe.MatchString("Warning: OPENCODE_SERVER_PASSWORD is not set; server is unsecured.") {
		t.Error("Warnzeile darf nicht als Listen-Zeile gelten")
	}
	if listenRe.MatchString("listening on http://0.0.0.0:4096") {
		t.Error("fremde Hosts dürfen nicht matchen — wir binden ausschließlich 127.0.0.1")
	}
}

func TestIsolatedEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/woanders/.config")
	t.Setenv("XDG_DATA_HOME", "/woanders/.local/share")
	env := isolatedEnv("/pfad/zum/bau")

	var configHome []string
	dataSeen := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			configHome = append(configHome, kv)
		}
		if kv == "XDG_DATA_HOME=/woanders/.local/share" {
			dataSeen = true
		}
	}
	want := "XDG_CONFIG_HOME=" + filepath.Join("/pfad/zum/bau", ".opencode-home")
	if len(configHome) != 1 || configHome[0] != want {
		t.Errorf("XDG_CONFIG_HOME nicht sauber ersetzt: %v", configHome)
	}
	if !dataSeen {
		t.Error("XDG_DATA_HOME muss geerbt bleiben (auth.json-Sharing, §3)")
	}
}
