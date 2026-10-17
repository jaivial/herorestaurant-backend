// Command group-menu-arroz-cleanup removes the arroz names that were stored as
// plain text inside menus.principales.items.
//
// GET /api/admin/group-menus/{id} merges the arroz into principales.items so the
// booking can offer them (coordination id booking_principales_include_arroz_v1).
// Those merged names are text only: no price, no allergens, no dish id. A save
// used to store them, after which they stayed forever, never followed a rename
// in the catalogue and could never carry the dish data the booking needs. The
// server no longer persists them (boGroupMenuPrincipalesForPersist); this
// command clears out what earlier saves already left behind.
//
// Deliberate choices, because this writes to live menu data:
//   - --dry-run is the DEFAULT; --apply is required to write anything.
//   - A stored name is only deleted when the read path re-merges it on every read
//     (boGroupMenuDerivedArrozNames), because then removing it changes nothing
//     anybody can observe. That is the same set the server refuses to persist
//     (boGroupMenuPrincipalesForPersist), so the cleanup and the write path now
//     agree on what a stored arroz is.
//   - A name nothing derives is NEVER deleted, however much it looks like a rice:
//     it is the only copy of that dish, and the row is what the public menu and
//     the booking read. Such names are reported instead, for a human to judge.
//   - The original principals column is written to a JSON backup before the
//     first UPDATE, so a wrong run can be undone by hand.
//   - --restaurant and --menu narrow the blast radius.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/db"
)

// arrozShaped matches the vocabulary the restaurant actually uses for rice. It
// no longer decides what gets deleted, only what gets reported: a false positive
// here used to delete a main course. "Gallineta al horno con verduras salteadas
// y arroz aromático" is a chicken that merely mentions rice in its description,
// and "Meloso de pescado y marisco" is a rice dish whose name never says so.
var arrozShaped = regexp.MustCompile(`(?i)\b(arroz|paella|fideu|arrozes|meloso)`)

type menuRow struct {
	RestaurantID int
	MenuID       int
	MenuTitle    string
	Principales  sql.NullString
}

func main() {
	apply := flag.Bool("apply", false, "actually write the cleaned principals (default: report only)")
	restaurant := flag.Int("restaurant", 0, "only this restaurant id (default: every restaurant)")
	menu := flag.Int("menu", 0, "only this menu id (default: every menu)")
	backup := flag.String("backup", "arroz-cleanup-backup.json", "where to write the original columns before applying")
	flag.Parse()

	cfg := config.Load()
	database, err := db.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, err := loadMenus(ctx, database, *restaurant, *menu)
	if err != nil {
		log.Fatal(err)
	}

	type change struct {
		RestaurantID int      `json:"restaurant_id"`
		MenuID       int      `json:"menu_id"`
		MenuTitle    string   `json:"menu_title"`
		Before       []string `json:"before"`
		After        []string `json:"after"`
		Removed      []string `json:"removed"`
	}
	var (
		changed  []change
		backups  []map[string]any
		skipped  int
		examined int
	)

	for _, row := range rows {
		if strings.TrimSpace(row.Principales.String) == "" {
			skipped++
			continue
		}
		var block map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row.Principales.String), &block); err != nil {
			log.Printf("menu %d: principales is not a JSON object, left alone (%v)", row.MenuID, err)
			skipped++
			continue
		}
		rawItems, ok := block["items"]
		if !ok {
			skipped++
			continue
		}
		var items []string
		if err := json.Unmarshal(rawItems, &items); err != nil {
			log.Printf("menu %d: principales.items is not a string list, left alone (%v)", row.MenuID, err)
			skipped++
			continue
		}
		examined++

		derived, err := derivedArrozNames(ctx, database, row.RestaurantID, row.MenuID)
		if err != nil {
			log.Fatalf("menu %d: cannot resolve the derived arroz: %v", row.MenuID, err)
		}
		derivedKeys := make(map[string]bool, len(derived))
		for _, name := range derived {
			derivedKeys[key(name)] = true
		}

		kept, removed, suspicious := splitItems(items, derivedKeys)
		for _, item := range suspicious {
			fmt.Printf("  OJO, no derivado (NO se borra): %s\n", item)
		}
		if len(removed) == 0 {
			skipped++
			continue
		}

		changed = append(changed, change{
			RestaurantID: row.RestaurantID, MenuID: row.MenuID, MenuTitle: row.MenuTitle,
			Before: items, After: kept, Removed: removed,
		})
		backups = append(backups, map[string]any{
			"restaurant_id": row.RestaurantID,
			"menu_id":       row.MenuID,
			"principales":   row.Principales.String,
		})

		fmt.Printf("\nmenu %d  restaurant %d  %q\n", row.MenuID, row.RestaurantID, row.MenuTitle)
		fmt.Printf("  antes  (%d):\n", len(items))
		for _, item := range items {
			mark := ""
			if arrozShaped.MatchString(item) {
				mark = "  <== arroz"
			}
			fmt.Printf("    - %s%s\n", item, mark)
		}
		fmt.Printf("  despues (%d):\n", len(kept))
		for _, item := range kept {
			fmt.Printf("    - %s\n", item)
		}
		fmt.Printf("  se quitan: %s\n", strings.Join(removed, ", "))
	}

	if len(changed) == 0 {
		fmt.Printf("\nnada que limpiar: %d menus con items, %d sin items que tocar\n", examined, skipped)
		return
	}

	mode := "DRY RUN (nada escrito) - repite con --apply para escribir"
	if *apply {
		mode = "APLICADO"
	}
	fmt.Printf("\n%d menus afectados | %s\n", len(changed), mode)

	if !*apply {
		return
	}

	payload, err := json.MarshalIndent(backups, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*backup, payload, 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("backup escrito en %s\n", *backup)

	// Re-check each row inside the write loop: if someone saved the menu between
	// the read and now, the column no longer matches what we planned against.
	for _, row := range rows {
		var current sql.NullString
		if err := database.QueryRowContext(ctx,
			`SELECT principales FROM menus WHERE id = ? AND restaurant_id = ?`,
			row.MenuID, row.RestaurantID,
		).Scan(&current); err != nil {
			log.Fatalf("menu %d: re-read before write failed: %v", row.MenuID, err)
		}
		if strings.TrimSpace(current.String) != strings.TrimSpace(row.Principales.String) {
			log.Printf("menu %d: guardado por alguien mientras corria el cleanup, se omite", row.MenuID)
			continue
		}
		var block map[string]json.RawMessage
		if err := json.Unmarshal([]byte(current.String), &block); err != nil {
			log.Printf("menu %d: ya no es un objeto JSON, se omite", row.MenuID)
			continue
		}
		var items []string
		if err := json.Unmarshal(block["items"], &items); err != nil {
			log.Printf("menu %d: items ya no es una lista, se omite", row.MenuID)
			continue
		}
		derived, err := derivedArrozNames(ctx, database, row.RestaurantID, row.MenuID)
		if err != nil {
			log.Fatalf("menu %d: %v", row.MenuID, err)
		}
		derivedKeys := make(map[string]bool, len(derived))
		for _, name := range derived {
			derivedKeys[key(name)] = true
		}
		kept, _, _ := splitItems(items, derivedKeys)
		if len(kept) == len(items) {
			continue
		}
		cleaned, err := json.Marshal(kept)
		if err != nil {
			log.Fatal(err)
		}
		block["items"] = cleaned
		updated, err := json.Marshal(block)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := database.ExecContext(ctx,
			`UPDATE menus SET principales = ? WHERE id = ? AND restaurant_id = ?`,
			string(updated), row.MenuID, row.RestaurantID,
		); err != nil {
			log.Fatalf("menu %d: update: %v", row.MenuID, err)
		}
		fmt.Printf("menu %d: %d -> %d items\n", row.MenuID, len(items), len(kept))
	}
}

