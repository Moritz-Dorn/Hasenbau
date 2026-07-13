// nachher.go führt die Aufräum-Schritte eines Auftrags aus (§6):
// move, copy, delete — nur nach einem erfolgreichen Lauf (der Aufrufer
// entscheidet das; der move → archiv/ ist der Idempotenz-Mechanismus).
package runner

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

// FuehreNachherAus arbeitet die nachher:-Schritte sequenziell ab.
// Pfade werden substituiert und müssen danach im Bau bleiben — $BAU
// (absolut) ist hier deshalb tabu. Der erste Fehler bricht ab.
func FuehreNachherAus(u *lauf.Umgebung, a *auftrag.Auftrag) error {
	for i, n := range a.Nachher {
		if err := fuehreSchrittAus(u, n); err != nil {
			return fmt.Errorf("nachher %d (%s): %w", i+1, n.Aktion, err)
		}
	}
	return nil
}

func fuehreSchrittAus(u *lauf.Umgebung, n auftrag.Nachher) error {
	von, err := substituierterBauPfad(u, n.Von)
	if err != nil {
		return err
	}

	switch n.Aktion {
	case "delete":
		if err := os.Remove(filepath.Join(u.Bau, von)); err != nil {
			return fmt.Errorf("delete %s: %w", von, err)
		}
		return nil
	case "move", "copy":
		nach, err := substituierterBauPfad(u, n.Nach)
		if err != nil {
			return err
		}
		ziel, err := zielPfad(u.Bau, von, nach, n.Nach)
		if err != nil {
			return err
		}
		if n.Aktion == "move" {
			if err := os.Rename(filepath.Join(u.Bau, von), filepath.Join(u.Bau, ziel)); err != nil {
				return fmt.Errorf("move %s -> %s: %w", von, ziel, err)
			}
			return nil
		}
		if err := kopiere(filepath.Join(u.Bau, von), filepath.Join(u.Bau, ziel)); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", von, ziel, err)
		}
		return nil
	default:
		// Parse lässt nur move/copy/delete durch — das hier wäre ein Bug.
		return fmt.Errorf("unbekannte Aktion %q", n.Aktion)
	}
}

// substituierterBauPfad ersetzt Variablen und erzwingt danach die
// Bau-Grenze: nachher-Schritte arbeiten nur im Bau (§3).
func substituierterBauPfad(u *lauf.Umgebung, roh string) (string, error) {
	pfad, err := u.Ersetze(roh)
	if err != nil {
		return "", err
	}
	if err := auftrag.BauRelativ(pfad); err != nil {
		return "", err
	}
	return pfad, nil
}

// zielPfad löst das Ziel auf: endet die rohe Angabe auf "/" oder ist
// das Ziel ein Verzeichnis, landet die Datei darin (Basename der
// Quelle). Kollisionen bekommen einen Zeitstempel-Präfix statt zu
// überschreiben — Material verschwindet nie lautlos (§7).
func zielPfad(bau, von, nach, roh string) (string, error) {
	ziel := nach
	istDir := strings.HasSuffix(roh, "/")
	if fi, err := os.Stat(filepath.Join(bau, nach)); err == nil && fi.IsDir() {
		istDir = true
	}
	if istDir {
		ziel = filepath.Join(nach, filepath.Base(von))
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(bau, ziel)), 0o755); err != nil {
		return "", fmt.Errorf("zielverzeichnis anlegen: %w", err)
	}
	if _, err := os.Stat(filepath.Join(bau, ziel)); err == nil {
		dir, name := filepath.Split(ziel)
		ziel = filepath.Join(dir, time.Now().UTC().Format("20060102-150405")+"-"+name)
	}
	return ziel, nil
}

func kopiere(von, nach string) error {
	quelle, err := os.Open(von)
	if err != nil {
		return err
	}
	defer quelle.Close()
	fi, err := quelle.Stat()
	if err != nil {
		return err
	}
	ziel, err := os.OpenFile(nach, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(ziel, quelle); err != nil {
		ziel.Close()
		return err
	}
	return ziel.Close()
}
