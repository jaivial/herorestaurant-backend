package api

import (
	"context"
	"testing"
	"time"
)

// provisionInstance wires a throwaway restaurant to a throwaway server of the
// given provider and returns the restaurant id.
func provisionInstance(t *testing.T, provider string) (int, func()) {
	t.Helper()
	db := testDB(t)
	suffix := time.Now().Format("150405.000000") + "-" + provider

	resRes, err := db.Exec(`INSERT INTO restaurants (name, slug) VALUES (?, ?)`, "wa-test-"+suffix, "wa-test-"+suffix)
	if err != nil {
		t.Fatalf("insert restaurant: %v", err)
	}
	restaurantID64, _ := resRes.LastInsertId()

	srvRes, err := db.Exec(`
		INSERT INTO uazapi_servers (name, provider, base_url, admin_token, capacity, used_count, priority, is_active)
		VALUES (?, ?, ?, 'admin-tok', 5, 1, 10, 1)
	`, "srv-"+suffix, provider, "https://srv-"+suffix+".example.com")
	if err != nil {
		t.Fatalf("insert server: %v", err)
	}
	serverID, _ := srvRes.LastInsertId()

	_, err = db.Exec(`
		INSERT INTO restaurant_uazapi_instances (restaurant_id, server_id, instance_name, provider_instance_id, instance_token, status, is_active)
		VALUES (?, ?, ?, ?, 'inst-tok', 'connected', 1)
	`, restaurantID64, serverID, "inst-"+suffix, "inst-"+suffix)
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	cleanup := func() {
		_, _ = db.Exec(`DELETE FROM restaurant_uazapi_instances WHERE restaurant_id = ?`, restaurantID64)
		_, _ = db.Exec(`DELETE FROM uazapi_servers WHERE id = ?`, serverID)
		_, _ = db.Exec(`DELETE FROM restaurants WHERE id = ?`, restaurantID64)
		db.Close()
	}
	return int(restaurantID64), cleanup
}

// An Evolution-backed restaurant must not yield UAZAPI credentials: callers of
// this chokepoint build UAZAPI-shaped URLs that Evolution answers with 404.
func TestLoadProvisionedUAZAPICredentials_SkipsEvolution_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)

	url, token, err := s.loadProvisionedUAZAPICredentials(context.Background(), restaurantID)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if url != "" || token != "" {
		t.Errorf("evolution instance leaked UAZAPI credentials: url=%q token=%q", url, token)
	}
}

func TestLoadProvisionedUAZAPICredentials_ReturnsUAZAPI_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "uazapi")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)

	url, token, err := s.loadProvisionedUAZAPICredentials(context.Background(), restaurantID)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if url == "" || token != "inst-tok" {
		t.Errorf("uazapi instance should yield credentials, got url=%q token=%q", url, token)
	}
}

// The gateway must still reach an Evolution restaurant even though the legacy
// UAZAPI chokepoint now refuses it.
func TestBotGatewayFor_EvolutionRestaurant_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)

	gw, ok := s.botGatewayFor(context.Background(), restaurantID)
	if !ok {
		t.Fatal("expected a gateway for an evolution-backed restaurant")
	}
	if _, isEvo := gw.(*evolutionGateway); !isEvo {
		t.Errorf("gateway = %T, want *evolutionGateway", gw)
	}
}
