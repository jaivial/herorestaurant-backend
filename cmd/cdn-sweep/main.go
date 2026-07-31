// Command cdn-sweep removes orphaned objects from the public CDN storage zone.
//
// This job can destroy customer data, so it is built to fail safe:
//   - --dry-run is the DEFAULT; real deletion needs an explicit --apply.
//   - Objects newer than the grace window are never touched (an upload reaches
//     the CDN before its database row does).
//   - The run aborts if an implausible share of objects looks orphaned, or if
//     any reference query failed, or if the schema has an image column that is
//     not in the reviewed registry.
//   - The private document zone is never listed: it has its own retention and
//     legal requirements.
//   - Every run and every deletion is recorded for audit.
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"time"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/db"
)

func main() {
	apply := flag.Bool("apply", false, "actually delete orphaned objects (default: report only)")
	graceHours := flag.Int("grace-hours", 48, "protect objects modified within this many hours")
	maxRatio := flag.Float64("max-delete-ratio", 0.5, "abort if more than this share of eligible objects would be deleted")
	flag.Parse()

	cfg := config.Load()
	if cfg.BunnyStorageZone == "" || cfg.BunnyStorageKey == "" {
		log.Fatal("cdn-sweep: public storage zone is not configured")
	}

	database, err := db.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Two sweeps running at once could both see an object as unreferenced and
	// race on deletion, so only one may run at a time.
	var locked int
	if err = database.QueryRowContext(ctx, `SELECT GET_LOCK('villacarmen:cdn-sweep',0)`).Scan(&locked); err != nil || locked != 1 {
		log.Print("cdn-sweep skipped: lock held")
		return
	}
	defer database.ExecContext(context.Background(), `SELECT RELEASE_LOCK('villacarmen:cdn-sweep')`)

	sweepID := startSweep(ctx, database, !*apply)
	if err := run(ctx, database, cfg, sweepID, *apply, time.Duration(*graceHours)*time.Hour, *maxRatio); err != nil {
		finishSweep(ctx, database, sweepID, "FAILED", err.Error(), sweepPlan{})
		log.Fatalf("cdn-sweep failed: %v", err)
	}
}

func run(ctx context.Context, database *sql.DB, cfg config.Config, sweepID int64,
	apply bool, grace time.Duration, maxRatio float64) error {
	schema := cfg.MySQL.DBName

	// The registry defines what "referenced" means. If the schema grew an image
	// column nobody registered, those images would look orphaned, so this is a
	// hard stop rather than a warning.
	discovered, err := discoverURLColumns(ctx, database, schema)
	if err != nil {
		return err
	}
	if unknown := unregisteredImageColumns(discovered, registeredColumnSet()); len(unknown) > 0 {
		plan := sweepPlan{Aborted: true, AbortReason: "columnas de imagen sin registrar: " + joinList(unknown)}
		finishSweep(ctx, database, sweepID, "ABORTED", plan.AbortReason, plan)
		log.Printf("cdn-sweep aborted: %s", plan.AbortReason)
		return nil
	}

	referenced, refErr := collectReferenced(ctx, database, schema)
	if refErr != nil {
		// Reported through the plan rather than returned, so the abort reason
		// is recorded in the same audited shape as every other stop.
		plan := planSweep(nil, nil, sweepOptions{ReferenceQueryFailed: true})
		finishSweep(ctx, database, sweepID, "ABORTED", plan.AbortReason+": "+refErr.Error(), plan)
		log.Printf("cdn-sweep aborted: %v", refErr)
		return nil
	}

	// Only the public zone is listed. The private document zone is governed by
	// its own retention policy and must never be swept here.
	objects, err := listZone(ctx, cfg.BunnyStorageZone, cfg.BunnyStorageKey)
	if err != nil {
		return err
	}

	plan := planSweep(objects, referenced, sweepOptions{GraceWindow: grace, MaxDeleteRatio: maxRatio})
	if plan.Aborted {
		finishSweep(ctx, database, sweepID, "ABORTED", plan.AbortReason, plan)
		log.Printf("cdn-sweep aborted: %s", plan.AbortReason)
		return nil
	}

	log.Printf("cdn-sweep: listed=%d referenced=%d skipped_recent=%d orphaned=%d apply=%v",
		len(objects), plan.Referenced, plan.Skipped, len(plan.Delete), apply)

	failures := 0
	for _, object := range plan.Delete {
		deleteErr := ""
		deleted := false
		if apply {
			if err := deleteObject(ctx, cfg.BunnyStorageZone, cfg.BunnyStorageKey, object.Path); err != nil {
				deleteErr = err.Error()
				failures++
			} else {
				deleted = true
			}
		}
		recordDeletion(ctx, database, sweepID, object, deleted, deleteErr)
	}

	status := "SUCCEEDED"
	if failures > 0 {
		status = "FAILED"
	}
	finishSweep(ctx, database, sweepID, status, "", plan)
	return nil
}

func startSweep(ctx context.Context, database *sql.DB, dryRun bool) int64 {
	res, err := database.ExecContext(ctx,
		`INSERT INTO cdn_object_sweeps (started_at,status,dry_run) VALUES (NOW(),'RUNNING',?)`,
		boolToInt(dryRun))
	if err != nil {
		log.Fatalf("cdn-sweep: could not record the run: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func finishSweep(ctx context.Context, database *sql.DB, id int64, status, message string, plan sweepPlan) {
	deleted := 0
	for range plan.Delete {
		deleted++
	}
	if _, err := database.ExecContext(ctx, `
		UPDATE cdn_object_sweeps
		   SET finished_at=NOW(), status=?, objects_listed=?, objects_referenced=?,
		       objects_deleted=?, objects_skipped=?, error_message=NULLIF(?,'')
		 WHERE id=?`,
		status, plan.Referenced+plan.Skipped+deleted, plan.Referenced, deleted, plan.Skipped,
		truncate(message, 500), id); err != nil {
		log.Printf("cdn-sweep: could not finalise the run: %v", err)
	}
}

func recordDeletion(ctx context.Context, database *sql.DB, sweepID int64, object storageObject, deleted bool, errMessage string) {
	if _, err := database.ExecContext(ctx, `
		INSERT INTO cdn_object_sweep_deletions
			(sweep_id,object_path,size_bytes,last_modified_at,deleted,error_message)
		VALUES (?,?,?,?,?,NULLIF(?,''))`,
		sweepID, truncate(object.Path, 1000), object.SizeBytes, object.LastModified,
		boolToInt(deleted), truncate(errMessage, 500)); err != nil {
		log.Printf("cdn-sweep: could not record deletion of %s: %v", object.Path, err)
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func joinList(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}
