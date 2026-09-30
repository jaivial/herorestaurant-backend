// Command chatgpt-plugin-token issues and revokes the bearer credentials used
// by the ChatGPT plugin (chatgpt_plugin_v1).
//
// A token is pinned to one backoffice user and one restaurant, and inherits
// that user's role, so a connector can never reach more than the operator
// already can from the backoffice. The raw secret is printed exactly once: the
// database only stores its SHA-256 digest, so a lost token must be revoked and
// reissued rather than recovered.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	appdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/db/migrations"
)

func main() {
	var (
		userID       = flag.Int("user", 0, "Backoffice user id that will own the token (bo_users.id)")
		restaurantID = flag.Int("restaurant", 0, "Restaurant the token is pinned to (restaurants.id)")
		label        = flag.String("label", "", "Human label stored with the token, e.g. 'chatgpt connector'")
		revoke       = flag.Bool("revoke", false, "Revoke the user's plugin tokens instead of issuing one")
		list         = flag.Bool("list", false, "List existing tokens for the user without showing secrets")
	)
	flag.Parse()

	cfg := config.Load()
	db, err := appdb.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// SKIP_MIGRATIONS=1 lets the generator run against an already-migrated
	// database, matching cmd/server.
	if os.Getenv("SKIP_MIGRATIONS") != "1" {
		if err := migrations.Apply(ctx, db); err != nil {
			log.Fatalf("Failed to apply migrations: %v", err)
		}
	}

	server := api.NewPluginTokenService(db)

	switch {
	case *list:
		if err := listTokens(ctx, db, *userID, *restaurantID); err != nil {
			log.Fatalf("Failed to list tokens: %v", err)
		}
	case *revoke:
		n, err := server.Revoke(ctx, *userID, *restaurantID)
		if err != nil {
			log.Fatalf("Failed to revoke tokens: %v", err)
		}
		fmt.Printf("Revoked %d plugin token(s) for user %d", n, *userID)
		if *restaurantID > 0 {
			fmt.Printf(" on restaurant %d", *restaurantID)
		}
		fmt.Println()
	default:
		if *userID <= 0 || *restaurantID <= 0 {
			log.Fatal("--user and --restaurant are required (use --list, --revoke or --help)")
		}
		token, err := server.Issue(ctx, *userID, *restaurantID, *label)
		if err != nil {
			log.Fatalf("Failed to issue token: %v", err)
		}
		printIssuedToken(token, *userID, *restaurantID)
	}
}

// printIssuedToken shows the secret once, together with the exact steps the
// operator needs in the ChatGPT connector UI. The secret cannot be recovered
// later, so the warning is part of the output rather than buried in the docs.
func printIssuedToken(token string, userID, restaurantID int) {
	fmt.Printf("ChatGPT plugin token issued (user %d, restaurant %d)\n\n", userID, restaurantID)
	fmt.Println("  API key (copy it now, it is never shown again):")
	fmt.Printf("    %s\n\n", token)
	fmt.Println("Configure it in ChatGPT:")
	fmt.Println("  1. Create a new GPT and choose \"Add to GPT\" -> \"Create a GPT\".")
	fmt.Println("  2. Configure > Actions -> Add a plugin (or \"Create a new action\").")
	fmt.Printf("  3. Authentication: paste the API key above (bearer).\n")
	fmt.Println("  4. The manifest is served from your own host at /api/plugin, so the")
	fmt.Println("     connector picks it up automatically once the key is saved.")
	fmt.Println()
	fmt.Println("Revoke with:")
	fmt.Printf("  go run ./cmd/chatgpt-plugin-token --user %d --restaurant %d --revoke\n", userID, restaurantID)
}

func listTokens(ctx context.Context, db *sql.DB, userID, restaurantID int) error {
	q := `
		SELECT t.id, t.user_id, t.restaurant_id, COALESCE(t.label, ''), t.created_at,
		       t.last_used_at, t.revoked_at
		FROM chatgpt_plugin_tokens t
		WHERE (? = 0 OR t.user_id = ?) AND (? = 0 OR t.restaurant_id = ?)
		ORDER BY t.id DESC
	`
	rows, err := db.QueryContext(ctx, q, userID, userID, restaurantID, restaurantID)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Printf("%-5s %-7s %-12s %-24s %-20s %-20s %s\n", "ID", "USER", "RESTAURANT", "LABEL", "CREATED", "LAST USED", "STATUS")
	for rows.Next() {
		var id, uid, rid int
		var label, created, lastUsed, revoked sql.NullString
		if err := rows.Scan(&id, &uid, &rid, &label, &created, &lastUsed, &revoked); err != nil {
			return err
		}
		status := "active"
		if revoked.Valid {
			status = "revoked " + revoked.String
		}
		used := "-"
		if lastUsed.Valid {
			used = lastUsed.String
		}
		fmt.Printf("%-5d %-7d %-12d %-24s %-20s %-20s %s\n", id, uid, rid, label.String, created.String, used, status)
	}
	return rows.Err()
}
