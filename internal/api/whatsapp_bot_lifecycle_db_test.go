package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// seedUAZAPIInstance provisions a uazapi_servers row + an active
// restaurant_uazapi_instances row pointing at the given base URL.
func seedUAZAPIInstance(t *testing.T, s *Server, restaurantID int, baseURL string) (string, func()) {
	t.Helper()
	res, err := s.db.Exec(`
		INSERT INTO uazapi_servers (name, base_url, admin_token, capacity, used_count, priority, is_active)
		VALUES (?, ?, 'admin-tok', 100, 0, 100, 1)
	`, "test-srv-"+time.Now().Format("150405.000"), baseURL)
	if err != nil {
		t.Fatalf("insert uazapi_servers: %v", err)
	}
	serverID, _ := res.LastInsertId()

	token := "inst-tok-" + time.Now().Format("150405.000000000")
	_, err = s.db.Exec(`
		INSERT INTO restaurant_uazapi_instances
			(restaurant_id, server_id, instance_name, instance_token, status, is_active, connected_phone, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'connected', 1, '34600111222', NOW(), NOW())
	`, restaurantID, serverID, "inst-"+time.Now().Format("150405.000"), token)
	if err != nil {
		t.Fatalf("insert restaurant_uazapi_instances: %v", err)
	}
	return token, func() {
		_, _ = s.db.Exec(`DELETE FROM restaurant_uazapi_instances WHERE restaurant_id = ?`, restaurantID)
		_, _ = s.db.Exec(`DELETE FROM uazapi_servers WHERE id = ?`, serverID)
	}
}

func TestSuspendAndReactivateUAZAPIInstance_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()

	uaz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer uaz.Close()

	token, cleanupInst := seedUAZAPIInstance(t, s, rid, uaz.URL)
	defer cleanupInst()

	ctx := context.Background()

	// Active instance must resolve for inbound routing.
	if _, ok := s.resolveBotRestaurant(ctx, token, ""); !ok {
		t.Fatal("expected active instance to resolve before suspend")
	}

	// Suspend on subscription lapse.
	if err := s.suspendRestaurantUAZAPIInstance(ctx, rid); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, ok := s.resolveBotRestaurant(ctx, token, ""); ok {
		t.Fatal("suspended instance must NOT resolve for inbound routing")
	}
	rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, rid)
	if err != nil || !found {
		t.Fatalf("load after suspend: found=%v err=%v", found, err)
	}
	if rec.Status != "suspended" {
		t.Errorf("status after suspend = %q, want suspended", rec.Status)
	}

	// Reactivate on re-subscribe (reuse same token, no new instance).
	reactivated, err := s.reactivateRestaurantUAZAPIInstance(ctx, rid)
	if err != nil || !reactivated {
		t.Fatalf("reactivate: ok=%v err=%v", reactivated, err)
	}
	if _, ok := s.resolveBotRestaurant(ctx, token, ""); !ok {
		t.Fatal("reactivated instance must resolve for inbound routing again")
	}
}
