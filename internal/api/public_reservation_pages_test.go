package api

import (
	"bytes"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Confirm reservation template tests
// ---------------------------------------------------------------------------

func TestConfirmReservationTemplateRenders(t *testing.T) {
	data := map[string]any{
		"BrandName":    "Test Restaurant",
		"LogoURL":     "/logo.png",
		"Message":     "",
		"Success":     false,
		"HasBooking":  true,
		"BookingID":   456,
		"ShowConfirmation": true,
		"CustomerName": "Ana Martínez",
		"DateDisplay":  "20/05/2026",
		"TimeDisplay":  "21:00",
		"PartySize":    3,
		"Action":       "/confirm_reservation.php?id=456",
	}
	var buf bytes.Buffer
	if err := confirmReservationTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "Ana Martínez") {
		t.Error("should contain customer name")
	}
	if !strings.Contains(html, "20/05/2026") {
		t.Error("should contain formatted date")
	}
	if !strings.Contains(html, "21:00") {
		t.Error("should contain time")
	}
	if !strings.Contains(html, "#456") {
		t.Error("should contain booking ID")
	}
}

func TestConfirmReservationTemplateHasDarkMode(t *testing.T) {
	data := map[string]any{
		"BrandName": "Test",
		"LogoURL":  "/logo.png",
		"Message":  "",
	}
	var buf bytes.Buffer
	if err := confirmReservationTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "prefers-color-scheme") {
		t.Error("template should contain prefers-color-scheme media query for dark/light mode")
	}
}

func TestConfirmReservationTemplateHasConfirmForm(t *testing.T) {
	data := map[string]any{
		"BrandName":        "Test",
		"LogoURL":         "/logo.png",
		"Message":         "",
		"HasBooking":      true,
		"BookingID":       1,
		"ShowConfirmation": true,
		"CustomerName":    "Test",
		"DateDisplay":     "01/01/2026",
		"TimeDisplay":     "14:00",
		"PartySize":       2,
		"Action":          "/confirm_reservation.php?id=1",
	}
	var buf bytes.Buffer
	if err := confirmReservationTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "confirm_booking") {
		t.Error("template should contain confirm_booking form field")
	}
	if !strings.Contains(html, "<form") {
		t.Error("template should contain a form element")
	}
}

// ---------------------------------------------------------------------------
// Cancel reservation template tests
// ---------------------------------------------------------------------------

func TestCancelReservationTemplateRenders(t *testing.T) {
	data := map[string]any{
		"BrandName":    "Test Restaurant",
		"LogoURL":     "/logo.png",
		"Message":     "",
		"Success":     false,
		"IsSameDay":   false,
		"HasBooking":  true,
		"BookingID":   123,
		"ShowConfirmation": true,
		"CustomerName": "Juan García",
		"DateDisplay":  "15/04/2026",
		"TimeDisplay":  "14:00",
		"PartySize":    4,
		"Action":       "/cancel_reservation.php?id=123",
	}
	var buf bytes.Buffer
	if err := cancelReservationTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "Juan García") {
		t.Error("should contain customer name")
	}
	if !strings.Contains(html, "15/04/2026") {
		t.Error("should contain formatted date")
	}
	if !strings.Contains(html, "14:00") {
		t.Error("should contain time")
	}
}

func TestCancelReservationTemplateHasDarkMode(t *testing.T) {
	data := map[string]any{
		"BrandName": "Test",
		"LogoURL":  "/logo.png",
		"Message":  "",
	}
	var buf bytes.Buffer
	if err := cancelReservationTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "prefers-color-scheme") {
		t.Error("template should contain prefers-color-scheme media query for dark/light mode")
	}
}

func TestCancelReservationTemplateHasCancelForm(t *testing.T) {
	data := map[string]any{
		"BrandName":        "Test",
		"LogoURL":         "/logo.png",
		"Message":         "",
		"ShowConfirmation": true,
		"CustomerName":    "Test",
		"DateDisplay":     "01/01/2026",
		"TimeDisplay":     "14:00",
		"PartySize":       2,
		"Action":          "/cancel_reservation.php?id=1",
	}
	var buf bytes.Buffer
	if err := cancelReservationTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "confirm_cancel") {
		t.Error("template should contain confirm_cancel form field")
	}
	if !strings.Contains(html, "<form") {
		t.Error("template should contain a form element")
	}
}

// ---------------------------------------------------------------------------
// Book rice template tests
// ---------------------------------------------------------------------------

func TestBookRiceTemplateRenders(t *testing.T) {
	data := map[string]any{
		"BrandName":    "Test Restaurant",
		"LogoURL":     "/logo.png",
		"Message":     "",
		"Success":     false,
		"HasBooking":  true,
		"ShowForm":    true,
		"Countdown":   false,
		"IsSameDay":   false,
		"CustomerName": "María López",
		"DateDisplay":  "01/05/2026",
		"TimeDisplay":  "21:30",
		"PartySize":    6,
		"RiceOptions": []string{"Arroz a la valenciana", "Arroz negro"},
		"Action":       "/book_rice.php?id=456",
	}
	var buf bytes.Buffer
	if err := bookRiceTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "María López") {
		t.Error("should contain customer name")
	}
	if !strings.Contains(html, "Arroz a la valenciana") {
		t.Error("should contain rice option")
	}
}

func TestBookRiceTemplateHasDarkMode(t *testing.T) {
	data := map[string]any{
		"BrandName": "Test",
		"LogoURL":  "/logo.png",
		"Message":  "",
	}
	var buf bytes.Buffer
	if err := bookRiceTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "prefers-color-scheme") {
		t.Error("template should contain prefers-color-scheme media query for dark/light mode")
	}
}

func TestBookRiceTemplateHasForm(t *testing.T) {
	data := map[string]any{
		"BrandName":    "Test",
		"LogoURL":     "/logo.png",
		"Message":     "",
		"HasBooking":  true,
		"ShowForm":    true,
		"CustomerName": "Test",
		"DateDisplay": "01/01/2026",
		"TimeDisplay": "14:00",
		"PartySize":   4,
		"RiceOptions": []string{"Arroz negro"},
		"Action":      "/book_rice.php?id=1",
	}
	var buf bytes.Buffer
	if err := bookRiceTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "rice_type") {
		t.Error("template should contain rice_type select")
	}
	if !strings.Contains(html, "rice_servings") {
		t.Error("template should contain rice_servings input")
	}
}
