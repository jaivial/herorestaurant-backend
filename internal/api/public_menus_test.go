package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsPublicMenuType(t *testing.T) {
	tests := []struct {
		name     string
		menuType string
		want     bool
	}{
		{"closed_conventional is public", "closed_conventional", true},
		{"closed_group is public", "closed_group", true},
		{"a_la_carte is public", "a_la_carte", true},
		{"a_la_carte_group is public", "a_la_carte_group", true},
		{"special is public", "special", true},
		{"invalid type is not public", "invalid", false},
		{"empty string is not public", "", false},
		{"dessert is not public", "dessert", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPublicMenuType(tt.menuType); got != tt.want {
				t.Errorf("isPublicMenuType(%q) = %v, want %v", tt.menuType, got, tt.want)
			}
		})
	}
}

func TestBuildPublicMenuSlug(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		menuID int64
		want   string
	}{
		{"basic menu", "Menú del Día", 123, "menu-del-dia-123"},
		{"with spaces", "  Menu   Principal  ", 456, "menu-principal-456"},
		{"with special chars", "Menú Especial: Éste es!@#", 789, "menu-especial-este-es-789"},
		{"with numbers", "Menu123", 1, "menu123-1"},
		{"empty title defaults to menu", "", 42, "menu-42"},
		{"spanish chars", "Cerveza", 1, "cerveza-1"},
		{"uppercase normalized", "MARIDAJE", 5, "maridaje-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildPublicMenuSlug(tt.title, tt.menuID); got != tt.want {
				t.Errorf("buildPublicMenuSlug(%q, %d) = %q, want %q", tt.title, tt.menuID, got, tt.want)
			}
		})
	}
}

func TestBuildFallbackPublicSectionDishes(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		want  int // expected number of dishes
	}{
		{"empty slice", []string{}, 0},
		{"single item", []string{"Patatas"}, 1},
		{"multiple items", []string{"Entrante", "Segundo", "Postre"}, 3},
		{"with empty strings filters out", []string{"", "Valid", ""}, 1},
		{"all empty strings", []string{"", ""}, 0},
		{"with whitespace trims", []string{"  Salmorejo  "}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFallbackPublicSectionDishes(tt.items)
			if len(got) != tt.want {
				t.Errorf("buildFallbackPublicSectionDishes(%v) returned %d dishes, want %d", tt.items, len(got), tt.want)
			}
		})
	}
}

func TestBuildFallbackPublicSections(t *testing.T) {
	t.Run("builds sections from entrantes principales and postre", func(t *testing.T) {
		menu := publicMenuItem{
			Entrantes: []string{"Ensalada", "Sopa"},
			Principales: publicMenuPrincipales{
				TituloPrincipales: "Platos Principales",
				Items:             []string{"Paella", "Filete"},
			},
			Postre: []string{"Flan", "Tarta"},
		}

		sections := buildFallbackPublicSections(menu)

		if len(sections) != 3 {
			t.Fatalf("expected 3 sections, got %d", len(sections))
		}

		// Check entrantes section
		if sections[0].Title != "Entrantes" {
			t.Errorf("first section title = %q, want %q", sections[0].Title, "Entrantes")
		}
		if sections[0].Kind != "entrantes" {
			t.Errorf("first section kind = %q, want %q", sections[0].Kind, "entrantes")
		}
		if len(sections[0].Dishes) != 2 {
			t.Errorf("entrantes dishes = %d, want 2", len(sections[0].Dishes))
		}

		// Check principales section with custom title
		if sections[1].Title != "Platos Principales" {
			t.Errorf("second section title = %q, want %q", sections[1].Title, "Platos Principales")
		}
		if sections[1].Kind != "principales" {
			t.Errorf("second section kind = %q, want %q", sections[1].Kind, "principales")
		}

		// Check postre section
		if sections[2].Title != "Postres" {
			t.Errorf("third section title = %q, want %q", sections[2].Title, "Postres")
		}
		if sections[2].Kind != "postres" {
			t.Errorf("third section kind = %q, want %q", sections[2].Kind, "postres")
		}
	})

	t.Run("uses default principales title when empty", func(t *testing.T) {
		menu := publicMenuItem{
			Entrantes:   []string{"Ensalada"},
			Principales: publicMenuPrincipales{Items: []string{"Paella"}},
			Postre:      []string{},
		}

		sections := buildFallbackPublicSections(menu)

		// Should have only entrantes and principales (no postre)
		if len(sections) != 2 {
			t.Fatalf("expected 2 sections, got %d", len(sections))
		}

		if sections[1].Title != "Principales" {
			t.Errorf("principales section title = %q, want default %q", sections[1].Title, "Principales")
		}
	})

	t.Run("handles empty menu gracefully", func(t *testing.T) {
		menu := publicMenuItem{}
		sections := buildFallbackPublicSections(menu)

		if len(sections) != 0 {
			t.Errorf("expected 0 sections for empty menu, got %d", len(sections))
		}
	})
}

