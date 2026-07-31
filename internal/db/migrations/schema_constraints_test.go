package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// These tests assert that the guarantees claimed by migrations 069-072 are
// enforced by the DATABASE, not by application code. They run against a real
// MySQL schema clone; set MIGRATIONS_TEST_MYSQL_DSN to enable them.
//
// The DSN must point at a THROWAWAY database: the tests create and drop rows.
func migrationsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedRecipeOutput creates a tenant-scoped stock item usable as a recipe output.
func seedRecipeOutput(t *testing.T, db *sql.DB) (restaurantID int64, itemID int64) {
	t.Helper()
	ctx := context.Background()
	if err := db.QueryRow(`SELECT id FROM restaurants LIMIT 1`).Scan(&restaurantID); err != nil {
		t.Fatalf("need at least one restaurant: %v", err)
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO stock_categories (restaurant_id,name,sort_order) VALUES (?,?,1)`,
		restaurantID, "migtest-cat")
	if err != nil {
		t.Fatal(err)
	}
	catID, _ := res.LastInsertId()
	res, err = db.ExecContext(ctx,
		`INSERT INTO stock_items (restaurant_id,category_id,name,kind,base_unit,base_dimension,deduction_source,is_tracked)
		 VALUES (?,?,?,'SEMI_FINISHED','ud','COUNT','SALE',1)`,
		restaurantID, catID, "migtest-output")
	if err != nil {
		t.Fatal(err)
	}
	itemID, _ = res.LastInsertId()
	t.Cleanup(func() {
		db.Exec(`DELETE FROM stock_recipes WHERE restaurant_id=? AND output_item_id=?`, restaurantID, itemID)
		db.Exec(`DELETE FROM stock_items WHERE id=?`, itemID)
		db.Exec(`DELETE FROM stock_categories WHERE id=?`, catID)
	})
	return restaurantID, itemID
}

func insertRecipe(db *sql.DB, restaurantID, itemID int64, name, status string) error {
	_, err := db.Exec(
		`INSERT INTO stock_recipes (restaurant_id,name,output_item_id,output_qty_base,status)
		 VALUES (?,?,?,1,?)`,
		restaurantID, name, itemID, status)
	return err
}

// Migration 070: DRAFT sheets must not reserve the output slot, or a user could
// never start a second draft for a dish that already has one in progress.
func TestDraftRecipesMayShareAnOutputItem(t *testing.T) {
	db := migrationsTestDB(t)
	restaurantID, itemID := seedRecipeOutput(t, db)

	if err := insertRecipe(db, restaurantID, itemID, "migtest-draft-1", "DRAFT"); err != nil {
		t.Fatalf("first draft rejected: %v", err)
	}
	if err := insertRecipe(db, restaurantID, itemID, "migtest-draft-2", "DRAFT"); err != nil {
		t.Fatalf("second draft for the same output must be allowed: %v", err)
	}
}

// ...but at most one ACTIVE recipe may own an output item, or stock deduction
// would be ambiguous.
func TestOnlyOneActiveRecipePerOutputItem(t *testing.T) {
	db := migrationsTestDB(t)
	restaurantID, itemID := seedRecipeOutput(t, db)

	if err := insertRecipe(db, restaurantID, itemID, "migtest-active-1", "ACTIVE"); err != nil {
		t.Fatalf("first active recipe rejected: %v", err)
	}
	err := insertRecipe(db, restaurantID, itemID, "migtest-active-2", "ACTIVE")
	if err == nil {
		t.Fatal("a second ACTIVE recipe for the same output must be rejected by the database")
	}
	if !strings.Contains(err.Error(), "uq_stock_recipe_output") {
		t.Fatalf("expected uq_stock_recipe_output violation, got: %v", err)
	}
}

// Migration 072: the whole reason for the new table. The old flat table keyed on
// a NULLable category_id, and MySQL treats NULLs as distinct, so two GLOBAL rows
// were both accepted. scope_key is NOT NULL, making the unique key total.
func TestOnlyOneGlobalMarginScopePerTenant(t *testing.T) {
	db := migrationsTestDB(t)
	var restaurantID int64
	if err := db.QueryRow(`SELECT id FROM restaurants LIMIT 1`).Scan(&restaurantID); err != nil {
		t.Fatalf("need at least one restaurant: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM stock_margin_scopes WHERE restaurant_id=? AND label LIKE 'migtest%'`, restaurantID)
	})

	insert := func(label string) error {
		_, err := db.Exec(
			`INSERT INTO stock_margin_scopes (restaurant_id,scope_kind,scope_key,label)
			 VALUES (?,'GLOBAL','*',?)`, restaurantID, label)
		return err
	}
	if err := insert("migtest-global"); err != nil {
		t.Fatalf("first global scope rejected: %v", err)
	}
	err := insert("migtest-global-dup")
	if err == nil {
		t.Fatal("a second GLOBAL scope must be rejected by the database, not by application code")
	}
	if !strings.Contains(err.Error(), "uq_stock_margin_scope") {
		t.Fatalf("expected uq_stock_margin_scope violation, got: %v", err)
	}
}

// Plato and bebida categories live in SEPARATE tables, each with its own INT
// auto-increment, so id=3 exists in both and means different things. scope_key
// is type-qualified precisely so they cannot be conflated.
func TestComidaCategoryScopesAreTypeQualified(t *testing.T) {
	db := migrationsTestDB(t)
	var restaurantID int64
	if err := db.QueryRow(`SELECT id FROM restaurants LIMIT 1`).Scan(&restaurantID); err != nil {
		t.Fatalf("need at least one restaurant: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM stock_margin_scopes WHERE restaurant_id=? AND label LIKE 'migtest%'`, restaurantID)
	})

	for _, key := range []string{"platos:3", "bebidas:3"} {
		_, err := db.Exec(
			`INSERT INTO stock_margin_scopes (restaurant_id,scope_kind,scope_key,label)
			 VALUES (?,'COMIDA_CATEGORY',?,?)`, restaurantID, key, "migtest-"+key)
		if err != nil {
			t.Fatalf("scope %q must be distinct from the other food type: %v", key, err)
		}
	}
}

