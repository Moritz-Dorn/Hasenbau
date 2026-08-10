// Package provider hält die Modell-Liste eines custom Providers in der
// Bau-Config aktuell (PLAN.md §3, Hasenbau-op3.1).
//
// Quelle ist der Provider selbst — sein OpenAI-kompatibler
// /models-Endpoint —, nicht die Alltags-Config des Nutzers: die ist
// ebenfalls nur ein handgepflegter Schnappschuss und driftet genauso.
// Der Schlüssel kommt aus der geteilten auth.json und landet nie in der
// Bau-Config.
//
// Arbeitsteilung: Das Gerüst (npm, name, options.baseURL) ist
// handgepflegt und Voraussetzung — ohne baseURL kein Endpoint. Gefüllt
// wird nur models:, ergänzt nur enabled_providers.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
)

// Modell ist ein Eintrag der Provider-Modell-Liste. Connection und
// Notiz sind Zusatz des Endpoints — sie helfen beim Lesen des Diffs
// (was kostet Budget?), landen aber nicht in der Config: geschrieben
// wird nur, was opencode versteht.
type Model struct {
	ID         string
	Name       string
	Connection string // connection_type, z.B. "local"/"external"; "" wenn nicht gemeldet
	Note       string // erste Zeile aus info.meta.description
}

// Fetch fragt <baseURL>/models mit Bearer-Auth ab. Akzeptiert sowohl das
// OpenAI-Format {"data":[…]} als auch eine nackte Liste.
func Fetch(ctx context.Context, baseURL, key string) ([]Model, error) {
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("provider: Anfrage an %s: %w", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider: %s nicht erreichbar: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("provider: Antwort von %s lesen: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider: %s antwortet %s: %s", url, resp.Status, short(string(body)))
	}

	roh, err := unwrapList(body)
	if err != nil {
		return nil, fmt.Errorf("provider: Antwort von %s: %w", url, err)
	}
	modelle := make([]Model, 0, len(roh))
	for _, e := range roh {
		if m, ok := fromEntry(e); ok {
			modelle = append(modelle, m)
		}
	}
	if len(modelle) == 0 {
		return nil, fmt.Errorf("provider: %s liefert keine Modelle mit id", url)
	}
	sort.Slice(modelle, func(i, j int) bool { return modelle[i].ID < modelle[j].ID })
	return modelle, nil
}

func unwrapList(body []byte) ([]map[string]any, error) {
	var umschlag struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &umschlag); err == nil && umschlag.Data != nil {
		return umschlag.Data, nil
	}
	var liste []map[string]any
	if err := json.Unmarshal(body, &liste); err != nil {
		return nil, fmt.Errorf("weder {\"data\":[…]} noch Liste: %s", short(string(body)))
	}
	return liste, nil
}

func fromEntry(e map[string]any) (Model, bool) {
	id, _ := e["id"].(string)
	if id == "" {
		return Model{}, false
	}
	m := Model{ID: id, Name: id}
	if s, _ := e["name"].(string); s != "" {
		m.Name = s
	}
	m.Connection, _ = e["connection_type"].(string)
	if info, ok := e["info"].(map[string]any); ok {
		if meta, ok := info["meta"].(map[string]any); ok {
			if d, _ := meta["description"].(string); d != "" {
				m.Note, _, _ = strings.Cut(d, "\n")
			}
		}
	}
	return m, true
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// Key liest den API-Key eines Providers aus der auth.json, die
// sich Bau und Alltags-opencode über XDG_DATA_HOME teilen (§3).
func Key(id string) (string, error) {
	pfad := AuthPath()
	b, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("provider: keine auth.json unter %s — erst im Alltags-opencode anmelden (`opencode auth login`)", pfad)
	}
	if err != nil {
		return "", fmt.Errorf("provider: %s lesen: %w", pfad, err)
	}
	var auth map[string]struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(b, &auth); err != nil {
		return "", fmt.Errorf("provider: %s parsen: %w", pfad, err)
	}
	eintrag, ok := auth[id]
	if !ok {
		return "", fmt.Errorf("provider: %s hat keinen Eintrag für %q — erst im Alltags-opencode anmelden (`opencode auth login`)", pfad, id)
	}
	if eintrag.Key == "" {
		return "", fmt.Errorf("provider: Eintrag für %q in %s ist type=%q ohne key — fetch kann nur API-Keys nutzen", id, pfad, eintrag.Type)
	}
	return eintrag.Key, nil
}

