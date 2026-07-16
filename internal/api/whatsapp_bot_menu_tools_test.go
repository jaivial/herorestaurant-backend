package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBotMenuCategoryLabel(t *testing.T) {
	cases := map[string]string{
		"closed_conventional": "Menú cerrado convencional",
		"closed_group":        "Menú cerrado de grupo",
		"a_la_carte":          "A la carta convencional",
		"a_la_carte_group":    "A la carta de grupo",
		"special":             "Menú especial",
	}
	for k, want := range cases {
		if got := botMenuCategoryLabel(k); got != want {
			t.Errorf("botMenuCategoryLabel(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestBotBeverageLabel(t *testing.T) {
	if botBeverageLabel("ilimitada") != "Bebida ilimitada" {
		t.Error("ilimitada label")
	}
	if botBeverageLabel("no_incluida") != "Bebida no incluida" {
		t.Error("no_incluida label")
	}
}

func TestBotMenuToolDefs_Present(t *testing.T) {
	names := map[string]bool{}
	for _, d := range botToolDefs(botTenantConfig{}) {
		names[d.Name] = true
	}
	for _, want := range []string{"list_menus", "get_menu_details", "get_coffee_menu", "get_drinks_menu", "get_wines_menu"} {
		if !names[want] {
			t.Errorf("missing menu tool %q", want)
		}
	}
}

func TestBotToolMenus_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	defer func() {
		_, _ = db.Exec(`DELETE FROM group_menu_section_dishes_v2 WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM group_menu_sections_v2 WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM menus WHERE restaurant_id = ?`, rid)
	}()

	ctx := context.Background()
	beverage := `{"type":"ilimitada","price_per_person":8,"has_supplement":false,"supplement_price":null}`
	res, err := db.Exec(`
		INSERT INTO menus (restaurant_id, menu_title, menu_type, price, active, is_draft,
			menu_subtitle, entrantes, principales, postre, beverage, comments,
			min_party_size, main_dishes_limit, main_dishes_limit_number, included_coffee)
		VALUES (?, 'Menú Grupo Test', 'closed_group', 42.50, 1, 0,
			'["Ideal para grupos"]', '[]', '{}', '[]', ?, '["Reserva con 24h"]',
			8, 1, 2, 1)
	`, rid, beverage)
	if err != nil {
		t.Fatalf("seed menu: %v", err)
	}
	menuID, _ := res.LastInsertId()

	secRes, err := db.Exec(`
		INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position)
		VALUES (?, ?, 'Entrantes', 'starter', 0)
	`, rid, menuID)
	if err != nil {
		t.Fatalf("seed section: %v", err)
	}
	sectionID, _ := secRes.LastInsertId()

	if _, err := db.Exec(`
		INSERT INTO group_menu_section_dishes_v2 (restaurant_id, menu_id, section_id,
			title_snapshot, description_snapshot, price, supplement_enabled, supplement_price, active, position)
		VALUES (?, ?, ?, 'Croquetas caseras', 'De jamón ibérico', NULL, 1, 3.50, 1, 0)
	`, rid, menuID, sectionID); err != nil {
		t.Fatalf("seed dish: %v", err)
	}

	// list_menus
	out, err := s.botToolListMenus(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Count int `json:"count"`
		Menus []struct {
			ID            int64  `json:"menu_id"`
			Title         string `json:"title"`
			Category      string `json:"category"`
			CategoryLabel string `json:"category_label"`
			Price         string `json:"price"`
		} `json:"menus"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Menus[0].Category != "closed_group" || list.Menus[0].CategoryLabel != "Menú cerrado de grupo" {
		t.Fatalf("list_menus = %s", out)
	}

	// get_menu_details
	out, err = s.botToolMenuDetails(ctx, rid, json.RawMessage(`{"menu_id":`+jsonInt(menuID)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Menú Grupo Test", "closed_group", "Menú cerrado de grupo",
		"Croquetas caseras", "De jamón ibérico",
		"ilimitada", "Bebida ilimitada",
		"Reserva con 24h", "Entrantes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("menu details missing %q in %s", want, out)
		}
	}
	var details struct {
		Settings struct {
			CoffeeIncluded        bool `json:"coffee_included"`
			MinPartySize          int  `json:"min_party_size"`
			MaxMainDishesPerTable int  `json:"max_main_dishes_per_table"`
			UnlimitedDrinks       bool `json:"unlimited_drinks"`
			DrinkPricePerPerson   any  `json:"drink_price_per_person"`
		} `json:"settings"`
	}
	_ = json.Unmarshal([]byte(out), &details)
	if !details.Settings.CoffeeIncluded || details.Settings.MinPartySize != 8 || !details.Settings.UnlimitedDrinks {
		t.Errorf("settings = %s", out)
	}

	// unknown menu id
	out, _ = s.botToolMenuDetails(ctx, rid, json.RawMessage(`{"menu_id":999999}`))
	if !strings.Contains(out, "no encontrado") {
		t.Errorf("expected not-found, got %s", out)
	}
}

func TestBotToolBeverages_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	defer func() {
		_, _ = db.Exec(`DELETE FROM CAFES WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM BEBIDAS WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM VINOS WHERE restaurant_id = ?`, rid)
	}()

	ctx := context.Background()
	_, _ = db.Exec(`INSERT INTO CAFES (restaurant_id, tipo, nombre, precio, descripcion, titulo, active) VALUES (?, 'CAFE', 'Café solo', 1.50, 'Espresso', 'Cafés', 1)`, rid)
	_, _ = db.Exec(`INSERT INTO BEBIDAS (restaurant_id, tipo, nombre, precio, descripcion, titulo, active) VALUES (?, 'BEBIDA', 'Agua mineral', 2.00, '50cl', 'Refrescos', 1)`, rid)
	_, _ = db.Exec(`INSERT INTO VINOS (restaurant_id, nombre, precio, descripcion, tipo, bodega, denominacion_origen, anyo, active) VALUES (?, 'Rioja Reserva', 18.00, 'Tinto con cuerpo', 'TINTO', 'Bodega X', 'DO Rioja', '2018', 1)`, rid)
	_ = time.Now()

	coffee, _ := s.botToolCoffeeMenu(ctx, rid)
	if !strings.Contains(coffee, "Café solo") || !strings.Contains(coffee, "Espresso") {
		t.Errorf("coffee = %s", coffee)
	}
	drinks, _ := s.botToolDrinksMenu(ctx, rid)
	if !strings.Contains(drinks, "Agua mineral") {
		t.Errorf("drinks = %s", drinks)
	}
	wines, _ := s.botToolWinesMenu(ctx, rid)
	for _, want := range []string{"Rioja Reserva", "TINTO", "DO Rioja", "Bodega X"} {
		if !strings.Contains(wines, want) {
			t.Errorf("wines missing %q in %s", want, wines)
		}
	}
}
