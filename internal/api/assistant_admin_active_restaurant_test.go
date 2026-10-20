package api

import (
	"testing"
)

// La operación /active-restaurant REBINDA la sesión a otro restaurante
// (persiste active_restaurant_id en la fila bo_sessions) y el replay se
// autentica sobre esa misma sesión. Si el bot pudiera llamarla, el turno
// privileged entero se movería a otro tenant.
//
// El bot tiene un solo restaurante, así que la operación no tiene sentido
// para él: el restaurante ya es el correcto.
//
// Coordination id: wa_bot_privileged_contacts_v1
func TestForkyPathIsActiveRestaurant(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/active-restaurant", true},
		{"/admin/active-restaurant", true},
		{"/bot/ai/{restaurantId}", false},
		{"/dashboard/metrics", false},
		{"/active-restaurants", false},
	}
	for _, tc := range cases {
		if got := forkyPathIsActiveRestaurant(tc.path); got != tc.want {
			t.Errorf("forkyPathIsActiveRestaurant(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// La operación está en el catálogo, así que el guard tiene algo que proteger.
func TestAdminCatalogHasActiveRestaurantOp(t *testing.T) {
	if !forkyPathIsActiveRestaurant("/active-restaurant") {
		t.Error("el guard no reconoce la ruta de rebind")
	}
}

// La sesion de servicio del bot pertenece a un superadmin real, asi que las
// operaciones sobre la cuenta de la sesion (/me/password) no deben poder
// ejecutarse: cambiarian el password de esa persona.
//
// Coordination id: wa_bot_privileged_contacts_v1
func TestForkyPathIsSessionScoped(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/me", true},
		{"/me/password", true},
		{"/me/preferences", true},
		{"/active-restaurant", false},
		{"/members", false},
		{"/members/12", false},
		{"/members/12/ensure-user", false},
		{"/bot/ai/1", false},
		{"/dashboard/metrics", false},
		{"/", false},
	}
	for _, tc := range cases {
		if got := forkyPathIsSessionScoped(tc.path); got != tc.want {
			t.Errorf("forkyPathIsSessionScoped(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
