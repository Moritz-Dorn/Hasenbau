// Package runner führt Läufe aus: erst die Gänge (deterministisch,
// dieses File), dann den Hasen (Phase-1-Bead q4y). PLAN.md §2, §6.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

// GangFehler beschreibt den Abbruch der Gang-Kette. Der Input liegt
// danach in quarantaene/ (wenn der Auftrag den Raum kennt) oder
// unverändert am Ursprung — niemals in archiv/ (§7).
type GangFehler struct {
	Gang       string
	Grund      string // "exit 3" oder "timeout nach 2m0s"
	LogPfad    string // Bau-relativ
	Quarantaene string // Bau-relativer Zielpfad, leer wenn Input blieb
}

func (e *GangFehler) Error() string {
	s := fmt.Sprintf("gang %s: %s (log: %s)", e.Gang, e.Grund, e.LogPfad)
	if e.Quarantaene != "" {
		s += fmt.Sprintf(" — input nach %s verschoben", e.Quarantaene)
	}
	return s
}

// FuehreGaengeAus führt die Gänge sequenziell als Subprocess aus
// (sh -c, CWD = Bau, Variablen substituiert). stdout+stderr jedes
// Gangs landen in $WORK/gang-<name>.log. Der erste Fehler bricht ab.
// defaultTimeout greift für Gänge ohne eigenes timeout; 0 = unbegrenzt.
func FuehreGaengeAus(ctx context.Context, u *lauf.Umgebung, a *auftrag.Auftrag, defaultTimeout time.Duration) ([]string, error) {
	if len(a.Gaenge) == 0 {
		return nil, nil
	}
	if u.Work == "" {
		return nil, fmt.Errorf("auftrag %s: Gänge brauchen einen Raum mit Rolle work (für $WORK und Logs)", a.Name)
	}

	var logs []string
	for _, g := range a.Gaenge {
		logRel := filepath.Join(u.Work, "gang-"+g.Name+".log")
		logs = append(logs, logRel)

		zeile, err := u.Ersetze(g.Run)
		if err != nil {
			return logs, fmt.Errorf("gang %s: %w", g.Name, err)
		}

		grund, err := fuehreAus(ctx, u.Bau, zeile, logRel, timeoutFuer(g, defaultTimeout))
		if err != nil {
			return logs, fmt.Errorf("gang %s: %w", g.Name, err)
		}
		if grund != "" {
			gf := &GangFehler{Gang: g.Name, Grund: grund, LogPfad: logRel}
			gf.Quarantaene = verschiebeInQuarantaene(u, a)
			return logs, gf
		}
	}
	return logs, nil
}

func timeoutFuer(g auftrag.Gang, defaultTimeout time.Duration) time.Duration {
	if g.Timeout != 0 {
		return g.Timeout
	}
	return defaultTimeout
}

// fuehreAus startet die Zeile via sh -c und schreibt den kombinierten
// Output nach logRel. Rückgabe: Fehlgrund ("" = Erfolg) für erwartete
// Fehlschläge (Exit≠0, Timeout); error nur für Infrastruktur-Probleme.
func fuehreAus(ctx context.Context, bau, zeile, logRel string, timeout time.Duration) (string, error) {
	logAbs := filepath.Join(bau, logRel)
	logDatei, err := os.Create(logAbs)
	if err != nil {
		return "", fmt.Errorf("log anlegen: %w", err)
	}
	defer logDatei.Close()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", zeile)
	cmd.Dir = bau
	cmd.Stdout = logDatei
	cmd.Stderr = logDatei
	// Eigene Prozessgruppe: der Timeout muss auch Kinder der Shell
	// treffen, nicht nur die Shell selbst.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	err = cmd.Run()
	switch {
	case err == nil:
		return "", nil
	case ctx.Err() != nil && timeout > 0:
		return fmt.Sprintf("timeout nach %s", timeout), nil
	case errors.As(err, new(*exec.ExitError)):
		return fmt.Sprintf("exit %d", err.(*exec.ExitError).ExitCode()), nil
	default:
		return "", fmt.Errorf("subprocess: %w", err)
	}
}

// verschiebeInQuarantaene bewegt den Trigger-Input in den
// quarantaene-Raum, falls der Auftrag einen kennt. Kollisionen werden
// mit Zeitstempel-Suffix aufgelöst. Scheitert der Move, bleibt der
// Input am Ursprung — das ist per §7 der zweite legale Zustand.
//
// Nur bei watch: dort ist $INPUT eine Datei. Bei manuell ist es ein
// freies Argument, und ein Move würde bestenfalls scheitern,
// schlimmstenfalls eine gleichnamige Datei im Bau-Root wegtragen.
func verschiebeInQuarantaene(u *lauf.Umgebung, a *auftrag.Auftrag) string {
	if u.TriggerArt != auftrag.TriggerWatch {
		return ""
	}
	raum, ok := a.Raeume["quarantaene"]
	if !ok || u.Input == "" {
		return ""
	}
	ziel := filepath.Join(raum, filepath.Base(u.Input))
	if _, err := os.Stat(filepath.Join(u.Bau, ziel)); err == nil {
		name := filepath.Base(u.Input)
		ziel = filepath.Join(raum, time.Now().UTC().Format("20060102-150405")+"-"+name)
	}
	if err := os.Rename(filepath.Join(u.Bau, u.Input), filepath.Join(u.Bau, ziel)); err != nil {
		return ""
	}
	return ziel
}
