// Command graph-ingest builds ONE restaurant's knowledge graph from the
// PostgreSQL business database into that restaurant's own neo4j-rust
// instance (coordination id: graphrag_ingest_v1).
//
//	DB_DRIVER=postgres PG_DSN=... graph-ingest -restaurant-id N [-apply] [-resync] [-registry FILE]
//
// Dry-run (default): reads PG, prints counts per kind, validates the registry
// (0600, no shared port), the token file (0600) and the loopback binding of the
// instance, writes nothing. -apply upserts nodes by stable uid
// (restaurant:<id>:<kind>:<source id>) and rebuilds that restaurant's edges;
// -resync also deletes that restaurant's nodes no longer in PG. Only the
// registry instance of this restaurant is ever contacted. PG is read in a READ
// ONLY transaction, every query filtered by restaurant_id = N. The DSN and the
// token are never printed.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"preactvillacarmen/internal/graphrag"
)

// node is one graph document.
type node struct {
	UID, Kind, Label, Title, Body string
	Price                         string
	Parent                        string // uid of the container (edge Parent -[Rel]-> node)
	Rel                           string
}

func main() {
	rid := flag.Int("restaurant-id", 0, "restaurant id (required)")
	reg := flag.String("registry", graphrag.RegistryPath(), "server-side registry")
	apply := flag.Bool("apply", false, "write to the graph (default dry-run)")
	resync := flag.Bool("resync", false, "with -apply: delete this restaurant's nodes not present in PG")
	query := flag.String("query", "", "manual QA: run the bot retrieval (graphrag.Query, VC_GRAPHRAG=1) for this restaurant and print uid/kind/title of the hits")
	flag.Parse()
	if *rid <= 0 {
		fail("refusing: -restaurant-id is required")
	}
	if *query != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		hits := graphrag.Query(ctx, *rid, *query, 10)
		fmt.Printf("query: restaurant=%d text_len=%d hits=%d enabled=%v\n", *rid, len(*query), len(hits), graphrag.Enabled())
		for _, h := range hits {
			fmt.Printf("hit uid=%s kind=%s score=%d title=%q\n", h.UID, h.Kind, h.Score, h.Title)
		}
		return
	}
	if os.Getenv("DB_DRIVER") != "postgres" || os.Getenv("PG_DSN") == "" {
		fail("refusing: needs DB_DRIVER=postgres and PG_DSN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	in, err := graphrag.Lookup(*reg, *rid)
	if err != nil {
		fail("registry: " + err.Error())
	}
	bind, err := in.LoopbackOnly()
	if err != nil {
		fail("refusing: " + err.Error())
	}
	fmt.Printf("registry ok: restaurant=%d port=%d token_file=0600 bind=%s\n", *rid, in.Port, bind)

	nodes, err := readPG(ctx, *rid)
	if err != nil {
		fail("postgres: " + err.Error())
	}
	counts := map[string]int{}
	for _, n := range nodes {
		counts[n.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Printf("kind=%s nodes=%d\n", k, counts[k])
	}
	if !*apply {
		fmt.Printf("dry-run: restaurant=%d nodes=%d (nothing written)\n", *rid, len(nodes))
		return
	}
	if bind != "loopback" {
		fail("refusing -apply: instance is not listening on loopback")
	}
	created, updated, deleted, edges, err := sync(ctx, in, *rid, nodes, *resync)
	if err != nil {
		fail("graph: " + err.Error())
	}
	fmt.Printf("applied: restaurant=%d created=%d updated=%d unchanged=%d deleted=%d edges=%d\n", *rid, created, updated, len(nodes)-created-updated, deleted, edges)
}

func fail(m string) { fmt.Fprintln(os.Stderr, m); os.Exit(1) }

// --- PostgreSQL (read-only, one restaurant) -------------------------------

func readPG(ctx context.Context, rid int) ([]node, error) {
	db, err := sql.Open("pgx", os.Getenv("PG_DSN"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // read-only; never committed
	ru := uid(rid, "restaurant", strconv.Itoa(rid))
	var out []node
	add := func(n node) { out = append(out, n) }

	var name, slug, loc, phone, email, web, menu sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT name, slug, location, contact_phone, contact_email, website_url, menu_url FROM restaurants WHERE id = $1`, rid).
		Scan(&name, &slug, &loc, &phone, &email, &web, &menu); err != nil {
		return nil, fmt.Errorf("restaurant %d: %w", rid, err)
	}
	body := join("Ubicación: "+loc.String, "Teléfono: "+phone.String, "Email: "+email.String, "Web: "+web.String, "Carta: "+menu.String)
	var dir, tel, mail, site sql.NullString
	if tx.QueryRowContext(ctx, `SELECT direccion, telefono, email, website FROM restaurant_info WHERE restaurant_id = $1`, rid).Scan(&dir, &tel, &mail, &site) == nil {
		body = join(body, "Dirección: "+dir.String, "Teléfono: "+tel.String, "Email: "+mail.String, "Web: "+site.String)
	}
	add(node{UID: ru, Kind: "restaurant", Label: "Restaurant", Title: name.String, Body: body})

	q := func(query string, scan func(*sql.Rows) error) error {
		rows, err := tx.QueryContext(ctx, query, rid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	if err := q(`SELECT id, food_type, name FROM comida_categories WHERE restaurant_id = $1 AND active = 1`, func(r *sql.Rows) error {
		var id int64
		var ft, nm sql.NullString
		if err := r.Scan(&id, &ft, &nm); err != nil {
			return err
		}
		add(node{UID: uid(rid, "category", strconv.FormatInt(id, 10)), Kind: "category", Label: "Category", Title: nm.String, Body: "Categoría de " + ft.String, Parent: ru, Rel: "HAS_CATEGORY"})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("comida_categories: %w", err)
	}
	if err := q(`SELECT id, source_type, nombre, titulo, categoria, category_id, CAST(precio AS text), CAST(suplemento AS text), descripcion, CAST(alergenos_json AS text)
		FROM comida_items WHERE restaurant_id = $1 AND active = 1`, func(r *sql.Rows) error {
		var id int64
		var cat sql.NullInt64
		var st, nm, ti, catName, price, sup, desc, alg sql.NullString
		if err := r.Scan(&id, &st, &nm, &ti, &catName, &cat, &price, &sup, &desc, &alg); err != nil {
			return err
		}
		parent, rel := ru, "OFFERS"
		if cat.Valid {
			parent, rel = uid(rid, "category", strconv.FormatInt(cat.Int64, 10)), "HAS_ITEM"
		}
		title := firstNonEmpty(ti.String, nm.String)
		add(node{UID: uid(rid, "dish", strconv.FormatInt(id, 10)), Kind: "dish", Label: "Dish", Title: title,
			Body:  join(nm.String, desc.String, "Tipo: "+st.String, "Categoría: "+catName.String, "Precio: "+price.String, "Suplemento: "+sup.String, "Alérgenos: "+alg.String),
			Price: price.String, Parent: parent, Rel: rel})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("comida_items: %w", err)
	}
	if err := q(`SELECT "NUM", "DESCRIPCION", alergenos FROM "POSTRES" WHERE restaurant_id = $1 AND active = 1`, func(r *sql.Rows) error {
		var id int64
		var d, alg sql.NullString
		if err := r.Scan(&id, &d, &alg); err != nil {
			return err
		}
		add(node{UID: uid(rid, "dessert", strconv.FormatInt(id, 10)), Kind: "dessert", Label: "Dessert", Title: d.String, Body: join(d.String, "Alérgenos: "+alg.String), Parent: ru, Rel: "OFFERS"})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("POSTRES: %w", err)
	}
	if err := q(`SELECT num, tipo, nombre, titulo, CAST(precio AS text), descripcion, CAST(alergenos AS text) FROM "BEBIDAS" WHERE restaurant_id = $1 AND active = 1`, func(r *sql.Rows) error {
		var id int64
		var tipo, nm, ti, price, desc, alg sql.NullString
		if err := r.Scan(&id, &tipo, &nm, &ti, &price, &desc, &alg); err != nil {
			return err
		}
		add(node{UID: uid(rid, "drink", strconv.FormatInt(id, 10)), Kind: "drink", Label: "Drink", Title: firstNonEmpty(ti.String, nm.String),
			Body: join(nm.String, desc.String, "Tipo: "+tipo.String, "Precio: "+price.String, "Alérgenos: "+alg.String), Price: price.String, Parent: ru, Rel: "OFFERS"})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("BEBIDAS: %w", err)
	}
	// Wines: the source has no VINOS table; wines live in group_menu_wines_v2.
	if err := q(`SELECT id, tipo, nombre, CAST(precio AS text), descripcion, bodega, denominacion_origen, CAST(graduacion AS text), anyo
		FROM group_menu_wines_v2 WHERE restaurant_id = $1 AND active = 1`, func(r *sql.Rows) error {
		var id int64
		var tipo, nm, price, desc, bod, do, grad, anyo sql.NullString
		if err := r.Scan(&id, &tipo, &nm, &price, &desc, &bod, &do, &grad, &anyo); err != nil {
			return err
		}
		add(node{UID: uid(rid, "wine", strconv.FormatInt(id, 10)), Kind: "wine", Label: "Wine", Title: nm.String,
			Body:  join(nm.String, desc.String, "Tipo: "+tipo.String, "Bodega: "+bod.String, "D.O.: "+do.String, "Graduación: "+grad.String, "Añada: "+anyo.String, "Precio: "+price.String),
			Price: price.String, Parent: ru, Rel: "OFFERS"})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("group_menu_wines_v2: %w", err)
	}
	if err := q(`SELECT id, menu_title, CAST(price AS text), entrantes, principales, postre, beverage, comments, seo_faq, CAST(min_party_size AS text)
		FROM menus WHERE restaurant_id = $1 AND active = 1 AND COALESCE(is_draft, 0) = 0`, func(r *sql.Rows) error {
		var id int64
		var t, price, ent, pri, pos, bev, com, faq, minp sql.NullString
		if err := r.Scan(&id, &t, &price, &ent, &pri, &pos, &bev, &com, &faq, &minp); err != nil {
			return err
		}
		add(node{UID: uid(rid, "menu", strconv.FormatInt(id, 10)), Kind: "menu", Label: "Menu", Title: t.String,
			Body:  join("Precio: "+price.String, "Mínimo comensales: "+minp.String, "Entrantes: "+ent.String, "Principales: "+pri.String, "Postre: "+pos.String, "Bebida: "+bev.String, com.String, "FAQ: "+faq.String),
			Price: price.String, Parent: ru, Rel: "OFFERS"})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("menus: %w", err)
	}
	if err := q(`SELECT d.id, d.menu_id, d.title_snapshot, d.description_snapshot, CAST(d.allergens_json AS text), CAST(d.price AS text), COALESCE(s.title, '')
		FROM group_menu_section_dishes_v2 d LEFT JOIN group_menu_sections_v2 s ON s.id = d.section_id AND s.restaurant_id = d.restaurant_id
		WHERE d.restaurant_id = $1 AND d.active = 1`, func(r *sql.Rows) error {
		var id, menuID int64
		var t, desc, alg, price, sec sql.NullString
		if err := r.Scan(&id, &menuID, &t, &desc, &alg, &price, &sec); err != nil {
			return err
		}
		add(node{UID: uid(rid, "menudish", strconv.FormatInt(id, 10)), Kind: "menudish", Label: "MenuDish", Title: t.String,
			Body:   join(t.String, desc.String, "Sección: "+sec.String, "Alérgenos: "+alg.String, "Suplemento: "+price.String),
			Parent: uid(rid, "menu", strconv.FormatInt(menuID, 10)), Rel: "HAS_DISH"})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("group_menu_section_dishes_v2: %w", err)
	}
	if err := q(`SELECT id, to_char(dateselected, 'YYYY-MM-DD'), hoursarray, COALESCE(opening_mode, '')
		FROM openinghours WHERE restaurant_id = $1 AND dateselected BETWEEN current_date AND current_date + 60`, func(r *sql.Rows) error {
		var id int64
		var d, h, mode sql.NullString
		if err := r.Scan(&id, &d, &h, &mode); err != nil {
			return err
		}
		add(node{UID: uid(rid, "hours", strconv.FormatInt(id, 10)), Kind: "hours", Label: "OpeningHours", Title: "Horario " + d.String,
			Body: join("Fecha: "+d.String, "Horas: "+h.String, "Modo: "+mode.String), Parent: ru, Rel: "HAS_HOURS"})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("openinghours: %w", err)
	}
	// FAQ/knowledge: menus.seo_faq is included in the Menu body; the bot
	// knowledge chunks live in the bot_context store (not the business schema).
	return out, nil
}

func uid(rid int, kind, id string) string {
	return "restaurant:" + strconv.Itoa(rid) + ":" + kind + ":" + id
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// join keeps only parts with content after their "Label: " prefix.
func join(parts ...string) string {
	var keep []string
	for _, p := range parts {
		v := p
		if i := strings.Index(p, ": "); i >= 0 {
			v = p[i+2:]
		}
		if strings.TrimSpace(v) != "" {
			keep = append(keep, strings.TrimSpace(p))
		}
	}
	return strings.Join(keep, " | ")
}

func hashOf(n node) string {
	h := sha256.Sum256([]byte(strings.Join([]string{n.Kind, n.Title, n.Body, n.Price, n.Parent, n.Rel}, "|")))
	return hex.EncodeToString(h[:])
}

// --- graph sync -------------------------------------------------------------

const batch = 200

func sync(ctx context.Context, in *graphrag.Instance, rid int, nodes []node, resync bool) (created, updated, deleted, edges int, err error) {
	res, err := in.Query(ctx, "graph-ingest", graphrag.Statement{Cypher: "MATCH (n:Doc {restaurant_id: $r}) RETURN n.uid, n.hash", Params: map[string]any{"r": rid}})
	if err != nil {
		return
	}
	have := map[string]string{}
	for _, row := range res.Rows {
		if len(row) >= 2 {
			u, _ := row[0].(string)
			h, _ := row[1].(string)
			have[u] = h
		}
	}
	var sts []graphrag.Statement
	flush := func() error {
		e := in.Tx(ctx, "graph-ingest", sts)
		sts = sts[:0]
		return e
	}
	want := map[string]bool{}
	for _, n := range nodes {
		want[n.UID] = true
		h := hashOf(n)
		p := map[string]any{"u": n.UID, "r": rid, "k": n.Kind, "t": n.Title, "b": n.Body, "p": n.Price, "h": h}
		old, ok := have[n.UID]
		switch {
		case !ok:
			sts = append(sts, graphrag.Statement{Cypher: "CREATE (n:Doc:" + n.Label + " {uid: $u, restaurant_id: $r, kind: $k, title: $t, body: $b, price: $p, hash: $h})", Params: p})
			created++
		case old != h:
			sts = append(sts, graphrag.Statement{Cypher: "MATCH (n:Doc {uid: $u, restaurant_id: $r}) SET n.kind = $k, n.title = $t, n.body = $b, n.price = $p, n.hash = $h", Params: p})
			updated++
		default:
			continue
		}
		if len(sts) >= batch {
			if err = flush(); err != nil {
				return
			}
		}
	}
	if resync {
		for u := range have {
			if !want[u] {
				p := map[string]any{"u": u, "r": rid}
				sts = append(sts,
					graphrag.Statement{Cypher: "MATCH (n:Doc {uid: $u, restaurant_id: $r})-[e]-() DELETE e", Params: p},
					graphrag.Statement{Cypher: "MATCH (n:Doc {uid: $u, restaurant_id: $r}) DELETE n", Params: p})
				deleted++
				if len(sts) >= batch {
					if err = flush(); err != nil {
						return
					}
				}
			}
		}
	}
	if err = flush(); err != nil {
		return
	}
	// Edges are derived: drop this restaurant's edges and rebuild them.
	if err = in.Tx(ctx, "graph-ingest", []graphrag.Statement{{Cypher: "MATCH (a:Doc {restaurant_id: $r})-[e]->(b:Doc) DELETE e", Params: map[string]any{"r": rid}}}); err != nil {
		return
	}
	for _, n := range nodes {
		if n.Parent == "" || !want[n.Parent] {
			continue
		}
		sts = append(sts, graphrag.Statement{Cypher: "MATCH (a:Doc {uid: $a, restaurant_id: $r}), (b:Doc {uid: $b, restaurant_id: $r}) CREATE (a)-[:" + n.Rel + "]->(b)",
			Params: map[string]any{"a": n.Parent, "b": n.UID, "r": rid}})
		edges++
		if len(sts) >= batch {
			if err = flush(); err != nil {
				return
			}
		}
	}
	err = flush()
	return
}
