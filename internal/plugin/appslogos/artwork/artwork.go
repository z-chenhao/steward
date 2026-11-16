// Package artwork owns the single original Apps logo source set. Managed
// packages embed these bytes in their versioned executable; the explicit native
// boundary reads the same source without keeping a duplicate Web asset tree.
package artwork

import "embed"

//go:embed *.svg
var files embed.FS

func Read(slug string) ([]byte, error) { return files.ReadFile(slug + ".svg") }
