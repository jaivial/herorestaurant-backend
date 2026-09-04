package api

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Tests for the WS hello frame assembled by buildBOGroupMenuV2WSHelloPayload.
//
// Regression: prior to the fix, the backoffice group-menus-v2 WS handler
// dropped the entire hello (including `beverage_options`) when
// loadBOMenuV2AIImageTracker returned an error. As a result the live
// preview iframe never received the operator's custom beverages on
// initial load and fell back to the hardcoded 4-default parenthetical.
//
// The fix makes buildBOGroupMenuV2WSHelloPayload a pure helper so it can
// be unit-tested without a DB / WebSocket.

func TestBuildBOGroupMenuV2WSHelloPayload_AllLoadersOK(t *testing.T) {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	tracker := boV2AIImagesTracker{TotalRequested: 2, TotalGenerating: 1}
	preview := boV2MenuPreviewTracker{ShowMenuPreviewImage: true, MenuPreviewImageURL: "https://example/x.png"}
	beverage := []beverageOption{
		{ID: 1, Slug: "agua", Name: "Agua", Custom: false, Selected: true},
		{ID: 64, Slug: "bebida-inventada", Name: "Bebida inventada", Custom: true, Selected: true},
	}
	slider := map[string]any{"slider_ai_generating": 0}

	payload := buildBOGroupMenuV2WSHelloPayload(
		7, 42, at,
		tracker, nil,
		preview, nil,
		beverage, nil,
		slider, nil,
	)

	if got := payload["type"]; got != "hello" {
		t.Errorf("type = %v, want hello", got)
	}
	if got := payload["restaurant_id"]; got != 7 {
		t.Errorf("restaurant_id = %v, want 7", got)
	}
	if got := payload["menu_id"]; got != int64(42) {
		t.Errorf("menu_id = %v, want 42", got)
	}
	if got, ok := payload["at"].(string); !ok || !strings.HasPrefix(got, "2026-09-04T12:00:00Z") {
		t.Errorf("at = %v, want RFC3339 string", payload["at"])
	}
	if got, ok := payload["tracker"].(boV2AIImagesTracker); !ok || got.TotalRequested != 2 {
		t.Errorf("tracker = %v, want TotalRequested=2", payload["tracker"])
	}
	if got, ok := payload["menu_preview"].(boV2MenuPreviewTracker); !ok || !got.ShowMenuPreviewImage {
		t.Errorf("menu_preview = %v, want ShowMenuPreviewImage=true", payload["menu_preview"])
	}
	bev, ok := payload["beverage_options"].([]beverageOption)
	if !ok {
		t.Fatalf("beverage_options type = %T, want []beverageOption", payload["beverage_options"])
	}
	if len(bev) != 2 || bev[1].Slug != "bebida-inventada" {
		t.Errorf("beverage_options = %+v, want 2 entries incl. custom", bev)
	}
	if got, ok := payload["menu_slider"].(map[string]any); !ok || got["slider_ai_generating"] != 0 {
		t.Errorf("menu_slider = %v, want slider_ai_generating=0", payload["menu_slider"])
	}
}

func TestBuildBOGroupMenuV2WSHelloPayload_TrackerErrorDoesNotDropHello(t *testing.T) {
	// Regression for the user-reported bug: when the AI tracker load
	// failed, the entire hello was silently dropped, so the backoffice
	// editor never received the live beverage_options list on initial
	// load. The hello MUST always be sent.
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	beverage := []beverageOption{
		{ID: 1, Slug: "agua", Name: "Agua", Selected: true},
		{ID: 64, Slug: "bebida-inventada", Name: "Bebida inventada", Custom: true, Selected: true},
	}

	payload := buildBOGroupMenuV2WSHelloPayload(
		7, 42, at,
		boV2AIImagesTracker{}, errors.New("tracker db unreachable"),
		boV2MenuPreviewTracker{}, errors.New("preview db unreachable"),
		beverage, nil,
		map[string]any{}, nil,
	)

	if payload["type"] != "hello" {
		t.Fatalf("hello dropped on tracker error: payload = %+v", payload)
	}
	// tracker must be present (zero value) so consumers that read it
	// directly do not crash on missing key.
	if _, ok := payload["tracker"]; !ok {
		t.Errorf("tracker field missing on hello")
	}
	// beverage_options MUST still be present even when tracker load failed
	// — this is the user-reported bug.
	bev, ok := payload["beverage_options"].([]beverageOption)
	if !ok || len(bev) != 2 {
		t.Errorf("beverage_options dropped on tracker error: %+v", payload["beverage_options"])
	}
}

