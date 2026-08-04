package main

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func backfillTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`UPDATE comida_items SET stock_item_id=NULL WHERE restaurant_id=1`,
			`UPDATE VINOS SET stock_item_id=NULL WHERE restaurant_id=1`,
			`UPDATE POSTRES SET stock_item_id=NULL WHERE restaurant_id=1`,
			`DELETE FROM comida_items WHERE restaurant_id=1`,
			`DELETE FROM VINOS WHERE restaurant_id=1`,
			`DELETE FROM POSTRES WHERE restaurant_id=1`,
			`DELETE FROM stock_item_units WHERE restaurant_id=1`,
			`DELETE FROM stock_items WHERE restaurant_id=1`,
		} {
			if _, err := database.Exec(stmt); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		database.Close()
	})
	database.Exec(`INSERT IGNORE INTO restaurants(id,slug,name) VALUES(1,'t','T')`)
	return database
}

func seedCatalogue(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO comida_items (restaurant_id,source_type,nombre,titulo,active)
		 VALUES (1,'platos','Paella','Paella',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO VINOS (restaurant_id,tipo,nombre,precio,active) VALUES (1,'TINTO','Rioja',12.5,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO POSTRES (restaurant_id,DESCRIPCION,active) VALUES (1,'Flan',1)`); err != nil {
		t.Fatal(err)
	}
}

func countStockItems(t *testing.T, database *sql.DB) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM stock_items WHERE restaurant_id=1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func runAllSources(t *testing.T, database *sql.DB, apply bool) summary {
	t.Helper()
	total := summary{}
	for _, source := range catalogueSources() {
		s, err := backfillSource(context.Background(), database, 1, source, apply)
		if err != nil {
			t.Fatalf("%s: %v", source.Name, err)
		}
		total.Created += s.Created
		total.AlreadyLinked += s.AlreadyLinked
		total.Skipped += s.Skipped
		total.Failed += s.Failed
	}
	return total
}

// The whole point of the default mode: an operator can see what would happen
// without changing anything.
func TestDryRunWritesNothing(t *testing.T) {
	database := backfillTestDB(t)
	seedCatalogue(t, database)

	before := countStockItems(t, database)
	result := runAllSources(t, database, false)

	if result.Created != 3 {
		t.Fatalf("dry run planned %d items, want 3", result.Created)
	}
	if after := countStockItems(t, database); after != before {
		t.Fatalf("dry run created %d stock items; it must write nothing", after-before)
	}
}

func TestApplyLinksEveryProductAcrossAllThreeTables(t *testing.T) {
	database := backfillTestDB(t)
	seedCatalogue(t, database)

	if result := runAllSources(t, database, true); result.Created != 3 || result.Failed != 0 {
		t.Fatalf("created=%d failed=%d want 3/0", result.Created, result.Failed)
	}

	for _, q := range []string{
		`SELECT COUNT(*) FROM comida_items WHERE restaurant_id=1 AND stock_item_id IS NULL`,
		`SELECT COUNT(*) FROM VINOS WHERE restaurant_id=1 AND stock_item_id IS NULL`,
		`SELECT COUNT(*) FROM POSTRES WHERE restaurant_id=1 AND stock_item_id IS NULL`,
	} {
		var unlinked int
		database.QueryRow(q).Scan(&unlinked)
		if unlinked != 0 {
			t.Fatalf("%d products left unlinked by %q", unlinked, q)
		}
	}

	// Every item must be countable, which requires a unit.
	var itemsWithoutUnit int
	database.QueryRow(`SELECT COUNT(*) FROM stock_items i
		WHERE i.restaurant_id=1 AND NOT EXISTS (
			SELECT 1 FROM stock_item_units u WHERE u.restaurant_id=1 AND u.stock_item_id=i.id)`).
		Scan(&itemsWithoutUnit)
	if itemsWithoutUnit != 0 {
		t.Fatalf("%d stock items have no unit and cannot be counted", itemsWithoutUnit)
	}
}

// Re-running after a partial failure, or simply twice, must not double the
// catalogue.
func TestApplyIsIdempotent(t *testing.T) {
	database := backfillTestDB(t)
	seedCatalogue(t, database)

	runAllSources(t, database, true)
	afterFirst := countStockItems(t, database)

	second := runAllSources(t, database, true)
	if second.Created != 0 {
		t.Fatalf("second run created %d items; it must create none", second.Created)
	}
	if second.AlreadyLinked != 3 {
		t.Fatalf("second run reported %d already linked, want 3", second.AlreadyLinked)
	}
	if afterSecond := countStockItems(t, database); afterSecond != afterFirst {
		t.Fatalf("stock items went from %d to %d on re-run", afterFirst, afterSecond)
	}
}

// Stock quantities must come from a real count, never from a guess made by
// this command.
func TestBackfillNeverCreatesStockMovements(t *testing.T) {
	database := backfillTestDB(t)
	seedCatalogue(t, database)

	var before int
	database.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=1`).Scan(&before)
	runAllSources(t, database, true)
	var after int
	database.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=1`).Scan(&after)

	if after != before {
		t.Fatalf("backfill created %d stock movements; opening balances must come from a real count", after-before)
	}
}

// Products stay RAW: no technical sheets exist yet.
func TestBackfilledProductsRemainRaw(t *testing.T) {
	database := backfillTestDB(t)
	seedCatalogue(t, database)
	runAllSources(t, database, true)

	var manufactured int
	database.QueryRow(`SELECT
		(SELECT COUNT(*) FROM comida_items WHERE restaurant_id=1 AND production_type<>'RAW') +
		(SELECT COUNT(*) FROM VINOS WHERE restaurant_id=1 AND production_type<>'RAW') +
		(SELECT COUNT(*) FROM POSTRES WHERE restaurant_id=1 AND production_type<>'RAW')`).Scan(&manufactured)
	if manufactured != 0 {
		t.Fatalf("%d products were marked manufactured without a technical sheet", manufactured)
	}

	var nonRawItems int
	database.QueryRow(`SELECT COUNT(*) FROM stock_items WHERE restaurant_id=1 AND kind<>'RAW'`).Scan(&nonRawItems)
	if nonRawItems != 0 {
		t.Fatalf("%d backfilled stock items are not RAW", nonRawItems)
	}
}

// A product already pointing at a stock item must be left alone: re-pointing it
// would silently move its history.
func TestAlreadyLinkedProductIsNotTouched(t *testing.T) {
	database := backfillTestDB(t)
	seedCatalogue(t, database)

	res, err := database.Exec(`INSERT INTO stock_items
		(restaurant_id,sku,name,kind,base_dimension,base_unit,is_tracked,deduction_source)
		VALUES (1,'manual-existing','Existente','RAW','COUNT','ud',1,'SALE')`)
	if err != nil {
		t.Fatal(err)
	}
	existingID, _ := res.LastInsertId()
	if _, err := database.Exec(
		`UPDATE comida_items SET stock_item_id=? WHERE restaurant_id=1`, existingID); err != nil {
		t.Fatal(err)
	}

	runAllSources(t, database, true)

	var linked int64
	database.QueryRow(`SELECT stock_item_id FROM comida_items WHERE restaurant_id=1 LIMIT 1`).Scan(&linked)
	if linked != existingID {
		t.Fatalf("existing link was changed from %d to %d", existingID, linked)
	}
}
