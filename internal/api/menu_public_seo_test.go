package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeMenuSEOSlug(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		valid bool
	}{
		{" menu-arrocero ", "menu-arrocero", true},
		{"Menu-Arrocero-2026", "menu-arrocero-2026", true},
		{"", "", true},
		{"menu arrocero", "", false},
		{"menú-arrocero", "", false},
		{"-menu-arrocero", "", false},
		{"menu--arrocero", "", false},
		{strings.Repeat("a", 191), "", false},
	} {
		got, err := normalizeMenuSEOSlug(test.input)
		if (err == nil) != test.valid || got != test.want {
			t.Errorf("normalizeMenuSEOSlug(%q) = %q, %v; want %q, valid=%v", test.input, got, err, test.want, test.valid)
		}
	}
}

func TestNormalizeMenuSEOTextTrimsAndCountsUnicodeRunes(t *testing.T) {
	got, err := normalizeMenuSEOText("  Menú arrocero  ", 180, "H1")
	if err != nil || got != "Menú arrocero" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := normalizeMenuSEOText(strings.Repeat("á", 181), 180, "H1"); err == nil {
		t.Fatal("expected Unicode-aware length validation error")
	}
	if !utf8.ValidString(got) {
		t.Fatal("normalization returned invalid UTF-8")
	}
}

func TestNullIfEmptyMenuSEOSlugAllowsMultipleMenusWithoutCustomURL(t *testing.T) {
	if got := nullIfEmptyMenuSEOSlug(" "); got != nil {
		t.Fatalf("blank slug must be SQL NULL, got %#v", got)
	}
	if got := nullIfEmptyMenuSEOSlug("menu-arrocero"); got != "menu-arrocero" {
		t.Fatalf("nonblank slug changed: %#v", got)
	}
}

func TestSEOMenuTargetRedirectsLegacyIDAndPreservesEnglish(t *testing.T) {
	manifest := &seoRoutesManifest{MenuPaths: map[string]string{"42": "/menu/menu-arrocero"}}
	for _, test := range []struct {
		path, want string
		found      bool
	}{
		{"/menu/42", "/menu/menu-arrocero", true},
		{"/menu/42/menu-arrocero-42", "/menu/menu-arrocero", true},
		{"/en/menu/42/cualquier-slug-antiguo", "/en/menu/menu-arrocero", true},
		{"/menu/menu-arrocero", "", false},
		{"/menu/unknown", "", false},
		{"/menusdegrupos", "", false},
	} {
		got, found := seoMenuTarget(manifest, test.path)
		if got != test.want || found != test.found {
			t.Errorf("seoMenuTarget(%q) = %q, %v; want %q, %v", test.path, got, found, test.want, test.found)
		}
	}
}
