package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/db"
	"preactvillacarmen/internal/db/migrations"
)

// La carta de vinos se escribe en group_menu_wines_v2; `num` es la clave legacy
// que legacy_wine_map traduce al id de la v2.
// wineFotoPathUpdateSQL writes the BunnyCDN path of a migrated image back into
// the v2 table.
const wineFotoPathUpdateSQL = `UPDATE group_menu_wines_v2 w
	JOIN legacy_wine_map m ON m.v2_wine_id = w.id AND m.restaurant_id = w.restaurant_id
	SET w.foto_path = ?
	WHERE w.restaurant_id = ? AND m.legacy_num = ?`

// isLegacyVinosMissing reports whether the error is just "the legacy VINOS
// table is not there", which is the expected state after the migration to v2
// finished and the table was dropped.
func isLegacyVinosMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "doesn't exist") &&
		(strings.Contains(msg, "vinos") || strings.Contains(msg, "1146"))
}

func main() {
	var (
		dryRun   = flag.Bool("dry-run", true, "Don't write DB updates")
		upload   = flag.Bool("upload", false, "Upload blobs to BunnyCDN storage")
		keepBlob = flag.Bool("keep-blob", false, "keep the source blob (only meaningful while reading the legacy VINOS table)")
		limit    = flag.Int("limit", 0, "Max wines to process (0 = no limit)")
	)
	flag.Parse()

	_ = godotenv.Overload("../.env")
	_ = godotenv.Overload(".env")

	cfg := config.Load()
	if cfg.BunnyStorageKey == "" && *upload {
		// Not fatal anymore: credentials are resolved per restaurant from
		// bunny_storage_config, and env is only the fallback.
		log.Printf("warning: BUNNY_STORAGE_ACCESS_KEY unset; restaurants without a stored config will fail to upload")
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
		// The legacy VINOS table was dropped once every blob was uploaded to
		// BunnyCDN. Say so plainly instead of failing with a raw SQL error:
		// there is nothing left to migrate.
		if isLegacyVinosMissing(err) {
			log.Printf("VINOS legacy table no longer exists: every blob was already migrated to BunnyCDN. Nothing to do.")
			return
		}
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
			objectPath, uploadErr = s.UploadWineImage(ctx, r.restaurantID, r.tipo, r.num, r.foto)
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
		_, err := db.ExecContext(ctx, wineFotoPathUpdateSQL, objectPath, restaurantID, num)
		return err
	}
	_, err := db.ExecContext(ctx, wineFotoPathUpdateSQL, objectPath, restaurantID, num)
	return err
}
