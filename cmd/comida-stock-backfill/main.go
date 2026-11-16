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
	"preactvillacarmen/internal/sqldb"
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
	// PG twins: quoted identifiers (uppercase source names) and PAD SPACE
	// NULLIF (MySQL NULLIF(x,'') also nulls a space-only value).
	TablePG, IDColumnPG, NameExprPG string
}

func catalogueSources() []catalogueSource {
	return []catalogueSource{
		// comida_items holds platos (including arroces), bebidas and cafes,
		// separated by source_type rather than by table.
		{Name: "platos", Table: "comida_items", IDColumn: "id",
			NameExpr: "COALESCE(NULLIF(titulo,''),nombre)", Predicate: "source_type='platos'",
			TablePG: "comida_items", IDColumnPG: "id", NameExprPG: pgPadNullIfTitulo},
		{Name: "bebidas", Table: "comida_items", IDColumn: "id",
			NameExpr: "COALESCE(NULLIF(titulo,''),nombre)", Predicate: "source_type='bebidas'",
			TablePG: "comida_items", IDColumnPG: "id", NameExprPG: pgPadNullIfTitulo},
		{Name: "cafes", Table: "comida_items", IDColumn: "id",
			NameExpr: "COALESCE(NULLIF(titulo,''),nombre)", Predicate: "source_type='cafes'",
			TablePG: "comida_items", IDColumnPG: "id", NameExprPG: pgPadNullIfTitulo},
		// SOURCE ANOMALY kept: VINOS is absent from the source schema (dump);
		// the query fails on both engines exactly as in the source.
		{Name: "vinos", Table: "VINOS", IDColumn: "num", NameExpr: "nombre", Predicate: "1=1",
			TablePG: `"VINOS"`, IDColumnPG: "num", NameExprPG: "nombre"},
		{Name: "postres", Table: "POSTRES", IDColumn: "NUM", NameExpr: "DESCRIPCION", Predicate: "1=1",
			TablePG: `"POSTRES"`, IDColumnPG: `"NUM"`, NameExprPG: `"DESCRIPCION"`},
	}
}

// pgPadNullIfTitulo is MySQL NULLIF(titulo,”) under PAD SPACE (a
// space-only titulo counts as empty) on PG.
const pgPadNullIfTitulo = "COALESCE(CASE WHEN rtrim(titulo, ' ') = '' THEN NULL ELSE titulo END,nombre)"

func main() {
	apply := flag.Bool("apply", false, "actually create and link stock items (default: report only)")
	flag.Parse()

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// db_dialect_selection_v1: MySQL unchanged; PG fails closed until
	// QA-approved and import-verified (no MySQL fallback, no migrations).
	database, d, err := db.OpenBusinessForTool(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	dialect = d
	if dialect == sqldb.Postgres && *apply {
		log.Fatal("comida-stock-backfill: --apply refused on PostgreSQL until runtime QA; run without --apply (report only)")
	}

	// Two concurrent runs could both see a product as unlinked and create two
	// stock items for it.
	// Session lock pinned to one connection (MySQL-only tool).
	release, locked, err := sqldb.TryNamedLock(ctx, dialect, database, "villacarmen:comida-stock-backfill")
	if err != nil || !locked {
		log.Printf("backfill skipped: lock held (err=%v)", err)
		return
	}
	defer release()

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
	rows, err := database.QueryContext(ctx, portableSQL(`SELECT id FROM restaurants ORDER BY id`))
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
	queryPG := fmt.Sprintf(
		`SELECT %s, COALESCE(%s,''), stock_item_id IS NOT NULL
		   FROM %s WHERE restaurant_id=? AND %s ORDER BY %s`,
		source.IDColumnPG, source.NameExprPG, source.TablePG, source.Predicate, source.IDColumnPG)

	// Fixed identifiers per dialect; rebound once.
	rows, err := database.QueryContext(ctx, nativeSQL(query, queryPG), restaurantID)
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
		nativeSQL(`SELECT id FROM stock_items WHERE restaurant_id=? AND sku=?`, `SELECT id FROM stock_items WHERE restaurant_id=? AND app.norm(sku) = app.norm(?)`), restaurantID, spec.SKU).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		// InsertID: MySQL LastInsertId unchanged; PG RETURNING id.
		var insertErr error
		itemID, insertErr = sqldb.InsertID(ctx, dialect, tx, portableSQL(`
			INSERT INTO stock_items
				(restaurant_id,sku,name,kind,base_dimension,base_unit,is_tracked,deduction_source)
			VALUES (?,?,?,?,?,?,1,?)`), "id",
			restaurantID, spec.SKU, spec.Name, spec.Kind,
			spec.BaseDimension, spec.BaseUnit, spec.DeductionSource)
		if insertErr != nil {
			return insertErr
		}

		// Without a unit the item cannot be counted or used in a recipe.
		if _, err = tx.ExecContext(ctx, portableSQL(`
			INSERT INTO stock_item_units
				(restaurant_id,stock_item_id,code,label,factor_to_base,is_default_display,can_recipe,can_count)
			VALUES (?,?,'ud','ud',1,1,1,1)`), restaurantID, itemID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	update := fmt.Sprintf(
		`UPDATE %s SET stock_item_id=? WHERE restaurant_id=? AND %s=?`,
		source.Table, source.IDColumn)
	updatePG := fmt.Sprintf(
		`UPDATE %s SET stock_item_id=? WHERE restaurant_id=? AND %s=?`,
		source.TablePG, source.IDColumnPG)
	// Fixed identifiers per dialect; rebound once.
	if _, err = tx.ExecContext(ctx, nativeSQL(update, updatePG), itemID, restaurantID, productID); err != nil {
		return err
	}
	return tx.Commit()
}