// splitItems divides the stored principals into what may be deleted and what
// must stay.
//
// The delete rule is the one the write path already uses
// (boGroupMenuPrincipalesForPersist): drop a name exactly when the read path
// re-merges it on every read, because then the row carries a copy of something
// the menu already owns and deleting it is invisible to everybody.
//
// The old rule was the complement — "looks like a rice AND nothing derives it"
// — which is backwards. Nothing-derives is the only proof that the row is the
// single copy of that dish, so that rule deleted the live rice of the "Menú
// Arrocero" and a gallineta whose description merely said "arroz aromático".
// It also could not tell a menu with no arroz section (derived empty for every
// name) from a genuinely stale one, so it emptied all of them.
//
// The third return value is reported and never deleted: an arroz-shaped name
// that nothing derives. On its own that is a judgement call, not evidence.
func splitItems(items []string, derivedKeys map[string]bool) (kept, removed, suspicious []string) {
	kept = make([]string, 0, len(items))
	removed = make([]string, 0, 4)
	for _, item := range items {
		if derivedKeys[key(item)] {
			removed = append(removed, item)
			continue
		}
		kept = append(kept, item)
		if arrozShaped.MatchString(item) {
			suspicious = append(suspicious, item)
		}
	}
	return kept, removed, suspicious
}

// derivedArrozNames mirrors what the read path merges in, so the cleanup never
// removes a name the booking would just merge back anyway.
func derivedArrozNames(ctx context.Context, database *sql.DB, restaurantID, menuID int) ([]string, error) {
	out := make([]string, 0, 16)
	seen := map[string]bool{}
	add := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			if name = strings.TrimSpace(name); name == "" || seen[key(name)] {
				continue
			}
			seen[key(name)] = true
			out = append(out, name)
		}
		return rows.Err()
	}

	sectionRows, err := database.QueryContext(ctx, `
		SELECT d.title_snapshot
		FROM group_menu_sections_v2 sec
		JOIN group_menu_section_dishes_v2 d
		  ON d.section_id = sec.id AND d.restaurant_id = sec.restaurant_id
		WHERE sec.restaurant_id = ? AND sec.menu_id = ? AND d.active = 1
		  AND sec.section_kind IN ('arroces', 'rice')
		ORDER BY d.position ASC, d.id ASC
	`, restaurantID, menuID)
	if err != nil {
		return nil, err
	}
	if err := add(sectionRows); err != nil {
		return nil, err
	}

	catalogRows, err := database.QueryContext(ctx, `
		SELECT nombre FROM comida_items
		WHERE restaurant_id = ? AND active = 1
		  AND (UPPER(TRIM(COALESCE(tipo, ''))) = 'ARROZ'
		       OR LOWER(TRIM(COALESCE(categoria, ''))) LIKE 'arroz%')
		ORDER BY nombre ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	if err := add(catalogRows); err != nil {
		return nil, err
	}
	return out, nil
}

func key(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func loadMenus(ctx context.Context, database *sql.DB, restaurantID, menuID int) ([]menuRow, error) {
	query := `SELECT restaurant_id, id, menu_title, principales FROM menus WHERE 1=1`
	args := make([]any, 0, 2)
	if restaurantID > 0 {
		query += ` AND restaurant_id = ?`
		args = append(args, restaurantID)
	}
	if menuID > 0 {
		query += ` AND id = ?`
		args = append(args, menuID)
	}
	query += ` ORDER BY restaurant_id, id`

	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []menuRow
	for rows.Next() {
		var row menuRow
		if err := rows.Scan(&row.RestaurantID, &row.MenuID, &row.MenuTitle, &row.Principales); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
