package main

import (
	"fmt"
	"os"

	"preactvillacarmen/internal/api"
)

// forky-docs renders the Forky tool registry as a Markdown matrix on stdout,
// used to regenerate backend/docs/FORKY_API.md.
func main() {
	w := os.Stdout
	fmt.Fprintln(w, "| Tool | Sección | Permiso | Confirma | Schema |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, d := range api.ToolDocs() {
		perm := "read"
		if d.Write {
			perm = "write"
		}
		confirm := "no"
		if d.Confirm {
			confirm = "sí"
		}
		fmt.Fprintf(w, "| `%s` | %s | %s | %s | `%s` |\n", d.Name, d.Section, perm, confirm, d.Schema)
	}
}
