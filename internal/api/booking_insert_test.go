package api

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseChildrenFromFormPrefersAdults(t *testing.T) {
	form := url.Values{
		"adults":   {"3"},
		"children": {"99"},
	}
	req := httptest.NewRequest("POST", "/api/bookings/front", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}

	children, err := parseChildrenFromForm(req, 5)
	if err != nil {
		t.Fatalf("parseChildrenFromForm() error = %v", err)
	}
	if children != 2 {
		t.Fatalf("parseChildrenFromForm() = %d, want 2", children)
	}
}

func TestParseArrozFromFormSupportsSingleBookingPayload(t *testing.T) {
	form := url.Values{
		"arroz_type":     {"Arroz del senyoret"},
		"arroz_servings": {"4"},
	}
	req := httptest.NewRequest("POST", "/api/bookings/front", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}

	typesJSON, servingsJSON, err := parseArrozFromForm(req, 4)
	if err != nil {
		t.Fatalf("parseArrozFromForm() error = %v", err)
	}
	if got := anyToString(typesJSON); got != "[\"Arroz del senyoret\"]" {
		t.Fatalf("typesJSON = %q, want %q", got, "[\"Arroz del senyoret\"]")
	}
	if got := anyToString(servingsJSON); got != "[4]" {
		t.Fatalf("servingsJSON = %q, want %q", got, "[4]")
	}
}

func TestBuildPrincipalesSummaryAndJSONFiltersAndSummarizes(t *testing.T) {
	menuPrincipalesRaw := `{"items":["Lubina","Solomillo"]}`
	rowsRaw := `[{"name":"Lubina","servings":2},{"name":"Lubina","servings":1},{"name":"Fuera de carta","servings":3},{"name":"Solomillo","servings":1}]`

	summary, storedJSON, err := buildPrincipalesSummaryAndJSON(menuPrincipalesRaw, rowsRaw, 4)
	if err != nil {
		t.Fatalf("buildPrincipalesSummaryAndJSON() error = %v", err)
	}
	if summary != "Lubina x 2, Solomillo x 1" {
		t.Fatalf("summary = %q, want %q", summary, "Lubina x 2, Solomillo x 1")
	}
	if storedJSON != `[{"name":"Lubina","servings":2},{"name":"Solomillo","servings":1}]` {
		t.Fatalf("storedJSON = %q, want filtered JSON", storedJSON)
	}
}
