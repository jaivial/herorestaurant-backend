// Command comida-stock-backfill links every catalogue product (platos,
// bebidas, cafes, vinos, postres) to a stock item so it can be counted.
//
// Deliberate choices, because this writes to live catalogue data:
//   - --dry-run is the DEFAULT; --apply is required to write anything.
//   - Every product is created as RAW. No technical sheets exist yet, so
//     marking anything MANUFACTURED would claim a recipe nobody wrote.
//   - NO stock movements are created. Items start at zero and stay there until
//     a real count. Inventing opening balances would corrupt the ledger, which
//     is the system's source of truth.
//   - Idempotent via UNIQUE(restaurant_id, sku): re-running links the existing
//     item instead of creating a duplicate.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/db"
)

// catalogueSource describes one of the three product tables.
type catalogueSource struct {
	Name      string // "platos", "vinos", "postres", ...
	Table     string
	IDColumn  string
	NameExpr  string
	Predicate string
}

func catalogueSources() []catalogueSource {
	return []catalogueSource{
		// comida_items holds platos (including arroces), bebidas and cafes,
		// separated by source_type rather than by table.
		{Name: "platos", Table: "comida_items", IDColumn: "id",
			NameExpr: "COALESCE(NULLIF(titulo,''),nombre)", Predicate: "source_type='platos'"},
		{Name: "bebidas", Table: "comida_items", IDColumn: "id",
			NameExpr: "COALESCE(NULLIF(titulo,''),nombre)", Predicate: "source_type='bebidas'"},
		{Name: "cafes", Table: "comida_items", IDColumn: "id",
			NameExpr: "COALESCE(NULLIF(titulo,''),nombre)", Predicate: "source_type='cafes'"},
		{Name: "vinos", Table: "VINOS", IDColumn: "num", NameExpr: "nombre", Predicate: "1=1"},
		{Name: "postres", Table: "POSTRES", IDColumn: "NUM", NameExpr: "DESCRIPCION", Predicate: "1=1"},
	}
}

func main() {
	apply := flag.Bool("apply", false, "actually create and link stock items (default: report only)")
	flag.Parse()

	cfg := config.Load()
	database, err := db.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Two concurrent runs could both see a product as unlinked and create two
	// stock items for it.
	var locked int
	if err := database.QueryRowContext(ctx,
		`SELECT GET_LOCK('villacarmen:comida-stock-backfill',0)`).Scan(&locked); err != nil || locked != 1 {
		log.Print("backfill skipped: lock held")
		return
	}
	defer database.ExecContext(context.Background(),
		`SELECT RELEASE_LOCK('villacarmen:comida-stock-backfill')`)

	restaurants, err := tenantIDs(ctx, database)
	if err != nil {
		log.Fatal(err)
	}

	total := summary{}
	for _, restaurantID := range restaurants {
		for _, source := range catalogueSources() {
			s, err := backfillSource(ctx, database, restaurantID, source, *apply)
			if err != nil {
				log.Fatalf("restaurant %d / %s: %v", restaurantID, source.Name, err)
			}
			log.Printf("restaurant=%d source=%-8s %s", restaurantID, source.Name, s)
			total.Created += s.Created
			total.AlreadyLinked += s.AlreadyLinked
			total.Skipped += s.Skipped
			total.Failed += s.Failed
		}
	}

	mode := "DRY RUN (nothing written) - re-run with --apply to write"
	if *apply {
		mode = "APPLIED"
	}
	log.Printf("%s | %s", mode, total)
}

func tenantIDs(ctx context.Context, database *sql.DB) ([]int, error) {
	rows, err := database.QueryContext(ctx, `SELECT id FROM restaurants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type catalogueRow struct {
	ID     int64
	Name   string
	Linked bool
}

func loadCatalogueRows(ctx context.Context, database *sql.DB, restaurantID int, source catalogueSource) ([]catalogueRow, error) {
	// Table, column and predicate all come from the fixed list above, never
	// from user input, so interpolation here cannot be abused.
	query := fmt.Sprintf(
		`SELECT %s, COALESCE(%s,''), stock_item_id IS NOT NULL
		   FROM %s WHERE restaurant_id=? AND %s ORDER BY %s`,
		source.IDColumn, source.NameExpr, source.Table, source.Predicate, source.IDColumn)

	rows, err := database.QueryContext(ctx, query, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []catalogueRow
	for rows.Next() {
		var row catalogueRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Linked); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func backfillSource(ctx context.Context, database *sql.DB, restaurantID int,
	source catalogueSource, apply bool) (summary, error) {
	result := summary{}
	rows, err := loadCatalogueRows(ctx, database, restaurantID, source)
	if err != nil {
		return result, err
	}

	for _, row := range rows {
		if row.Linked {
			// Already pointing at a stock item; re-linking would risk pointing
			// it somewhere else.
			result.recordAlreadyLinked()
			continue
		}
		spec, ok := planProduct(source.Name, row.ID, row.Name)
		if !ok {
			result.recordSkipped()
			continue
		}
		if !apply {
			result.recordCreated()
			continue
		}
		if err := linkProduct(ctx, database, restaurantID, source, row.ID, spec); err != nil {
			log.Printf("  %s #%d (%s): %v", source.Name, row.ID, spec.Name, err)
			result.recordFailed()
			continue
		}
		result.recordCreated()
	}
	return result, nil
}

// linkProduct creates the stock item, its base unit and the product link in one
// transaction: a half-linked product would be invisible to stock while looking
// linked in the catalogue.
func linkProduct(ctx context.Context, database *sql.DB, restaurantID int,
	source catalogueSource, productID int64, spec stockItemSpec) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// An earlier interrupted run may already have created the item; the unique
	// sku is what makes that recoverable rather than duplicating.
	var itemID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM stock_items WHERE restaurant_id=? AND sku=?`, restaurantID, spec.SKU).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		res, insertErr := tx.ExecContext(ctx, `
			INSERT INTO stock_items
				(restaurant_id,sku,name,kind,base_dimension,base_unit,is_tracked,deduction_source)
			VALUES (?,?,?,?,?,?,1,?)`,
			restaurantID, spec.SKU, spec.Name, spec.Kind,
			spec.BaseDimension, spec.BaseUnit, spec.DeductionSource)
		if insertErr != nil {
			return insertErr
		}
		itemID, _ = res.LastInsertId()

		// Without a unit the item cannot be counted or used in a recipe.
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO stock_item_units
				(restaurant_id,stock_item_id,code,label,factor_to_base,is_default_display,can_recipe,can_count)
			VALUES (?,?,'ud','ud',1,1,1,1)`, restaurantID, itemID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	update := fmt.Sprintf(
		`UPDATE %s SET stock_item_id=? WHERE restaurant_id=? AND %s=?`,
		source.Table, source.IDColumn)
	if _, err = tx.ExecContext(ctx, update, itemID, restaurantID, productID); err != nil {
		return err
	}
	return tx.Commit()
}
