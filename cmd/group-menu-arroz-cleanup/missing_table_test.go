package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Not every deployment has the comida_items table. The server treats a missing
// one as an empty source ("if rows, err := ...; err == nil"), so this command
// must too. Before the fix it did log.Fatalf on the very first menu, which made
// the cleanup impossible to run on any such database.
//
// These tests pin the rule itself, so it survives a refactor of the query.

func TestIsMissingTableErrSoloParaLaTablaInexistente(t *testing.T) {
	// 1146 ER_NO_SUCH_TABLE: a deployment whose schema has no comida_items.
	if !isMissingTableErr(&mysql.MySQLError{Number: 1146, Message: "Table 'herorestaurant.comida_items' doesn't exist"}) {
		t.Fatal("1146 (tabla inexistente) debe tratarse como fuente vacia, no como error fatal")
	}
	// A missing COLUMN in an existing table is a real bug in the query and must
	// still stop the run rather than silently deriving an empty set.
	if isMissingTableErr(&mysql.MySQLError{Number: 1054, Message: "Unknown column 'nombre'"}) {
		t.Fatal("1054 (columna inexistente) es un fallo real: no se puede degradar a fuente vacia")
	}
	// A connection failure must never be mistaken for an absent table.
	if isMissingTableErr(errors.New("dial tcp 127.0.0.1:3306: connection refused")) {
		t.Fatal("un error de conexion no es una tabla inexistente")
	}
	if isMissingTableErr(nil) {
		t.Fatal("nil no es una tabla inexistente")
	}
}

// The end-to-end shape of the bug, against the real database: with the catalog
// table absent, the arroz section still decides what is derived and the run
// does not abort. It then removes the derived rice and keeps everything else —
// the same set the server's write path refuses to persist.
func TestLaTablaDeCatalogoAusenteNoAbortaLaLimpieza(t *testing.T) {
	database := arrozCleanupTestDB(t)

	const restaurant, menu = 99002, 99002
	arrozCleanupSeed(t, database, restaurant, menu)

	// Make sure the catalog table is genuinely absent, which is the situation
	// this test exists for.
	var exists int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'comida_items'`,
	).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 0 {
		t.Skip("esta base si tiene comida_items; lo que se prueba aqui es su ausencia")
	}

	derived, err := derivedArrozNames(arrozCleanupCtx(t), database, restaurant, menu)
	if err != nil {
		t.Fatalf("una tabla de catalogo ausente no puede abortar la limpieza: %v", err)
	}
	if len(derived) != 2 {
		t.Fatalf("derived %v, want the 2 dishes of the arroz section", derived)
	}
	for _, want := range []string{"Arroz a banda", "Arroz del senyoret"} {
		found := false
		for _, got := range derived {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("derived %v no contiene %q", derived, want)
		}
	}

	// Those derived names are the ones the cleanup removes, not the complement:
	// the live main course survives untouched.
	items := []string{"Meloso de pescado", "Arroz a banda", "Arroz del senyoret"}
	kept, removed, _ := splitItems(items, keys(derived...))
	if len(removed) != 2 {
		t.Fatalf("removed %v, want the 2 derived dishes", removed)
	}
	if len(kept) != 1 || kept[0] != "Meloso de pescado" {
		t.Fatalf("kept %v, want only the live main course", kept)
	}
}

func arrozCleanupCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func arrozCleanupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Ping(); err != nil {
		database.Close()
		t.Skipf("MySQL de test no disponible: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// arrozCleanupSeed creates one restaurant, one menu and an arroz section that
// owns two dishes, using the real DDL from 008a_group_menus_v2.sql. Children go
// before parents because of the foreign keys, and the cleanup reverses it.
func arrozCleanupSeed(t *testing.T, database *sql.DB, restaurant, menu int) {
	t.Helper()
	if _, err := database.Exec(arrozCleanupSectionsDDL[0]); err != nil {
		t.Skipf("no se puede preparar la tabla de secciones: %v", err)
	}
	if _, err := database.Exec(arrozCleanupSectionsDDL[1]); err != nil {
		t.Skipf("no se puede preparar la tabla de platos: %v", err)
	}
	ctx := arrozCleanupCtx(t)
	if _, err := database.ExecContext(ctx,
		`INSERT IGNORE INTO restaurants (id, name) VALUES (?, 'Arroz Cleanup Test')`, restaurant); err != nil {
		t.Skipf("no se puede sembrar el restaurante: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO menus (id, restaurant_id, menu_title, price, active) VALUES (?, ?, 'Arroces', 0, 1)`,
		menu, restaurant); err != nil {
		t.Skipf("no se puede sembrar el menu: %v", err)
	}
	sectionID := int64(menu) * 10
	if _, err := database.ExecContext(ctx,
		`INSERT INTO group_menu_sections_v2 (id, restaurant_id, menu_id, title, section_kind, position)
		 VALUES (?, ?, ?, 'Arroces', 'arroces', 0)`,
		sectionID, restaurant, menu); err != nil {
		t.Skipf("no se puede sembrar la seccion de arroces: %v", err)
	}
	for i, title := range []string{"Arroz a banda", "Arroz del senyoret"} {
		if _, err := database.ExecContext(ctx,
			`INSERT INTO group_menu_section_dishes_v2 (section_id, restaurant_id, menu_id, title_snapshot, position, active)
			 VALUES (?, ?, ?, ?, ?, 1)`,
			sectionID, restaurant, menu, title, i); err != nil {
			t.Skipf("no se puede sembrar el plato %q: %v", title, err)
		}
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, statement := range []string{
			`DELETE FROM group_menu_section_dishes_v2 WHERE restaurant_id = ?`,
			`DELETE FROM group_menu_sections_v2 WHERE restaurant_id = ?`,
			`DELETE FROM menus WHERE restaurant_id = ?`,
			`DELETE FROM restaurants WHERE id = ?`,
		} {
			arg := any(restaurant)
			if strings.Contains(statement, "restaurants WHERE id") {
				arg = restaurant
			}
			if _, err := database.ExecContext(c, statement, arg); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
			}
		}
	})
}