func TestBuildBOGroupMenuV2WSHelloPayload_BeverageErrorDropsOnlyBeverage(t *testing.T) {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	tracker := boV2AIImagesTracker{TotalRequested: 1}

	payload := buildBOGroupMenuV2WSHelloPayload(
		7, 42, at,
		tracker, nil,
		boV2MenuPreviewTracker{}, nil,
		nil, errors.New("beverage db unreachable"),
		map[string]any{}, nil,
	)

	if payload["type"] != "hello" {
		t.Fatalf("hello dropped on beverage error: %+v", payload)
	}
	if _, ok := payload["beverage_options"]; ok {
		t.Errorf("beverage_options should be absent when loader errored: %+v", payload)
	}
	if _, ok := payload["tracker"]; !ok {
		t.Errorf("tracker should still be present")
	}
}

func TestBuildBOGroupMenuV2WSHelloPayload_AllErrorsStillSendHello(t *testing.T) {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	payload := buildBOGroupMenuV2WSHelloPayload(
		7, 42, at,
		boV2AIImagesTracker{}, errors.New("tracker"),
		boV2MenuPreviewTracker{}, errors.New("preview"),
		nil, errors.New("beverage"),
		nil, errors.New("slider"),
	)

	if payload["type"] != "hello" {
		t.Fatalf("hello dropped when all loaders failed: %+v", payload)
	}
	if payload["restaurant_id"] != 7 {
		t.Errorf("restaurant_id = %v, want 7", payload["restaurant_id"])
	}
	if _, ok := payload["tracker"]; !ok {
		t.Errorf("tracker zero value missing on all-loaders-error")
	}
	if _, ok := payload["beverage_options"]; ok {
		t.Errorf("beverage_options should be absent when loader errored")
	}
	if _, ok := payload["menu_preview"]; ok {
		t.Errorf("menu_preview should be absent when loader errored")
	}
	if _, ok := payload["menu_slider"]; ok {
		t.Errorf("menu_slider should be absent when loader errored")
	}
}

func TestBuildBOGroupMenuV2WSHelloPayload_TableDrivenHelloIsAlwaysSent(t *testing.T) {
	// Cross-check: for any combination of loader outcomes, the hello
	// frame must be returned with type=hello and the required fields.
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	tracker := boV2AIImagesTracker{TotalRequested: 1}
	preview := boV2MenuPreviewTracker{ShowMenuPreviewImage: true}
	beverage := []beverageOption{{ID: 1, Slug: "agua", Name: "Agua", Selected: true}}
	slider := map[string]any{"slider_ai_generating": 0}

	tests := []struct {
		name         string
		trackerErr   error
		previewErr   error
		beverageErr  error
		sliderErr    error
		wantBeverage bool
		wantSlider   bool
		wantPreview  bool
	}{
		{"all OK", nil, nil, nil, nil, true, true, true},
		{"tracker fails", errors.New("x"), nil, nil, nil, true, true, true},
		{"preview fails", nil, errors.New("x"), nil, nil, true, true, false},
		{"beverage fails", nil, nil, errors.New("x"), nil, false, true, true},
		{"slider fails", nil, nil, nil, errors.New("x"), true, false, true},
		{"all fail", errors.New("x"), errors.New("x"), errors.New("x"), errors.New("x"), false, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := buildBOGroupMenuV2WSHelloPayload(
				1, 1, at,
				tracker, tc.trackerErr,
				preview, tc.previewErr,
				beverage, tc.beverageErr,
				slider, tc.sliderErr,
			)
			if payload["type"] != "hello" {
				t.Fatalf("hello dropped: %+v", payload)
			}
			if _, ok := payload["tracker"]; !ok {
				t.Errorf("tracker must always be present (zero on error)")
			}
			_, hasBeverage := payload["beverage_options"]
			if hasBeverage != tc.wantBeverage {
				t.Errorf("beverage_options present=%v, want %v", hasBeverage, tc.wantBeverage)
			}
			_, hasPreview := payload["menu_preview"]
			if hasPreview != tc.wantPreview {
				t.Errorf("menu_preview present=%v, want %v", hasPreview, tc.wantPreview)
			}
			_, hasSlider := payload["menu_slider"]
			if hasSlider != tc.wantSlider {
				t.Errorf("menu_slider present=%v, want %v", hasSlider, tc.wantSlider)
			}
		})
	}
}
