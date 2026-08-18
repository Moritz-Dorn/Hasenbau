// funnel.go: Ein SSE-Abo auf GET /event für den ganzen Daemon. Der
// Writeback der Hasen (session.idle, session.error, Tool-Parts) wird
// hier gebündelt und pro Session an die Läufe verteilt — der
// Event-Stream ist die Wahrheitsquelle für das Lauf-Ende, nicht der
// synchrone Prompt-Call (Spike-Befund, Hasenbau-q4y).
package opencode

import (
	"context"
	"sync"
	"time"

	sdk "github.com/sst/opencode-sdk-go"
)

// SessionEvent ist die auf einen Lauf reduzierte Sicht auf den
// Event-Stream. Genau eines der Felder ist gesetzt.
type SessionEvent struct {
	Idle        bool      // session.idle — der Hase ist fertig
	Error       string    // session.error — Fehlername (+ Kurztext)
	Tool        *ToolCall // message.part.updated mit Tool-Part
	Reconnected bool      // Stream neu verbunden — Ereignisse können fehlen
}

// ToolCall ist ein strukturiert geloggter Tool-Call (Grundlage für
// Phase 2, graben — der vollständige Trace kommt aus Session.Messages).
type ToolCall struct {
	Name   string
	CallID string
	Status string // pending | running | completed | error
	Error  string
}

// Funnel hält genau eine SSE-Connection und verteilt pro Session.
type Funnel struct {
	baseURL func() string // typischerweise supervisor.BaseURL; "" = Server weg
	logf    func(format string, args ...any)

	mu   sync.Mutex
	abos map[string][]chan SessionEvent
}

// NewFunnel baut den Funnel; Start öffnet die Connection.
func NewFunnel(baseURL func() string, logf func(format string, args ...any)) *Funnel {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Funnel{baseURL: baseURL, logf: logf, abos: map[string][]chan SessionEvent{}}
}

// Subscribe liefert die Ereignisse einer Session. Die zurückgegebene
// Funktion meldet ab; danach wird der Kanal nicht mehr beschrieben.
func (f *Funnel) Subscribe(sessionID string) (<-chan SessionEvent, func()) {
	// Puffer großzügig: der Verteiler blockiert nie (sonst stünde der
	// Stream aller Läufe still) — volle Kanäle verlieren Ereignisse
	// und werden geloggt.
	ch := make(chan SessionEvent, 256)
	f.mu.Lock()
	f.abos[sessionID] = append(f.abos[sessionID], ch)
	f.mu.Unlock()

	abmelden := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		kanaele := f.abos[sessionID]
		for i, k := range kanaele {
			if k == ch {
				f.abos[sessionID] = append(kanaele[:i], kanaele[i+1:]...)
				break
			}
		}
		if len(f.abos[sessionID]) == 0 {
			delete(f.abos, sessionID)
		}
	}
	return ch, abmelden
}

// Start liest den Stream in einer eigenen Goroutine, bis ctx endet.
// Verbindungsabrisse (auch Server-Restarts durch den Supervisor) werden
// mit Backoff neu verbunden; die Abonnenten sehen dann ein Reconnected-
// Ereignis, weil zwischenzeitliche Events verloren sein können.
func (f *Funnel) Start(ctx context.Context) {
	go func() {
		backoff := time.Second
		const backoffMax = 10 * time.Second
		for ctx.Err() == nil {
			url := f.baseURL()
			if url == "" {
				// Server (noch) nicht da — Supervisor startet ihn gerade.
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				continue
			}
			client := New(url)
			stream := client.Event.ListStreaming(ctx, sdk.EventListParams{})
			verbunden := false
			for stream.Next() {
				verbunden = true
				backoff = time.Second
				f.verteile(stream.Current())
			}
			err := stream.Err()
			stream.Close()
			if ctx.Err() != nil {
				return
			}
			f.logf("funnel: Event-Stream weg (%v), neu verbinden in %s", err, backoff)
			if verbunden {
				f.meldeAbriss()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > backoffMax {
				backoff = backoffMax
			}
		}
	}()
}

// verteile reduziert ein Stream-Event auf ein SessionEvent und
// stellt es allen Abonnenten der Session zu.
func (f *Funnel) verteile(evt sdk.EventListResponse) {
	switch u := evt.AsUnion().(type) {
	case sdk.EventListResponseEventSessionIdle:
		f.sende(u.Properties.SessionID, SessionEvent{Idle: true})
	case sdk.EventListResponseEventSessionError:
		id := u.Properties.SessionID
		if id == "" {
			// Fehler ohne Session — nur loggen, der Lauf-Timeout ist
			// der Backstop.
			f.logf("funnel: session.error ohne Session-ID: %s", u.Properties.Error.JSON.RawJSON())
			return
		}
		f.sende(id, SessionEvent{Error: fehlerText(u.Properties.Error)})
	case sdk.EventListResponseEventMessagePartUpdated:
		p := u.Properties.Part
		if p.Type != sdk.PartTypeTool {
			return
		}
		tp, ok := p.AsUnion().(sdk.ToolPart)
		if !ok {
			return
		}
		f.sende(p.SessionID, SessionEvent{Tool: &ToolCall{
			Name:   tp.Tool,
			CallID: tp.CallID,
			Status: string(tp.State.Status),
			Error:  tp.State.Error,
		}})
	}
}

func (f *Funnel) sende(sessionID string, e SessionEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.abos[sessionID] {
		select {
		case ch <- e:
		default:
			f.logf("funnel: subscriber of session %s is stuck — event dropped", sessionID)
		}
	}
}

// meldeAbriss meldet allen Abonnenten, dass der Stream neu verbunden
// wurde und Ereignisse fehlen können.
func (f *Funnel) meldeAbriss() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, kanaele := range f.abos {
		for _, ch := range kanaele {
			select {
			case ch <- SessionEvent{Reconnected: true}:
			default:
				f.logf("funnel: subscriber of session %s is stuck — reconnect dropped", id)
			}
		}
	}
}

// fehlerText macht aus dem Fehler-Union eine loggbare Zeile.
func fehlerText(e sdk.EventListResponseEventSessionErrorPropertiesError) string {
	roh := e.JSON.RawJSON()
	const max = 300
	if len(roh) > max {
		roh = roh[:max] + "…"
	}
	if e.Name != "" {
		return string(e.Name) + ": " + roh
	}
	return roh
}