// The real DDL of the two tables the derived set reads, copied from
// internal/db/migrations/008a_group_menus_v2.sql so this test does not depend on
// the migrations having been applied to the test database.
var arrozCleanupSectionsDDL = []string{
	"CREATE TABLE IF NOT EXISTS `group_menu_sections_v2` (" +
		"`id` BIGINT NOT NULL AUTO_INCREMENT," +
		"`restaurant_id` INT NOT NULL," +
		"`menu_id` INT NOT NULL," +
		"`title` VARCHAR(255) NOT NULL," +
		"`section_kind` VARCHAR(64) NOT NULL DEFAULT 'custom'," +
		"`position` INT NOT NULL DEFAULT 0," +
		"`created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP," +
		"`updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
		"PRIMARY KEY (`id`)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",

	"CREATE TABLE IF NOT EXISTS `group_menu_section_dishes_v2` (" +
		"`id` BIGINT NOT NULL AUTO_INCREMENT," +
		"`restaurant_id` INT NOT NULL," +
		"`menu_id` INT NOT NULL," +
		"`section_id` BIGINT NOT NULL," +
		"`catalog_dish_id` BIGINT NULL," +
		"`title_snapshot` VARCHAR(255) NOT NULL," +
		"`description_snapshot` TEXT NULL," +
		"`allergens_json` JSON NULL," +
		"`supplement_enabled` TINYINT(1) NOT NULL DEFAULT 0," +
		"`supplement_price` DECIMAL(10,2) NULL," +
		"`active` TINYINT(1) NOT NULL DEFAULT 1," +
		"`position` INT NOT NULL DEFAULT 0," +
		"`created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP," +
		"`updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
		"PRIMARY KEY (`id`)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
}
