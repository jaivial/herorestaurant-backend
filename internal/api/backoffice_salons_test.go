package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"

	"preactvillacarmen/internal/config"
)

// Integration tests for the salons CRUD endpoints (/admin/config/salons).
// Set BUNNY_TEST_MYSQL_DSN to a THROWAWAY schema to run them.
func setupSalonsTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("BUNNY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BUNNY_TEST_MYSQL_DSN not set (skipping integration)")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`DELETE FROM restaurant_salons_overrides`,
		`DELETE FROM restaurant_salons`,
		`DELETE FROM restaurant_floor_overrides`,
		`DELETE FROM restaurant_floors WHERE restaurant_id = 1`,
		`INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active) VALUES (1, 0, 'Planta baja', 1, 1)`,
		`INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active) VALUES (1, 1, 'Planta 1', 0, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("cleanup: %v (q=%s)", err, q)
		}
	}
	s := NewServer(db, config.Config{})
	return s, db
}

// salonsReq invokes a handler directly with BO auth (same pattern as the
// MiniMax integration tests) and an injected chi route param when given.
func salonsReq(t *testing.T, h func(w http.ResponseWriter, r *http.Request), method, path, body string, param map[string]string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := withBOAuth(req.Context(), boAuth{ActiveRestaurantID: 1, Role: "root", User: boUser{ID: 7}})
	if len(param) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range param {
			rctx.URLParams.Add(k, v)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return out
}

func itoa(v int) string { return strconv.Itoa(v) }

func salonsList(t *testing.T, s *Server) []map[string]any {
	t.Helper()
	out := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons", "", nil)
	if out["success"] != true {
		t.Fatalf("list failed: %v", out)
	}
	raw, _ := json.Marshal(out["salons"])
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("salons decode: %v", err)
	}
	return list
}

func floorIDForNumber(t *testing.T, db *sql.DB, floorNumber int) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`SELECT id FROM restaurant_floors WHERE restaurant_id = 1 AND floor_number = ?`, floorNumber).Scan(&id); err != nil {
		t.Fatalf("floor lookup: %v", err)
	}
	return id
}

func TestSalonsCrudIntegration(t *testing.T) {
	s, db := setupSalonsTestServer(t)

	floor0 := floorIDForNumber(t, db, 0)
	floor1 := floorIDForNumber(t, db, 1)

	t.Run("create salon", func(t *testing.T) {
		out := salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
			`{"floorId":`+itoa(floor0)+`,"name":"Terraza","hasCapacityLimit":true,"capacityLimit":45}`, nil)
		if out["success"] != true {
			t.Fatalf("create failed: %v", out)
		}
		list := salonsList(t, s)
		if len(list) != 1 {
			t.Fatalf("expected 1 salon, got %d (%v)", len(list), list)
		}
		sal := list[0]
		if sal["name"] != "Terraza" || sal["floorId"].(float64) != float64(floor0) {
			t.Fatalf("bad salon: %v", sal)
		}
		if sal["hasCapacityLimit"] != true || sal["capacityLimit"].(float64) != 45 {
			t.Fatalf("bad capacity: %v", sal)
		}
	})

	t.Run("create salon without limit defaults active", func(t *testing.T) {
		out := salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
			`{"floorId":`+itoa(floor1)+`,"name":"Salon Privado","hasCapacityLimit":false,"capacityLimit":45}`, nil)
		if out["success"] != true {
			t.Fatalf("create failed: %v", out)
		}
		if len(salonsList(t, s)) != 2 {
			t.Fatalf("expected 2 salons")
		}
	})

	t.Run("duplicate name in same floor rejected", func(t *testing.T) {
		out := salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
			`{"floorId":`+itoa(floor0)+`,"name":"Terraza","hasCapacityLimit":false,"capacityLimit":45}`, nil)
		if out["success"] == true {
			t.Fatalf("expected duplicate rejection, got %v", out)
		}
	})

	t.Run("name required", func(t *testing.T) {
		out := salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
			`{"floorId":`+itoa(floor0)+`,"name":"  ","hasCapacityLimit":false,"capacityLimit":45}`, nil)
		if out["success"] == true {
			t.Fatalf("expected name validation failure, got %v", out)
		}
	})

	t.Run("update salon via PUT", func(t *testing.T) {
		list := salonsList(t, s)
		var id float64
		for _, sal := range list {
			if sal["name"] == "Terraza" {
				id = sal["id"].(float64)
			}
		}
		out := salonsReq(t, s.handleBOConfigSalonsUpdate, http.MethodPut, "/admin/config/salons/"+itoa(int(id)),
			`{"floorId":`+itoa(floor1)+`,"name":"Terraza VIP","hasCapacityLimit":true,"capacityLimit":80,"isActive":true}`,
			map[string]string{"salonId": itoa(int(id))})
		if out["success"] != true {
			t.Fatalf("update failed: %v", out)
		}
		for _, sal := range salonsList(t, s) {
			if sal["id"].(float64) == id {
				if sal["name"] != "Terraza VIP" || sal["capacityLimit"].(float64) != 80 || sal["floorId"].(float64) != float64(floor1) {
					t.Fatalf("bad update result: %v", sal)
				}
			}
		}
	})

	t.Run("delete salon", func(t *testing.T) {
		list := salonsList(t, s)
		var id float64
		for _, sal := range list {
			if sal["name"] == "Salon Privado" {
				id = sal["id"].(float64)
			}
		}
		out := salonsReq(t, s.handleBOConfigSalonsDelete, http.MethodDelete, "/admin/config/salons/"+itoa(int(id)), "",
			map[string]string{"salonId": itoa(int(id))})
		if out["success"] != true {
			t.Fatalf("delete failed: %v", out)
		}
		if len(salonsList(t, s)) != 1 {
			t.Fatalf("expected 1 salon after delete")
		}
	})

	t.Run("deleting floor cascades salons", func(t *testing.T) {
		out := salonsReq(t, s.handleBOConfigFloorsDefaultsSet, http.MethodPost, "/admin/config/floors/defaults", `{"count":1}`, nil)
		if out["success"] != true {
			t.Fatalf("floors defaults set failed: %v", out)
		}
		if list := salonsList(t, s); len(list) != 0 {
			t.Fatalf("expected cascade delete of salons on floor removal, got %v", list)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM restaurant_salons_overrides`).Scan(&count); err != nil {
			t.Fatalf("overrides count: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected salon overrides cascade, got %d", count)
		}
	})
}

