package api

import (
	"strings"
	"testing"
)

func TestNormalizeBOAdContentEnforcesLimitsAndOrder(t *testing.T) {
	input := []boAdContentElement{
		{ID: "t1", Type: "title", Value: "Primero"},
		{ID: "s1", Type: "subtitle", Value: "Segundo"},
		{ID: "x1", Type: "text", Value: "Tercero"},
		{ID: "i1", Type: "image", Value: "https://cdn.example/ad.webp"},
	}
	got, err := normalizeBOAdContent(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(input) {
		t.Fatalf("expected %d items, got %d", len(input), len(got))
	}
	for i := range input {
		if got[i].ID != input[i].ID || got[i].Type != input[i].Type || got[i].Value != input[i].Value {
			t.Fatalf("order/content changed at %d: %#v", i, got[i])
		}
	}

	tooManyTitles := make([]boAdContentElement, 0, 6)
	for i := 0; i < 6; i++ {
		tooManyTitles = append(tooManyTitles, boAdContentElement{ID: string(rune('a' + i)), Type: "title", Value: "x"})
	}
	if _, err := normalizeBOAdContent(tooManyTitles); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected title limit error, got %v", err)
	}

	twoImages := []boAdContentElement{{ID: "i1", Type: "image", Value: "a"}, {ID: "i2", Type: "image", Value: "b"}}
	if _, err := normalizeBOAdContent(twoImages); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected image limit error, got %v", err)
	}
}

func TestBOAdPromptUsesWrittenContentInDisplayOrder(t *testing.T) {
	content := []boAdContentElement{
		{ID: "s1", Type: "subtitle", Value: "Cena especial"},
		{ID: "i1", Type: "image", Value: "https://cdn.example/current.webp"},
		{ID: "t1", Type: "title", Value: "Noche de verano"},
		{ID: "x1", Type: "text", Value: "Jardín iluminado con velas"},
	}
	prompt := boAdTextToImagePrompt(content)
	wantOrder := []string{"Cena especial", "Noche de verano", "Jardín iluminado con velas"}
	last := -1
	for _, part := range wantOrder {
		idx := strings.Index(prompt, part)
		if idx < 0 {
			t.Fatalf("prompt missing %q: %s", part, prompt)
		}
		if idx <= last {
			t.Fatalf("prompt order is wrong: %s", prompt)
		}
		last = idx
	}
	if strings.Contains(prompt, "cdn.example") {
		t.Fatalf("image URL must not be used as prompt text: %s", prompt)
	}
}

func TestBOAdWaveSpeedModelEndpoints(t *testing.T) {
	base := "https://api.wavespeed.ai/"
	if got := boAdTextToImageURL(base); got != "https://api.wavespeed.ai/api/v3/wavespeed-ai/z-image/turbo" {
		t.Fatalf("unexpected t2i url: %s", got)
	}
	if got := boAdEnhanceURL(base); got != "https://api.wavespeed.ai/api/v3/openai/gpt-image-2/edit" {
		t.Fatalf("unexpected enhance url: %s", got)
	}
}

func TestNormalizeBOAdCTAsValidatesNavigationTargets(t *testing.T) {
	valid := []boAdCTA{
		{ID: "route", Text: "Reservar", NavigationMode: "route", Route: "/reservas"},
		{ID: "external", Text: "Comprar", NavigationMode: "custom", CustomURL: "https://tickets.example/event"},
	}
	got, err := normalizeBOAdCTAs(valid)
	if err != nil || len(got) != 2 {
		t.Fatalf("expected valid CTAs, got %#v err=%v", got, err)
	}
	if _, err := normalizeBOAdCTAs([]boAdCTA{{ID: "bad", NavigationMode: "route", Route: "/admin"}}); err == nil {
		t.Fatal("expected unknown route to be rejected")
	}
	if _, err := normalizeBOAdCTAs([]boAdCTA{{ID: "bad", NavigationMode: "custom", CustomURL: "javascript:alert(1)"}}); err == nil {
		t.Fatal("expected unsafe custom URL to be rejected")
	}
}

func TestBOAdPublicRoutesMatchWebsiteStandard(t *testing.T) {
	want := []string{"/", "/contacto", "/eventos", "/menufindesemana", "/menudeldia", "/menusdegrupos", "/postres", "/vinos", "/cafes", "/bebidas", "/reservas", "/reservas.php", "/avisolegal", "/avisolegal.html", "/booking-policies", "/booking_policies.php", "/confirm", "/cancel", "/update-rice", "/protecciondatos", "/protecciondatos.html", "/menusanvalentin", "/regala"}
	if len(boAdPublicRoutes) != len(want) {
		t.Fatalf("expected %d routes, got %d: %#v", len(want), len(boAdPublicRoutes), boAdPublicRoutes)
	}
	for i := range want {
		if boAdPublicRoutes[i] != want[i] {
			t.Fatalf("route %d mismatch: got %q want %q", i, boAdPublicRoutes[i], want[i])
		}
	}
}

func TestBOAdPromptRequiresWrittenText(t *testing.T) {
	if got := boAdTextToImagePrompt([]boAdContentElement{{ID: "i1", Type: "image", Value: "https://cdn.example/ad.webp"}}); got != "" {
		t.Fatalf("expected empty prompt without text, got %q", got)
	}
}