// KeyIDs meldet, für welche Provider die geteilte auth.json
// einen benutzbaren API-Key hat — ohne den Key selbst herauszugeben.
// Fehlt die Datei, kommt eine leere Menge und kein Error: das ist ein
// Zustand, kein Unfall.
func KeyIDs() (map[string]bool, error) {
	b, err := os.ReadFile(AuthPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provider: %s lesen: %w", AuthPath(), err)
	}
	var auth map[string]struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(b, &auth); err != nil {
		return nil, fmt.Errorf("provider: %s parsen: %w", AuthPath(), err)
	}
	da := map[string]bool{}
	for id, e := range auth {
		da[id] = e.Key != ""
	}
	return da, nil
}

// AuthPath ist der Ort der geteilten auth.json — dieselbe XDG-Kaskade,
// der auch der gespawnte Server folgt (§3: XDG_DATA_HOME bleibt geerbt).
func AuthPath() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "opencode", "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "opencode", "auth.json")
	}
	return filepath.Join(home, ".local", "share", "opencode", "auth.json")
}

// Config ist die opencode.json des Baus. Sie wird roh gehalten
// (map[string]any, Zahlen als json.Number), damit ein Fetch nur
// models: und enabled_providers anfasst und alles andere unverändert
// zurückschreibt.
type Config struct {
	Pfad string
	roh  map[string]any
}