func TestSalonsFloorDisableSyncIntegration(t *testing.T) {
	s, db := setupSalonsTestServer(t)
	floor1 := floorIDForNumber(t, db, 1)

	salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
		`{"floorId":`+itoa(floor1)+`,"name":"P1 Salon","hasCapacityLimit":false,"capacityLimit":45}`, nil)

	out := salonsReq(t, s.handleBOConfigFloorsDefaultsSet, http.MethodPost, "/admin/config/floors/defaults", `{"floorNumber":1,"active":false}`, nil)
	if out["success"] != true {
		t.Fatalf("floor disable failed: %v", out)
	}
	list := salonsList(t, s)
	if len(list) != 1 || list[0]["isActive"] != false {
		t.Fatalf("expected salon synced to inactive when floor disabled, got %v", list)
	}
}

func TestSalonsDayOverrideIntegration(t *testing.T) {
	s, db := setupSalonsTestServer(t)
	floor0 := floorIDForNumber(t, db, 0)

	salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
		`{"floorId":`+itoa(floor0)+`,"name":"S","hasCapacityLimit":false,"capacityLimit":45}`, nil)
	list := salonsList(t, s)
	salonID := int(list[0]["id"].(float64))

	out := salonsReq(t, s.handleBOConfigSalonsDayStatusSet, http.MethodPost, "/admin/config/salons/day-status",
		`{"date":"2026-08-18","salonId":`+itoa(salonID)+`,"active":false}`, nil)
	if out["success"] != true {
		t.Fatalf("day override failed: %v", out)
	}

	day := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-18", "", nil)
	raw, _ := json.Marshal(day["salons"])
	var dayList []map[string]any
	_ = json.Unmarshal(raw, &dayList)
	if len(dayList) != 1 || dayList[0]["isActive"] != false {
		t.Fatalf("expected overridden inactive salon on date, got %v", dayList)
	}

	base := salonsList(t, s)
	if len(base) != 1 || base[0]["isActive"] != true {
		t.Fatalf("expected default active salon, got %v", base)
	}
}
