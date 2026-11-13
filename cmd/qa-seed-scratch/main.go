// Command qa-seed-scratch creates (or removes) ONE QA backoffice account on the
// DISPOSABLE scratch PostgreSQL only (repair-v22, coordination id
// qa_seed_scratch_v1). Manual QA helper; not a test or fixture suite.
//
// Refuses unless ALL hold: QA_SCRATCH_SEED=1; DATABASE_URL is postgres:// on a
// unix socket (?host=/...) or loopback; QA_SCRATCH_DB_NAME equals the connected
// current_database() and does not contain "prod". The random password is
// generated in-process and written ONLY to a 0600 file (never printed); the
// created ids go to a 0600 state file used by -cleanup, which deletes exactly
// those rows plus the account's own bo_sessions.
//
//	QA_SCRATCH_SEED=1 QA_SCRATCH_DB_NAME=<db> DATABASE_URL=... qa-seed-scratch [-restaurant 1]
//	QA_SCRATCH_SEED=1 QA_SCRATCH_DB_NAME=<db> DATABASE_URL=... qa-seed-scratch -cleanup
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"preactvillacarmen/internal/sqldb"
)

type state struct {
	UserID       int64  `json:"user_id"`
	RestaurantID int    `json:"restaurant_id"`
	Email        string `json:"email"`
	CreatedAt    string `json:"created_at"`
	Database     string `json:"database"`
}

func main() {
	cleanup := flag.Bool("cleanup", false, "delete exactly what the seed created (from the state file)")
	rid := flag.Int("restaurant", 1, "restaurant to grant (role admin)")
	secretFile := flag.String("secret-file", "/tmp/qa-scratch-login.txt", "0600 file receiving the login (email + password); never printed")
	stateFile := flag.String("state-file", "/tmp/qa-scratch-seed.json", "0600 state file with created ids")
	flag.Parse()
	dsn := os.Getenv("DATABASE_URL")
	want := strings.TrimSpace(os.Getenv("QA_SCRATCH_DB_NAME"))
	if os.Getenv("QA_SCRATCH_SEED") != "1" || want == "" || strings.Contains(strings.ToLower(want), "prod") {
		fail("refused: needs QA_SCRATCH_SEED=1 and QA_SCRATCH_DB_NAME (non-production scratch)")
	}
	if !localDSN(dsn) {
		fail("refused: DATABASE_URL must be postgres:// on a unix socket or loopback")
	}
	db, err := sqldb.OpenPostgres(dsn, sqldb.SchemaBusiness)
	if err != nil {
		fail("open failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var cur string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&cur); err != nil || cur != want {
		fail("refused: connected database does not match QA_SCRATCH_DB_NAME")
	}
	if *cleanup {
		doCleanup(ctx, db, *stateFile, cur)
		return
	}
	doSeed(ctx, db, *rid, *secretFile, *stateFile, cur)
}

func doSeed(ctx context.Context, db *sql.DB, rid int, secretFile, stateFile, dbName string) {
	if _, err := os.Stat(stateFile); err == nil {
		fail("refused: state file exists (run -cleanup first)")
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		fail("random failed")
	}
	password := base64.RawURLEncoding.EncodeToString(buf)
	tag := make([]byte, 4)
	_, _ = rand.Read(tag)
	email := "qa-scratch-" + hex.EncodeToString(tag) + "@invalid.local"
	username := "qa-scratch-" + hex.EncodeToString(tag)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fail("hash failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		fail("begin failed")
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM restaurants WHERE id = $1", rid).Scan(&exists); err != nil || exists != 1 {
		fail("refused: restaurant not found")
	}
	var uid int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO bo_users (email, username, name, password_hash, is_superadmin, must_change_password) VALUES ($1, $2, 'QA scratch', $3, 0, 0) RETURNING id", email, username, string(hash)).Scan(&uid); err != nil {
		fail("insert bo_users failed: " + err.Error())
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO bo_user_restaurants (user_id, restaurant_id, role) VALUES ($1, $2, 'admin')", uid, rid); err != nil {
		fail("insert bo_user_restaurants failed: " + err.Error())
	}
	f, err := os.OpenFile(secretFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail("refused: secret file exists or cannot be created")
	}
	_, werr := fmt.Fprintf(f, "identifier=%s%cpassword=%s%c", email, 10, password, 10)
	_ = f.Close()
	if werr != nil {
		fail("secret write failed")
	}
	st, _ := json.Marshal(state{UserID: uid, RestaurantID: rid, Email: email, CreatedAt: time.Now().UTC().Format(time.RFC3339), Database: dbName})
	if err := os.WriteFile(stateFile, st, 0o600); err != nil {
		fail("state write failed")
	}
	if err := tx.Commit(); err != nil {
		fail("commit failed")
	}
	fmt.Printf("qa-seed-scratch: created user_id=%d restaurant=%d role=admin; login in %s (0600)%c", uid, rid, secretFile, 10)
}

func doCleanup(ctx context.Context, db *sql.DB, stateFile, dbName string) {
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		fail("no state file")
	}
	var st state
	if json.Unmarshal(raw, &st) != nil || st.UserID <= 0 || st.Database != dbName || !strings.HasPrefix(st.Email, "qa-scratch-") {
		fail("refused: state file invalid or for another database")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		fail("begin failed")
	}
	defer tx.Rollback()
	var email string
	if err := tx.QueryRowContext(ctx, "SELECT email FROM bo_users WHERE id = $1", st.UserID).Scan(&email); err != nil || email != st.Email {
		fail("refused: user row does not match state")
	}
	s, err := tx.ExecContext(ctx, "DELETE FROM bo_sessions WHERE user_id = $1", st.UserID)
	if err != nil {
		fail("delete bo_sessions failed: " + err.Error())
	}
	m, err := tx.ExecContext(ctx, "DELETE FROM bo_user_restaurants WHERE user_id = $1 AND restaurant_id = $2", st.UserID, st.RestaurantID)
	if err != nil {
		fail("delete bo_user_restaurants failed: " + err.Error())
	}
	u, err := tx.ExecContext(ctx, "DELETE FROM bo_users WHERE id = $1 AND email = $2", st.UserID, st.Email)
	if err != nil {
		fail("delete bo_users failed (other rows reference the account; clean them first): " + err.Error())
	}
	if err := tx.Commit(); err != nil {
		fail("commit failed")
	}
	ns, _ := s.RowsAffected()
	nm, _ := m.RowsAffected()
	nu, _ := u.RowsAffected()
	_ = os.Remove(stateFile)
	fmt.Printf("qa-seed-scratch: removed sessions=%d memberships=%d users=%d (delete the 0600 login file yourself)%c", ns, nm, nu, 10)
}

func localDSN(dsn string) bool {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return false
	}
	if h := u.Query().Get("host"); h != "" {
		return strings.HasPrefix(h, "/")
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "qa-seed-scratch:", msg)
	os.Exit(2)
}
