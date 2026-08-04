package main

import (
	"testing"
	"time"
)

func obj(path string, age time.Duration) storageObject {
	return storageObject{Path: path, LastModified: time.Now().Add(-age)}
}

// The grace window exists because an upload commits to the CDN before its row
// is committed to MySQL. Deleting inside that gap would destroy a live image.
func TestFreshObjectsAreNeverDeleted(t *testing.T) {
	plan := planSweep(
		[]storageObject{obj("a.webp", 1*time.Hour)},
		map[string]bool{},
		sweepOptions{GraceWindow: 48 * time.Hour, MaxDeleteRatio: 0.5},
	)
	if len(plan.Delete) != 0 {
		t.Fatalf("deleting %v inside the grace window risks killing an in-flight upload", plan.Delete)
	}
	if plan.Skipped != 1 {
		t.Fatalf("skipped=%d want 1", plan.Skipped)
	}
}

func TestReferencedObjectsAreNeverDeleted(t *testing.T) {
	plan := planSweep(
		[]storageObject{obj("dishes/1.webp", 72*time.Hour)},
		map[string]bool{"dishes/1.webp": true},
		sweepOptions{GraceWindow: 48 * time.Hour, MaxDeleteRatio: 0.5},
	)
	if len(plan.Delete) != 0 {
		t.Fatalf("a referenced object must never be deleted, got %v", plan.Delete)
	}
}

func TestUnreferencedOldObjectIsCollected(t *testing.T) {
	plan := planSweep(
		[]storageObject{obj("keep.webp", 72*time.Hour), obj("orphan.webp", 72*time.Hour)},
		map[string]bool{"keep.webp": true},
		sweepOptions{GraceWindow: 48 * time.Hour, MaxDeleteRatio: 0.9},
	)
	if len(plan.Delete) != 1 || plan.Delete[0].Path != "orphan.webp" {
		t.Fatalf("delete=%v want just orphan.webp", plan.Delete)
	}
}

// If a reference query silently returned nothing, almost everything would look
// orphaned. Rather than trust that, the run aborts: a broken query must never
// be read as "nothing is referenced".
func TestSweepAbortsWhenTooMuchWouldBeDeleted(t *testing.T) {
	objects := []storageObject{
		obj("1.webp", 72*time.Hour), obj("2.webp", 72*time.Hour),
		obj("3.webp", 72*time.Hour), obj("4.webp", 72*time.Hour),
	}
	plan := planSweep(objects, map[string]bool{"1.webp": true}, sweepOptions{
		GraceWindow: 48 * time.Hour, MaxDeleteRatio: 0.5,
	})
	if !plan.Aborted {
		t.Fatal("deleting 3 of 4 objects must abort the run")
	}
	if len(plan.Delete) != 0 {
		t.Fatalf("an aborted run must delete nothing, got %v", plan.Delete)
	}
	if plan.AbortReason == "" {
		t.Fatal("an abort must say why")
	}
}

func TestRatioGuardIgnoresObjectsInsideTheGraceWindow(t *testing.T) {
	// 1 old orphan out of 1 eligible object, but 9 fresh ones are not eligible
	// at all, so the ratio is measured against what could be deleted.
	objects := []storageObject{obj("orphan.webp", 72*time.Hour)}
	for i := 0; i < 9; i++ {
		objects = append(objects, obj("fresh.webp", time.Hour))
	}
	plan := planSweep(objects, map[string]bool{}, sweepOptions{
		GraceWindow: 48 * time.Hour, MaxDeleteRatio: 0.5,
	})
	if !plan.Aborted {
		t.Fatal("100% of eligible objects would be deleted; that must abort")
	}
}

// An empty zone is not an emergency: there is simply nothing to do.
func TestEmptyZoneIsNotAnAbort(t *testing.T) {
	plan := planSweep(nil, map[string]bool{}, sweepOptions{
		GraceWindow: 48 * time.Hour, MaxDeleteRatio: 0.5,
	})
	if plan.Aborted {
		t.Fatalf("an empty zone must not abort: %s", plan.AbortReason)
	}
	if len(plan.Delete) != 0 {
		t.Fatal("nothing to delete in an empty zone")
	}
}

// Paths are compared after normalisation because the DB stores full CDN URLs
// while the storage API returns bare object paths.
func TestReferenceMatchingIgnoresURLPrefixAndLeadingSlash(t *testing.T) {
	referenced := referencedSetFromValues([]string{
		"https://cdn.example.com/dishes/1.webp",
		"/dishes/2.webp",
		"dishes/3.webp",
		"",
	})
	for _, want := range []string{"dishes/1.webp", "dishes/2.webp", "dishes/3.webp"} {
		if !referenced[want] {
			t.Fatalf("%q not in referenced set %v", want, referenced)
		}
	}
	if referenced[""] {
		t.Fatal("an empty column value must not become a reference")
	}
}

func TestQueryFailureAbortsBeforeAnyDeletion(t *testing.T) {
	plan := planSweep(
		[]storageObject{obj("orphan.webp", 72*time.Hour)},
		map[string]bool{},
		sweepOptions{GraceWindow: 48 * time.Hour, MaxDeleteRatio: 0.9, ReferenceQueryFailed: true},
	)
	if !plan.Aborted || len(plan.Delete) != 0 {
		t.Fatal("a failed reference query must abort without deleting anything")
	}
}

// The registry is the sweep's whole notion of "referenced". If someone adds a
// new image column and forgets to register it, every image in that column looks
// orphaned and would be deleted. So an unknown column is a hard stop.
func TestUnregisteredImageColumnAbortsTheRun(t *testing.T) {
	registered := map[string]bool{"stock_items.image_url": true}
	discovered := []string{"stock_items.image_url", "new_feature.image_url"}

	unknown := unregisteredImageColumns(discovered, registered)
	if len(unknown) != 1 || unknown[0] != "new_feature.image_url" {
		t.Fatalf("unknown=%v want [new_feature.image_url]", unknown)
	}
}

func TestFullyRegisteredSchemaReportsNoUnknownColumns(t *testing.T) {
	registered := map[string]bool{"a.image_url": true, "b.foto_path": true}
	if unknown := unregisteredImageColumns([]string{"a.image_url", "b.foto_path"}, registered); len(unknown) != 0 {
		t.Fatalf("unknown=%v want none", unknown)
	}
}

// Columns that hold outbound links or documents are not CDN image references;
// listing them as ignored keeps the guard honest instead of silencing it.
func TestIgnoredColumnsAreNotTreatedAsImageReferences(t *testing.T) {
	if !isIgnoredURLColumn("restaurant_integrations.n8n_webhook_url") {
		t.Fatal("a webhook URL is not a CDN image")
	}
	if isIgnoredURLColumn("stock_items.image_url") {
		t.Fatal("a real image column must never be ignored")
	}
}
