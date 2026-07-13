// Package opencode kapselt den SDK-Client gegen den lokalen
// opencode-Server. Die SDK-Signaturen sind generiert und gegen v0.19.2
// verifiziert (PLAN.md §11.2) — bei Änderungen dort nachschlagen,
// nicht raten.
package opencode

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"
)

// New liefert einen SDK-Client für den Server unter baseURL —
// typischerweise supervisor.BaseURL().
func New(baseURL string) *sdk.Client {
	return sdk.NewClient(option.WithBaseURL(baseURL))
}

// DisposeInstance verwirft die Instanz-Caches des Servers — nach dem
// (Re-)Generieren von Agenten nötig, damit der nächste Request sie neu
// lädt (PLAN.md §11.6). Achtung: cancelt laufende Sessions; der
// Aufrufer serialisiert gegen aktive Läufe. SDK v0.19.2 kennt keinen
// Instance-Service; Client.Post ist der dokumentierte Weg für solche
// Endpoints ("useful for hitting undocumented endpoints").
func DisposeInstance(ctx context.Context, client *sdk.Client) error {
	var ok bool
	if err := client.Post(ctx, "instance/dispose", nil, &ok); err != nil {
		return fmt.Errorf("instance/dispose: %w", err)
	}
	if !ok {
		return fmt.Errorf("instance/dispose: Server meldet false")
	}
	return nil
}

// TextPart baut den Text-Part für einen Prompt.
func TextPart(text string) sdk.SessionPromptParamsPartUnion {
	return sdk.TextPartInputParam{
		Type: sdk.F(sdk.TextPartInputTypeText),
		Text: sdk.F(text),
	}
}

// SplitModel zerlegt "provider/model" in die beiden SDK-Felder.
// Der ModelID-Teil darf selbst Slashes enthalten.
func SplitModel(s string) (sdk.SessionPromptParamsModel, bool) {
	provider, model, ok := strings.Cut(s, "/")
	if !ok || provider == "" || model == "" {
		return sdk.SessionPromptParamsModel{}, false
	}
	return sdk.SessionPromptParamsModel{
		ProviderID: sdk.F(provider),
		ModelID:    sdk.F(model),
	}, true
}

// AnswerText sammelt den Text aller Text-Parts einer Antwort.
func AnswerText(parts []sdk.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == sdk.PartTypeText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
