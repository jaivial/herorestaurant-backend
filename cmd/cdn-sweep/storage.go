package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Bunny's storage API lists one directory at a time, so a full inventory means
// walking the tree. maxObjects caps a run: an unbounded walk on a large zone
// would hold the whole listing in memory and run for an unpredictable time.
const maxObjects = 200000

type bunnyListEntry struct {
	ObjectName  string `json:"ObjectName"`
	Path        string `json:"Path"`
	IsDirectory bool   `json:"IsDirectory"`
	Length      int64  `json:"Length"`
	LastChanged string `json:"LastChanged"`
	DateCreated string `json:"DateCreated"`
}

// listZone walks the storage zone depth-first and returns every file.
func listZone(ctx context.Context, zone, accessKey string) ([]storageObject, error) {
	var out []storageObject
	queue := []string{""}
	visited := map[string]bool{}

	for len(queue) > 0 {
		prefix := queue[0]
		queue = queue[1:]
		if visited[prefix] {
			continue
		}
		visited[prefix] = true

		entries, err := listDirectory(ctx, zone, accessKey, prefix)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			path := strings.TrimPrefix(prefix+entry.ObjectName, "/")
			if entry.IsDirectory {
				queue = append(queue, path+"/")
				continue
			}
			out = append(out, storageObject{
				Path:         path,
				SizeBytes:    entry.Length,
				LastModified: parseBunnyTime(entry.LastChanged, entry.DateCreated),
			})
			if len(out) >= maxObjects {
				return nil, fmt.Errorf("la zona supera el limite de %d objetos por ejecucion", maxObjects)
			}
		}
	}
	return out, nil
}

func listDirectory(ctx context.Context, zone, accessKey, prefix string) ([]bunnyListEntry, error) {
	endpoint := "https://storage.bunnycdn.com/" + url.PathEscape(zone) + "/"
	if prefix != "" {
		endpoint += bunnyEscapeListPath(prefix)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("AccessKey", accessKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return nil, fmt.Errorf("bunny list %q failed (%d): %s", prefix, res.StatusCode, strings.TrimSpace(string(body)))
	}
	var entries []bunnyListEntry
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(&entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// parseBunnyTime prefers the last change; a zero time would make an object look
// ancient and therefore deletable, so an unparseable value is treated as "now"
// and the object is protected by the grace window.
func parseBunnyTime(values ...string) time.Time {
	for _, value := range values {
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
				return parsed
			}
		}
	}
	return time.Now()
}

func bunnyEscapeListPath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	joined := strings.Join(parts, "/")
	if joined == "" {
		return ""
	}
	return joined + "/"
}

func deleteObject(ctx context.Context, zone, accessKey, objectPath string) error {
	endpoint := "https://storage.bunnycdn.com/" + url.PathEscape(zone) + "/" + bunnyEscapeListPath(objectPath)
	endpoint = strings.TrimSuffix(endpoint, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("AccessKey", accessKey)
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	return fmt.Errorf("bunny delete failed (%d): %s", res.StatusCode, strings.TrimSpace(string(body)))
}
