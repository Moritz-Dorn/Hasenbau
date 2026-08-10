// get.go: das lesende Verb der CLI (Hasenbau-ha0). `hasenbau get
// <ressource>` zeigt, was der Bau kennt — angelehnt an kubectl, damit
// die Befehle einem Schema folgen statt einzeln zu wachsen. Die Verben
// zum Auslösen (`lauf`, `baumeister`, `daemon`) bleiben davon unberührt.
package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
)

const getUsage = `Aufruf: hasenbau get <ressource>

Ressourcen:
  provider   Provider der Bau-Config: Endpoint, Modelle, Schlüssel
`

func cmdGet(root string, args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, getUsage)
		return 2
	}
	switch args[0] {
	case "provider", "providers":
		return getProvider(root, args[1:], out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau get: unbekannte Ressource %q\n\n%s", args[0], getUsage)
		return 2
	}
}

// getProvider beantwortet die Frage, die `provider fetch <id>` stellt,
// aber nirgends beantwortet: welche IDs kennt dieser Bau, und welche
// davon lassen sich überhaupt holen?
func getProvider(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get provider")
		return 2
	}
	conf, err := provider.LadeConfig(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	eintraege := conf.Liste()
	if len(eintraege) == 0 {
		fmt.Fprintf(out, "keine Provider in %s\n", conf.Pfad)
		fmt.Fprintln(out, "Das Gerüst (npm, options.baseURL) gehört handgepflegt in den provider:-Block (PLAN.md §3).")
		return 0
	}

	// Der Schlüssel selbst bleibt, wo er ist — hier zählt nur, ob es
	// einen gibt.
	schluessel, err := provider.SchluesselIDs()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tENDPOINT\tMODELLE\tAKTIV\tSCHLÜSSEL")
	var ohneEndpoint, ohneSchluessel bool
	for _, e := range eintraege {
		endpoint := e.BaseURL
		if endpoint == "" {
			endpoint = "—"
			ohneEndpoint = true
		}
		if !schluessel[e.ID] {
			ohneSchluessel = true
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			e.ID, endpoint, e.Modelle, jaNein(e.Aktiv), jaNein(schluessel[e.ID]))
	}
	w.Flush()

	fmt.Fprintf(out, "\n%s\n", conf.Pfad)
	fmt.Fprintln(out, "Holen lässt sich, was Endpoint und Schlüssel hat: hasenbau provider fetch <id>")
	if ohneEndpoint {
		fmt.Fprintln(out, "Ohne Endpoint: eingebauter Provider (seine Modelle kennt opencode aus models.dev)")
		fmt.Fprintln(out, "oder das Gerüst im provider:-Block ist unvollständig.")
	}
	if ohneSchluessel {
		fmt.Fprintf(out, "Ohne Schlüssel: in %s anmelden (`opencode auth login`) — die Datei teilen\n", provider.AuthPfad())
		fmt.Fprintln(out, "sich Bau und Alltags-opencode (PLAN.md §3).")
	}
	return 0
}

func jaNein(b bool) string {
	if b {
		return "ja"
	}
	return "nein"
}
