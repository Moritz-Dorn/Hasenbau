// Package frontmatter trennt Markdown-Dateien mit YAML-Frontmatter —
// das gemeinsame Format von Aufträgen (§6) und Hasen-Templates.
package frontmatter

import (
	"fmt"
	"strings"
)

// Split erwartet "---" als erste Zeile und liefert den YAML-Block
// sowie den restlichen Body.
func Split(src []byte) (kopf []byte, body string, err error) {
	const marke = "---"
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	zeilen := strings.SplitAfter(text, "\n")
	if len(zeilen) == 0 || strings.TrimRight(zeilen[0], "\n") != marke {
		return nil, "", fmt.Errorf("no frontmatter: file must start with %q", marke)
	}
	for i := 1; i < len(zeilen); i++ {
		if strings.TrimRight(zeilen[i], "\n") == marke {
			return []byte(strings.Join(zeilen[1:i], "")), strings.Join(zeilen[i+1:], ""), nil
		}
	}
	return nil, "", fmt.Errorf("frontmatter not closed: missing %q", marke)
}
