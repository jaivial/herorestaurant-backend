package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/joho/godotenv"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/db"
	"preactvillacarmen/internal/db/migrations"
)

func main() {
	var (
		dryRun   = flag.Bool("dry-run", true, "Don't write DB updates")
		upload   = flag.Bool("upload", false, "Upload blobs to BunnyCDN storage")
		keepBlob = flag.Bool("keep-blob", false, "Keep VINOS.foto blob after migration")
		limit    = flag.Int("limit", 0, "Max wines to process (0 = no limit)")
	)
	flag.Parse()

	_ = godotenv.Overload("../.env")
	_ = godotenv.Overload(".env")

	cfg := config.Load()
	if cfg.BunnyStorageKey == "" && *upload {
		log.Fatalf("missing BUNNY_STORAGE_ACCESS_KEY")
	}

	sqlDB, err := db.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer sqlDB.Close()

	{
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := migrations.Apply(ctx, sqlDB); err != nil {
			log.Fatalf("db migrations: %v", err)
		}
	}

	s := api.NewServer(sqlDB, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	rows, err := sqlDB.QueryContext(ctx, `
		SELECT restaurant_id, num, COALESCE(tipo,''), foto
		FROM VINOS
		WHERE foto IS NOT NULL AND LENGTH(foto) > 0 AND (foto_path IS NULL OR LENGTH(foto_path) = 0)
		ORDER BY restaurant_id ASC, num ASC
	`)
	if err != nil {
		log.Fatalf("query vinos: %v", err)
	}
	defer rows.Close()

	type row struct {
		restaurantID int
		num          int
		tipo         string
		foto         []byte
	}

	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.restaurantID, &r.num, &r.tipo, &r.foto); err != nil {
			log.Fatalf("scan: %v", err)
		}
		pending = append(pending, r)
		if *limit > 0 && len(pending) >= *limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("rows: %v", err)
	}

	if len(pending) == 0 {
		log.Printf("nothing to migrate")
		return
	}

	log.Printf("found %d wines to migrate (dry-run=%v, upload=%v)", len(pending), *dryRun, *upload)

	var (
		okCount   int
		failCount int
	)
	for _, r := range pending {
		var objectPath string
		var uploadErr error
		if !*upload {
			objectPath = fmt.Sprintf("images/vinos/<tipo>/%d.<ext>", r.num)
		} else {
			objectPath, uploadErr = s.UploadWineImage(ctx, r.tipo, r.num, r.foto)
		}

		if uploadErr != nil {
			failCount++
			log.Printf("FAIL restaurant=%d num=%d: %v", r.restaurantID, r.num, uploadErr)
			continue
		}

		if *dryRun {
			okCount++
			log.Printf("DRY restaurant=%d num=%d -> %s", r.restaurantID, r.num, objectPath)
			continue
		}

		if err := updateWinePath(ctx, sqlDB, r.restaurantID, r.num, objectPath, *keepBlob); err != nil {
			failCount++
			log.Printf("FAIL restaurant=%d num=%d db: %v", r.restaurantID, r.num, err)
			continue
		}
		okCount++
		log.Printf("OK  restaurant=%d num=%d -> %s", r.restaurantID, r.num, objectPath)
	}

	log.Printf("done ok=%d fail=%d", okCount, failCount)
}

func updateWinePath(ctx context.Context, db *sql.DB, restaurantID int, num int, objectPath string, keepBlob bool) error {
	if keepBlob {
		_, err := db.ExecContext(ctx, "UPDATE VINOS SET foto_path = ? WHERE restaurant_id = ? AND num = ?", objectPath, restaurantID, num)
		return err
	}
	_, err := db.ExecContext(ctx, "UPDATE VINOS SET foto_path = ?, foto = NULL WHERE restaurant_id = ? AND num = ?", objectPath, restaurantID, num)
	return err
}
