package main

import (
	"fmt"
	"strings"
	"time"
)

// The sweep deletes files from the public CDN, so it is the highest blast
// radius job in the system. Every rule below exists to make an incorrect
// deletion impossible rather than merely unlikely:
//
//   - a grace window, because a file reaches the CDN before its row reaches
//     MySQL, and an in-flight upload must survive;
//   - a ratio guard, because a reference query that silently returns nothing
//     would otherwise look like "everything is orphaned";
//   - an explicit failure flag, because a query error is not evidence of
//     absence;
//   - dry-run by default, so the first runs only report.

type storageObject struct {
	Path         string
	SizeBytes    int64
	LastModified time.Time
}

type sweepOptions struct {
	// GraceWindow protects recently uploaded objects whose row may not be
	// committed yet.
	GraceWindow time.Duration
	// MaxDeleteRatio aborts the run when this share of eligible objects would
	// be deleted.
	MaxDeleteRatio float64
	// ReferenceQueryFailed is set when any reference query errored. A partial
	// reference set is worse than useless: it looks like proof of orphanhood.
	ReferenceQueryFailed bool
}

type sweepPlan struct {
	Delete      []storageObject
	Skipped     int
	Referenced  int
	Aborted     bool
	AbortReason string
}

// planSweep decides what may be deleted. It is pure so the safety rules can be
// proven by tests without touching a CDN.
func planSweep(objects []storageObject, referenced map[string]bool, opts sweepOptions) sweepPlan {
	plan := sweepPlan{}
	if opts.ReferenceQueryFailed {
		plan.Aborted = true
		plan.AbortReason = "una consulta de referencias fallo; no se puede distinguir huerfano de no leido"
		return plan
	}

	cutoff := time.Now().Add(-opts.GraceWindow)
	var candidates []storageObject
	eligible := 0
	for _, object := range objects {
		if object.LastModified.After(cutoff) {
			// Too recent to judge: its row may still be in flight.
			plan.Skipped++
			continue
		}
		eligible++
		if referenced[normalizeObjectPath(object.Path)] {
			plan.Referenced++
			continue
		}
		candidates = append(candidates, object)
	}

	if eligible > 0 && opts.MaxDeleteRatio > 0 {
		ratio := float64(len(candidates)) / float64(eligible)
		if ratio > opts.MaxDeleteRatio {
			// This is the shape of a bug, not of a real cleanup.
			plan.Aborted = true
			plan.AbortReason = fmt.Sprintf(
				"se borraria el %.0f%% de los objetos elegibles (%d de %d), por encima del limite del %.0f%%",
				ratio*100, len(candidates), eligible, opts.MaxDeleteRatio*100)
			return plan
		}
	}

	plan.Delete = candidates
	return plan
}

// normalizeObjectPath reduces a stored value to the bare object path. Columns
// hold a mixture of full CDN URLs, absolute paths and bare paths, while the
// storage API always returns bare paths.
func normalizeObjectPath(value string) string {
	path := strings.TrimSpace(value)
	if path == "" {
		return ""
	}
	if index := strings.Index(path, "://"); index >= 0 {
		rest := path[index+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			path = rest[slash+1:]
		} else {
			return ""
		}
	}
	if question := strings.Index(path, "?"); question >= 0 {
		path = path[:question]
	}
	return strings.TrimPrefix(path, "/")
}

func referencedSetFromValues(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		if normalized := normalizeObjectPath(value); normalized != "" {
			out[normalized] = true
		}
	}
	return out
}
