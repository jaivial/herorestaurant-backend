// Command update-restaurant-info updates the restaurant_info table with the correct address,
// website and menu URL. Run from the herorestaurant-backend directory:
//   cd /projects/newvillacarmen/herorestaurant-backend
//   go run cmd/update-restaurant-info/main.go

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env
	if err := godotenv.Load(); err != nil {
		fmt.Println("Warning: .env file not found, using environment variables")
	}

	host := envDefault("DB_HOST", "127.0.0.1")
	port := envDefault("DB_PORT", "3306")
	user := envDefault("DB_USER", "villacarmen")
	password := envDefault("DB_PASSWORD", "villacarmen")
	dbName := envDefault("DB_NAME", "villacarmen")
	restaurantID := envDefault("RESTAURANT_ID", "1")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci",
		user, password, host, port, dbName)

	fmt.Printf("Connecting to database: %s@%s:%s/%s\n", user, host, port, dbName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		fmt.Printf("Error connecting to DB: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Connected successfully")

	direccion := "C/ Sequía de Rascanya, 2 46470 Catarroja Valencia"
	telefono := "638 85 72 94"
	website := "https://www.alqueriavillacarmen.com/"
	menuURL := "https://www.alqueriavillacarmen.com/menufindesemana.php"

	// Add columns if they don't exist (safe to run multiple times)
	fmt.Println("Adding new columns if they don't exist...")
	_, _ = db.ExecContext(ctx, "ALTER TABLE restaurant_info ADD COLUMN website VARCHAR(512) DEFAULT ''")
	_, _ = db.ExecContext(ctx, "ALTER TABLE restaurant_info ADD COLUMN menu_url VARCHAR(512) DEFAULT ''")

	// Update the row
	result, err := db.ExecContext(ctx, `
		INSERT INTO restaurant_info (restaurant_id, direccion, telefono, website, menu_url)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			direccion = VALUES(direccion),
			telefono = VALUES(telefono),
			website = VALUES(website),
			menu_url = VALUES(menu_url)
	`, restaurantID, direccion, telefono, website, menuURL)

	if err != nil {
		fmt.Printf("Error updating restaurant_info: %v\n", err)
		os.Exit(1)
	}

	rowsAffected, _ := result.RowsAffected()
	fmt.Printf("Updated restaurant_info (restaurant_id=%s)\n", restaurantID)
	fmt.Printf("  direccion = %s\n", direccion)
	fmt.Printf("  telefono = %s\n", telefono)
	fmt.Printf("  website = %s\n", website)
	fmt.Printf("  menu_url = %s\n", menuURL)
	fmt.Printf("  rows affected: %d\n", rowsAffected)

	// Verify
	var dir, tel, web, menu sql.NullString
	err = db.QueryRowContext(ctx, `
		SELECT direccion, telefono, website, menu_url
		FROM restaurant_info WHERE restaurant_id = ?
	`, restaurantID).Scan(&dir, &tel, &web, &menu)
	if err == nil {
		fmt.Println("\nVerified - stored values:")
		if dir.Valid {
			fmt.Printf("  direccion: %s\n", dir.String)
		}
		if tel.Valid {
			fmt.Printf("  telefono: %s\n", tel.String)
		}
		if web.Valid {
			fmt.Printf("  website: %s\n", web.String)
		}
		if menu.Valid {
			fmt.Printf("  menu_url: %s\n", menu.String)
		}
	}

	fmt.Println("\nDone!")
}

func envDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