// LoadConfig liest die Bau-eigene opencode.json (§4).
func LoadConfig(root string) (*Config, error) {
	pfad := filepath.Join(root, bau.OpencodeConfig)
	b, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("provider: keine Bau-Config unter %s — `hasenbau init` läuft lassen", pfad)
	}
	if err != nil {
		return nil, fmt.Errorf("provider: %s lesen: %w", pfad, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // Zahlen unverfälscht durchreichen
	roh := map[string]any{}
	if err := dec.Decode(&roh); err != nil {
		return nil, fmt.Errorf("provider: %s parsen: %w", pfad, err)
	}
	return &Config{Pfad: pfad, roh: roh}, nil
}

// BaseURL liest options.baseURL des Providers — den Endpoint, den der
// Fetch fragt. Fehlt er, ist der Provider entweder eingebaut (die zieht
// opencode aus models.dev, da ist nichts zu holen) oder das Gerüst ist
// unvollständig.
func (c *Config) BaseURL(id string) (string, error) {
	p, ok := c.provider()[id].(map[string]any)
	if !ok {
		return "", fmt.Errorf("provider: %s kennt keinen Provider %q — das Gerüst (npm, name, options.baseURL) gehört handgepflegt in den provider:-Block", c.Pfad, id)
	}
	opts, _ := p["options"].(map[string]any)
	url, _ := opts["baseURL"].(string)
	if url == "" {
		return "", fmt.Errorf("provider: %q hat keine options.baseURL in %s — ohne Endpoint kein Fetch (PLAN.md §3)", id, c.Pfad)
	}
	return url, nil
}

// Eintrag ist ein Provider, wie ihn die Bau-Config kennt.
type Entry struct {
	ID      string
	BaseURL string // leer: eingebaut (Modelle kommen aus models.dev) oder Gerüst unvollständig
	Modelle int
	Aktiv   bool // steht in enabled_providers — ohne das ignoriert der Server die Definition
}

// List zählt auf, was im Bau definiert ist. Enthalten sind auch IDs,
// die nur in enabled_providers stehen: ein Tippfehler dort ist sonst
// unsichtbar, obwohl er den Provider still abschaltet.
func (c *Config) List() []Entry {
	aktiv := map[string]bool{}
	liste, _ := c.roh["enabled_providers"].([]any)
	for _, e := range liste {
		if s, _ := e.(string); s != "" {
			aktiv[s] = true
		}
	}

	eintraege := map[string]*Entry{}
	for id, roh := range c.provider() {
		e := &Entry{ID: id, Aktiv: aktiv[id]}
		if p, ok := roh.(map[string]any); ok {
			if opts, ok := p["options"].(map[string]any); ok {
				e.BaseURL, _ = opts["baseURL"].(string)
			}
			if m, ok := p["models"].(map[string]any); ok {
				e.Modelle = len(m)
			}
		}
		eintraege[id] = e
	}
	for id := range aktiv {
		if _, da := eintraege[id]; !da {
			eintraege[id] = &Entry{ID: id, Aktiv: true}
		}
	}

	out := make([]Entry, 0, len(eintraege))
	for _, e := range eintraege {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c *Config) provider() map[string]any {
	p, ok := c.roh["provider"].(map[string]any)
	if !ok {
		p = map[string]any{}
		c.roh["provider"] = p
	}
	return p
}

// Change ist das Ergebnis eines Merge: was der Endpoint gegenüber
// der Config neu hat, was er nicht mehr kennt, was umbenannt wurde.
type Change struct {
	Neu             []Model
	Weg             []string
	Umbenannt       []Rename
	Unveraendert    int
	EnabledErgaenzt bool
}

// Umbenennung ist ein Modell, dessen Anzeigename sich geändert hat.
type Rename struct {
	ID  string
	Alt string
	Neu string
}

// Empty meldet, ob der Fetch nichts zu schreiben hätte.
func (a Change) Empty() bool {
	return len(a.Neu) == 0 && len(a.Weg) == 0 && len(a.Umbenannt) == 0 && !a.EnabledErgaenzt
}

// Merge zieht die Modell-Liste in die Config und meldet den Unterschied.
// Bestehende Modell-Einträge behalten ihre Zusatzfelder (z.B. limit,
// options) — überschrieben wird nur name. Modelle, die der Endpoint
// nicht mehr kennt, fallen raus: sie sind ein 500 mit Anlauf.
func (c *Config) Merge(id string, modelle []Model) Change {
	prov, ok := c.provider()[id].(map[string]any)
	if !ok {
		prov = map[string]any{}
		c.provider()[id] = prov
	}
	alt, _ := prov["models"].(map[string]any)
	neu := map[string]any{}
	var ae Change

	for _, m := range modelle {
		eintrag, bestand := alt[m.ID].(map[string]any)
		if !bestand {
			neu[m.ID] = map[string]any{"name": m.Name}
			ae.Neu = append(ae.Neu, m)
			continue
		}
		altName, _ := eintrag["name"].(string)
		switch {
		case altName == m.Name:
			ae.Unveraendert++
		default:
			ae.Umbenannt = append(ae.Umbenannt, Rename{ID: m.ID, Alt: altName, Neu: m.Name})
		}
		eintrag["name"] = m.Name
		neu[m.ID] = eintrag
	}
	for k := range alt {
		if _, noch := neu[k]; !noch {
			ae.Weg = append(ae.Weg, k)
		}
	}
	sort.Strings(ae.Weg)
	prov["models"] = neu

	ae.EnabledErgaenzt = c.ensureEnabled(id)
	return ae
}

// ensureEnabled ergänzt die Provider-ID in enabled_providers —
// ohne sie ignoriert der Server die Definition.
func (c *Config) ensureEnabled(id string) bool {
	liste, _ := c.roh["enabled_providers"].([]any)
	for _, e := range liste {
		if s, _ := e.(string); s == id {
			return false
		}
	}
	c.roh["enabled_providers"] = append(liste, id)
	return true
}

// Write legt die Config zurück auf die Platte. Die Schlüssel stehen
// danach sortiert — deterministisch, damit der Bau als Git-Repo saubere
// Diffs bekommt.
func (c *Config) Write() error {
	b, err := json.MarshalIndent(c.roh, "", "  ")
	if err != nil {
		return fmt.Errorf("provider: %s serialisieren: %w", c.Pfad, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(c.Pfad, b, 0o644); err != nil {
		return fmt.Errorf("provider: %s schreiben: %w", c.Pfad, err)
	}
	return nil
}

// Report formatiert die Änderung als lesbaren Diff.
func (a Change) Report() string {
	var b strings.Builder
	for _, m := range a.Neu {
		fmt.Fprintf(&b, "  + %s  %s\n", m.ID, extra(m))
	}
	for _, u := range a.Umbenannt {
		fmt.Fprintf(&b, "  ~ %s  %q → %q\n", u.ID, u.Alt, u.Neu)
	}
	for _, id := range a.Weg {
		fmt.Fprintf(&b, "  - %s  (Endpoint kennt es nicht mehr)\n", id)
	}
	if a.EnabledErgaenzt {
		fmt.Fprintln(&b, "  + enabled_providers")
	}
	fmt.Fprintf(&b, "\n%d neu, %d umbenannt, %d entfernt, %d unverändert\n",
		len(a.Neu), len(a.Umbenannt), len(a.Weg), a.Unveraendert)
	return b.String()
}

// zusatz zeigt, was der Endpoint über das Modell verrät — Orientierung
// beim Lesen, nicht Teil der Config. Gefiltert wird bewusst nicht:
// welches Modell ein Hase bekommt, entscheidet der Auftrag.
func extra(m Model) string {
	teile := []string{fmt.Sprintf("%q", m.Name)}
	if m.Connection != "" {
		teile = append(teile, m.Connection)
	}
	if m.Note != "" {
		teile = append(teile, m.Note)
	}
	return short(strings.Join(teile, " · "))
}
