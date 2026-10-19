package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// legacyTablesGone lists the tables that the v2 migration retired. Nothing in
// the runtime code may read or write them again: they are dropped from the
// database once the migration is complete.
//
// Note that POSTRES (postres, desserts) is NOT on this list: it is a live
// table with its own writers, and it is not part of the v2 menu migration.
var legacyTablesGone = []string{"DIA", "FINDE", "PLATOS", "VINOS"}

// legacyQueryRe matches a SQL statement that references a table by name in a
// read or write position: FROM, JOIN, INTO, UPDATE, DELETE FROM.
var legacyQueryRe = regexp.MustCompile("(?i)\\b(FROM|JOIN|INTO|UPDATE|DELETE\\s+FROM)\\s+\"?`?([A-Za-z_][A-Za-z0-9_]*)`??")

// stripGoComments removes line comments and block comments so that a table
// name mentioned in a doc comment is not mistaken for a live query.
func stripGoComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	cleaned := out.String()
	for {
		start := strings.Index(cleaned, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(cleaned[start:], "*/")
		if end < 0 {
			cleaned = cleaned[:start]
			break
		}
		cleaned = cleaned[:start] + cleaned[start+end+2:]
	}
	return cleaned
}

// TestRuntimeCodeDoesNotTouchLegacyTables walks the non-test Go sources and
// fails if any of them builds a query against a retired table.
//
// The historical regression this guards: the writers still pointed at DIA and
// FINDE, so once the tables were dropped the API returned a SQL error instead
// of updating the v2 row. The read paths were migrated earlier; this test
// keeps the write paths from drifting back.
func TestRuntimeCodeDoesNotTouchLegacyTables(t *testing.T) {
	root := "."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "dashboard_static", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The one-off migration tool is allowed to read the legacy table: it
		// is the tool that moves the last blobs to BunnyCDN. It is not part of
		// the running API.
		if strings.HasSuffix(path, filepath.Join("cmd", "migrate-wine-images", "main.go")) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		code := stripGoComments(string(src))
		for _, m := range legacyQueryRe.FindAllStringSubmatch(code, -1) {
			table := strings.ToUpper(m[2])
			for _, legacy := range legacyTablesGone {
				if table == legacy {
					t.Errorf("%s: query references the retired table %q in a %s clause: %s",
						path, legacy, strings.ToUpper(m[1]), strings.TrimSpace(m[0]))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestToggleDishStatusWritesToV2 pins the shape of the two toggle handlers:
// they must update group_menu_section_dishes_v2 and resolve the legacy id
// through legacy_dish_map, never through a legacy table name.
func TestToggleDishStatusWritesToV2(t *testing.T) {
	src, err := os.ReadFile("admin_menus.go")
	if err != nil {
		t.Fatalf("read admin_menus.go: %v", err)
	}
	text := string(src)
	for _, fn := range []string{"handleToggleDishStatusDia", "handleToggleDishStatusGeneric"} {
		start := strings.Index(text, "func (s *Server) "+fn)
		if start < 0 {
			t.Fatalf("%s not found in admin_menus.go", fn)
		}
		body := text[start:]
		if end := strings.Index(body[10:], "\nfunc "); end > 0 {
			body = body[:end+10]
		}
		if !strings.Contains(body, "group_menu_section_dishes_v2") {
			t.Errorf("%s does not write to group_menu_section_dishes_v2", fn)
		}
		if !strings.Contains(body, "legacy_dish_map") {
			t.Errorf("%s does not resolve the legacy id through legacy_dish_map", fn)
		}
		for _, legacy := range legacyTablesGone {
			if regexp.MustCompile(`(?i)"UPDATE\s+` + legacy + `\b`).MatchString(body) {
				t.Errorf("%s still builds a query against the retired table %q", fn, legacy)
			}
		}
	}
}
