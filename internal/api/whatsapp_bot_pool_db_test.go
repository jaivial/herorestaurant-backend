package api

import (
	"context"
	"testing"
	"time"
)

func TestPickUAZAPIServer_SkipsFullServers_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	ctx := context.Background()

	suffix := time.Now().Format("150405.000")
	// Full server (used_count == capacity) should be skipped.
	fullRes, err := db.Exec(`
		INSERT INTO uazapi_servers (name, base_url, admin_token, capacity, used_count, priority, is_active)
		VALUES (?, ?, 'a', 5, 5, 10, 1)
	`, "full-"+suffix, "https://full-"+suffix+".example.com")
	if err != nil {
		t.Fatalf("insert full server: %v", err)
	}
	fullID, _ := fullRes.LastInsertId()
	defer func() { _, _ = db.Exec(`DELETE FROM uazapi_servers WHERE id = ?`, fullID) }()

	// Server with room should be selected even at lower priority.
	freeRes, err := db.Exec(`
		INSERT INTO uazapi_servers (name, base_url, admin_token, capacity, used_count, priority, is_active)
		VALUES (?, ?, 'b', 5, 1, 20, 1)
	`, "free-"+suffix, "https://free-"+suffix+".example.com")
	if err != nil {
		t.Fatalf("insert free server: %v", err)
	}
	freeID, _ := freeRes.LastInsertId()
	defer func() { _, _ = db.Exec(`DELETE FROM uazapi_servers WHERE id = ?`, freeID) }()

	// Deactivate any other active servers so the pool is deterministic.
	_, _ = db.Exec(`UPDATE uazapi_servers SET is_active = 0 WHERE id NOT IN (?, ?)`, fullID, freeID)
	defer func() { _, _ = db.Exec(`UPDATE uazapi_servers SET is_active = 1 WHERE id NOT IN (?, ?)`, fullID, freeID) }()

	rec, err := s.pickUAZAPIServer(ctx)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if rec.ID != freeID {
		t.Errorf("picked server %d, want free server %d (full must be skipped)", rec.ID, freeID)
	}

	// Now mark the free server full too -> pool exhausted -> error.
	if _, err := db.Exec(`UPDATE uazapi_servers SET used_count = capacity WHERE id = ?`, freeID); err != nil {
		t.Fatalf("update free->full: %v", err)
	}
	if _, err := s.pickUAZAPIServer(ctx); err == nil {
		t.Fatal("expected error when all servers are at capacity")
	}
}