func TestMarginScopeBandConstraints(t *testing.T) {
	db := migrationsTestDB(t)
	var restaurantID int64
	if err := db.QueryRow(`SELECT id FROM restaurants LIMIT 1`).Scan(&restaurantID); err != nil {
		t.Fatalf("need at least one restaurant: %v", err)
	}
	res, err := db.Exec(
		`INSERT INTO stock_margin_scopes (restaurant_id,scope_kind,scope_key,label)
		 VALUES (?,'COMIDA_TYPE','migtest-type','migtest-bands')`, restaurantID)
	if err != nil {
		t.Fatal(err)
	}
	scopeID, _ := res.LastInsertId()
	t.Cleanup(func() {
		db.Exec(`DELETE FROM stock_margin_scopes WHERE id=?`, scopeID)
	})

	addBand := func(zone string, min, max interface{}) error {
		_, err := db.Exec(
			`INSERT INTO stock_margin_scope_bands (restaurant_id,scope_id,zone,min_food_cost_pct,max_food_cost_pct)
			 VALUES (?,?,?,?,?)`, restaurantID, scopeID, zone, min, max)
		return err
	}

	t.Run("accepts the four default zones", func(t *testing.T) {
		if err := addBand("PURPLE", nil, 25.0); err != nil {
			t.Fatal(err)
		}
		if err := addBand("GREEN", 25.0, 35.0); err != nil {
			t.Fatal(err)
		}
		if err := addBand("AMBER", 35.0, 40.0); err != nil {
			t.Fatal(err)
		}
		if err := addBand("RED", 40.0, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rejects a duplicate zone in the same scope", func(t *testing.T) {
		err := addBand("AMBER", 50.0, 60.0)
		if err == nil {
			t.Fatal("duplicate zone must be rejected")
		}
		if !strings.Contains(err.Error(), "uq_stock_margin_scope_band") {
			t.Fatalf("expected unique key violation, got: %v", err)
		}
	})

	t.Run("rejects an inverted range", func(t *testing.T) {
		_, err := db.Exec(
			`INSERT INTO stock_margin_scope_bands (restaurant_id,scope_id,zone,min_food_cost_pct,max_food_cost_pct)
			 VALUES (?,?, 'RED', 40, 30)`, restaurantID, scopeID)
		if err == nil {
			t.Fatal("min >= max must be rejected")
		}
		if !strings.Contains(err.Error(), "chk_stock_margin_scope_band_range") {
			t.Fatalf("expected range check violation, got: %v", err)
		}
	})

	t.Run("deleting the scope cascades its bands", func(t *testing.T) {
		if _, err := db.Exec(`DELETE FROM stock_margin_scopes WHERE id=?`, scopeID); err != nil {
			t.Fatal(err)
		}
		var remaining int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM stock_margin_scope_bands WHERE scope_id=?`, scopeID).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("expected bands to cascade, %d remained", remaining)
		}
	})
}

func TestMarginScopeTargetMustBeAPercentage(t *testing.T) {
	db := migrationsTestDB(t)
	var restaurantID int64
	if err := db.QueryRow(`SELECT id FROM restaurants LIMIT 1`).Scan(&restaurantID); err != nil {
		t.Fatalf("need at least one restaurant: %v", err)
	}
	_, err := db.Exec(
		`INSERT INTO stock_margin_scopes (restaurant_id,scope_kind,scope_key,label,target_food_cost_pct)
		 VALUES (?,'COMIDA_TYPE','migtest-bad','migtest-bad',150)`, restaurantID)
	if err == nil {
		db.Exec(`DELETE FROM stock_margin_scopes WHERE restaurant_id=? AND scope_key='migtest-bad'`, restaurantID)
		t.Fatal("a target above 100% must be rejected")
	}
	if !strings.Contains(err.Error(), "chk_stock_margin_scope_target") {
		t.Fatalf("expected target check violation, got: %v", err)
	}
}

// The old flat table must be gone, so no code can accidentally keep using it.
func TestLegacyFlatMarginBandTableIsRetired(t *testing.T) {
	db := migrationsTestDB(t)
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'stock_margin_bands'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stock_margin_bands should have been dropped by migration 072")
	}
}

// POSTRES is a legacy table that never received migration 069, so it had no
// production/stock columns at all. Without them, postres cannot be linked to
// stock like every other product type.
func TestPostresHasProductionAndStockColumns(t *testing.T) {
	db := migrationsTestDB(t)

	for _, column := range []string{"production_type", "stock_item_id", "stock_recipe_id"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='POSTRES' AND COLUMN_NAME=?`, column).
			Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("POSTRES.%s missing; postres cannot be linked to stock", column)
		}
	}

	// A dessert with no technical sheet is raw, and existing rows must not be
	// silently reclassified as manufactured by the upgrade.
	var defaultValue string
	if err := db.QueryRow(`SELECT COLUMN_DEFAULT FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='POSTRES' AND COLUMN_NAME='production_type'`).
		Scan(&defaultValue); err != nil {
		t.Fatal(err)
	}
	if defaultValue != "RAW" {
		t.Fatalf("production_type default is %q, want RAW", defaultValue)
	}
}