func TestPublicMenuItemHomeIncludesSpecialMenuImageURL(t *testing.T) {
	item := publicMenuItemHome{
		ID:                   14,
		Slug:                 "menu-especial-14",
		MenuTitle:            "Menu San Valentin",
		MenuType:             "special",
		Active:               true,
		MenuSubtitle:         []string{"Un menu romantico"},
		ShowDishImages:       false,
		ShowMenuPreviewImage: false,
		MenuPreviewImageURL:  "",
		SpecialMenuImageURL:  "https://cdn.example.com/special-menu-14.webp",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal(publicMenuItemHome) error = %v", err)
	}

	raw := string(data)

	if !strings.Contains(raw, `"special_menu_image_url"`) {
		t.Errorf("publicMenuItemHome JSON does not contain 'special_menu_image_url': %s", raw)
	}
	if !strings.Contains(raw, `"https://cdn.example.com/special-menu-14.webp"`) {
		t.Errorf("publicMenuItemHome JSON does not contain the expected image URL: %s", raw)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	val, ok := decoded["special_menu_image_url"]
	if !ok {
		t.Fatal("publicMenuItemHome JSON missing key 'special_menu_image_url'")
	}
	strVal, ok := val.(string)
	if !ok {
		t.Fatalf("special_menu_image_url is %T, want string", val)
	}
	if strVal != "https://cdn.example.com/special-menu-14.webp" {
		t.Errorf("special_menu_image_url = %q, want %q", strVal, "https://cdn.example.com/special-menu-14.webp")
	}
}

func TestPublicMenuItemSpecialIncludesSpecialMenuImageURL(t *testing.T) {
	item := publicMenuItemSpecial{
		ID:                  14,
		MenuTitle:           "Menu San Valentin",
		MenuSubtitle:        []string{"Un menu romantico"},
		Comments:            []string{"Incluye champagne"},
		SpecialMenuImageURL: "https://cdn.example.com/special-menu-14.webp",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal(publicMenuItemSpecial) error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	val, ok := decoded["special_menu_image_url"]
	if !ok {
		t.Fatal("publicMenuItemSpecial JSON missing key 'special_menu_image_url'")
	}
	strVal, ok := val.(string)
	if !ok {
		t.Fatalf("special_menu_image_url is %T, want string", val)
	}
	if strVal != "https://cdn.example.com/special-menu-14.webp" {
		t.Errorf("special_menu_image_url = %q, want %q", strVal, "https://cdn.example.com/special-menu-14.webp")
	}
}

func TestPublicMenuItemIncludesSpecialMenuImageURLForSpecialType(t *testing.T) {
	item := publicMenuItem{
		ID:                  14,
		Slug:                "menu-especial-14",
		MenuTitle:           "Menu San Valentin",
		MenuType:            "special",
		Price:               "0",
		Active:              true,
		MenuSubtitle:        []string{"Un menu romantico"},
		SpecialMenuImageURL: "https://cdn.example.com/special-menu-14.webp",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal(publicMenuItem) error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	val, ok := decoded["special_menu_image_url"]
	if !ok {
		t.Fatal("publicMenuItem JSON missing key 'special_menu_image_url'")
	}
	strVal, ok := val.(string)
	if !ok {
		t.Fatalf("special_menu_image_url is %T, want string", val)
	}
	if strVal != "https://cdn.example.com/special-menu-14.webp" {
		t.Errorf("special_menu_image_url = %q, want %q", strVal, "https://cdn.example.com/special-menu-14.webp")
	}
}

func TestPublicMenuItemHomeEmptySpecialMenuImageURL(t *testing.T) {
	item := publicMenuItemHome{
		ID:                  15,
		MenuType:            "closed_conventional",
		SpecialMenuImageURL: "",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	val, ok := decoded["special_menu_image_url"]
	if !ok {
		t.Fatal("publicMenuItemHome JSON missing key 'special_menu_image_url' even when empty")
	}
	strVal, _ := val.(string)
	if strVal != "" {
		t.Errorf("special_menu_image_url = %q, want empty string", strVal)
	}
}
