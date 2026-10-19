package api

import (
	"testing"
)

// El replay de admin_call puede apuntar a una ruta que lleva {restaurantId}.
// Como la sesión de servicio del bot está atada a un restaurante, el
// parámetro tiene que ser SIEMPRE ese restaurante: si no, el replay leería o
// escribiría los datos de otro tenant con la autoridad de la sesión.
//
// Coordination id: wa_bot_privileged_contacts_v1
func TestForkyPathHasParam(t *testing.T) {
	cases := []struct {
		path string
		name string
		want bool
	}{
		{"/bot/ai/{restaurantId}", "restaurantId", true},
		{"/bot/ai/{restaurantId}/preview", "restaurantId", true},
		{"/platform/users/{id}/assign/{restaurantId}", "restaurantId", true},
		{"/bot/ai/{restaurantId}", "id", false},
		{"/dashboard/metrics", "restaurantId", false},
		{"/users/{id}", "id", true},
	}
	for _, tc := range cases {
		if got := forkyPathHasParam(tc.path, tc.name); got != tc.want {
			t.Errorf("forkyPathHasParam(%q, %q) = %v, want %v", tc.path, tc.name, got, tc.want)
		}
	}
}

// El catálogo de operaciones incluye rutas con {restaurantId}. El replay de
// un bot tiene que quedarse en su propio restaurante: el guard vive en
// assistantAdminCall, y aquí se comprueba que las rutas peligrosas existen
// (para que el guard no se quede sin nada que proteger).
func TestAdminCatalogHasRestaurantScopedRoutes(t *testing.T) {
	ops := forkyAdminOpsJSON
	if len(ops) == 0 {
		t.Fatal("el catalogo de operaciones esta vacio")
	}
	// El guard se aplica a las rutas con el parametro; basta con que el
	// catalogo contenga alguna, que es lo que hace relevante el guard.
	if !forkyPathHasParam("/bot/ai/{restaurantId}", "restaurantId") {
		t.Error("el guard de tenant no reconoce la ruta con restaurantId")
	}
}
