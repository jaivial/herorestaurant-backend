// Command seed-legal-pages fills the legal_pages table for restaurant 1 with the
// original content that used to live as static markup:
//   - booking-policies: from the hardcoded api.BookingPoliciesHTML var.
//   - aviso-legal, proteccion-datos: from the embedded HTML snapshots under
//     ./content, converted 1:1 from the former static preact JSX.
//
// Run once after the 051_legal_pages migration is applied:
//
//	cd backend && env $(cat .env | xargs) go run ./cmd/seed-legal-pages
//
// It is re-runnable: each UPDATE only touches a row that still holds the
// migration placeholder, so a page edited in the backoffice is never
// overwritten.
package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	bizdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/sqldb"
)

//go:embed content/*.html
var contentFS embed.FS

func main() {
	_ = godotenv.Load()

	host := envDefault("DB_HOST", "127.0.0.1")
	port := envDefault("DB_PORT", "3306")
	user := envDefault("DB_USER", "villacarmen")
	password := envDefault("DB_PASSWORD", "villacarmen")
	dbName := envDefault("DB_NAME", "villacarmen")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// db_dialect_selection_v1: MySQL keeps this tool's own DSN unchanged; PG
	// opens only through bizdb.OpenBusinessForTool and is REPORT ONLY (counts
	// the placeholder rows it would seed; no UPDATE) until runtime QA.
	d, derr := bizdb.BusinessDialect(os.Getenv("DB_DRIVER"))
	if derr != nil {
		log.Fatal(derr)
	}
	var db *sql.DB
	if d == sqldb.Postgres {
		pg, _, err := bizdb.OpenBusinessForTool(ctx, config.Load())
		if err != nil {
			log.Fatalf("open db: %v", err)
		}
		db = pg
		dialect = sqldb.Postgres
	} else {
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci",
			user, password, host, port, dbName)
		my, err := sql.Open("mysql", dsn)
		if err != nil {
			log.Fatalf("open db: %v", err)
		}
		db = my
		if err := db.PingContext(ctx); err != nil {
			log.Fatalf("ping db: %v", err)
		}
	}
	defer db.Close()

	avisoLegal := mustReadContent("content/aviso-legal.html")
	proteccionDatos := mustReadContent("content/proteccion-datos.html")

	seeds := []struct {
		slug string
		html string
	}{
		{"booking-policies", api.BookingPoliciesHTML},
		{"aviso-legal", avisoLegal},
		{"proteccion-datos", proteccionDatos},
	}

	for _, s := range seeds {
		if dialect == sqldb.Postgres {
			var n int
			if err := db.QueryRowContext(ctx, nativeSQL(`SELECT COUNT(*) FROM legal_pages WHERE restaurant_id = 1 AND slug = ? AND content_html = '<p>Placeholder</p>'`, `SELECT COUNT(*) FROM legal_pages WHERE restaurant_id = 1 AND app.norm_ascii(slug) = app.norm_ascii(?) AND content_html = '<p>Placeholder</p>'`), s.slug).Scan(&n); err != nil {
				log.Fatalf("report %s: %v", s.slug, err)
			}
			fmt.Printf("seed-legal-pages [PG report only]: %s placeholder rows = %d (not written)\n", s.slug, n)
			continue
		}
		// Portable text (content_json is text on PG); runs only on MySQL now.
		res, err := db.ExecContext(ctx, portableSQL(`
			UPDATE legal_pages
			SET content_html = ?, content_json = '[]'
			WHERE restaurant_id = 1 AND slug = ? AND content_html = '<p>Placeholder</p>'
		`), s.html, s.slug)
		if err != nil {
			log.Fatalf("seed %s: %v", s.slug, err)
		}
		n, _ := res.RowsAffected()
		fmt.Printf("seed-legal-pages: %s rows updated = %d\n", s.slug, n)
	}
}

func mustReadContent(name string) string {
	b, err := contentFS.ReadFile(name)
	if err != nil {
		log.Fatalf("read embedded %s: %v", name, err)
	}
	return string(b)
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
