package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

func (s *Server) bunnyConfigured() bool {
	return strings.TrimSpace(s.cfg.BunnyStorageKey) != "" && strings.TrimSpace(s.cfg.BunnyStorageZone) != "" && strings.TrimSpace(s.cfg.BunnyPullBaseURL) != ""
}

func (s *Server) bunnyMembersConfigured() bool {
	return strings.TrimSpace(s.cfg.BunnyMemberStorageKey) != "" && strings.TrimSpace(s.cfg.BunnyMemberStorageZone) != "" && strings.TrimSpace(s.cfg.BunnyMemberPullBaseURL) != ""
}

func (s *Server) bunnyPullURL(objectPath string) string {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.BunnyPullBaseURL), "/")
	p := strings.TrimLeft(objectPath, "/")
	return base + "/" + p
}

func (s *Server) bunnyMembersPullURL(objectPath string) string {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.BunnyMemberPullBaseURL), "/")
	p := strings.TrimLeft(objectPath, "/")
	return base + "/" + p
}

func (s *Server) bunnyPut(ctx context.Context, objectPath string, payload []byte, contentType string) error {
	if !s.bunnyConfigured() {
		return errors.New("BunnyCDN storage not configured")
	}
	return bunnyPutWithCredentials(ctx, strings.TrimSpace(s.cfg.BunnyStorageZone), strings.TrimSpace(s.cfg.BunnyStorageKey), objectPath, payload, contentType)
}

func (s *Server) bunnyMembersPut(ctx context.Context, objectPath string, payload []byte, contentType string) error {
	if !s.bunnyMembersConfigured() {
		return errors.New("BunnyCDN member storage not configured")
	}
	return bunnyPutWithCredentials(ctx, strings.TrimSpace(s.cfg.BunnyMemberStorageZone), strings.TrimSpace(s.cfg.BunnyMemberStorageKey), objectPath, payload, contentType)
}

func bunnyPutWithCredentials(ctx context.Context, zone, accessKey, objectPath string, payload []byte, contentType string) error {
	if len(payload) == 0 {
		return errors.New("empty payload")
	}
	if strings.TrimSpace(zone) == "" || strings.TrimSpace(accessKey) == "" {
		return errors.New("invalid bunny credentials")
	}
	if contentType == "" {
		contentType = http.DetectContentType(payload)
	}
	objectPath = strings.TrimLeft(objectPath, "/")
	escaped := bunnyEscapePath(objectPath)

	u := "https://storage.bunnycdn.com/" + url.PathEscape(zone) + "/" + escaped

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("AccessKey", accessKey)
	req.Header.Set("Content-Type", contentType)

	cli := &http.Client{Timeout: 30 * time.Second}
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = res.Status
	}
	return fmt.Errorf("bunny upload failed (%d): %s", res.StatusCode, msg)
}

func bunnyEscapePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimLeft(p, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, url.PathEscape(part))
	}
	return strings.Join(out, "/")
}

func wineTypeSlug(tipo string) string {
	t := strings.ToLower(strings.TrimSpace(tipo))
	switch t {
	case "tinto":
		return "tinto"
	case "blanco":
		return "blanco"
	case "cava":
		return "cava"
	case "tintos":
		return "tinto"
	case "blancos":
		return "blanco"
	default:
		// Also support DB/legacy values like "TINTO".
		switch strings.ToUpper(t) {
		case "TINTO":
			return "tinto"
		case "BLANCO":
			return "blanco"
		case "CAVA":
			return "cava"
		default:
			return "otros"
		}
	}
}

func fileExtForContentType(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(ct, "image/jpeg") {
		return ".jpg"
	}
	if strings.HasPrefix(ct, "image/png") {
		return ".png"
	}
	if strings.HasPrefix(ct, "image/webp") {
		return ".webp"
	}
	if strings.HasPrefix(ct, "image/gif") {
		return ".gif"
	}
	return ".jpg"
}

func (s *Server) UploadWineImage(ctx context.Context, tipo string, num int, img []byte) (string, error) {
	if num <= 0 {
		return "", errors.New("invalid wine num")
	}
	if len(img) == 0 {
		return "", errors.New("empty image")
	}

	contentType := http.DetectContentType(img)
	ext := fileExtForContentType(contentType)
	slug := wineTypeSlug(tipo)

	objectPath := path.Join("images", "vinos", slug, strconv.Itoa(num)+ext)
	if err := s.bunnyPut(ctx, objectPath, img, contentType); err != nil {
		return "", err
	}
	return objectPath, nil
}

// UploadFoodImage uploads a food image to bunnycdn storage
// folder format: restaurants/{restaurant_id}/{type}/{num}.{ext}
func (s *Server) UploadFoodImage(ctx context.Context, restaurantID int, foodType string, num int, img []byte) (string, error) {
	if num <= 0 {
		return "", errors.New("invalid food num")
	}
	if len(img) == 0 {
		return "", errors.New("empty image")
	}

	contentType := http.DetectContentType(img)
	ext := fileExtForContentType(contentType)
	typeSlug := strings.ToLower(strings.TrimSpace(foodType))

	objectPath := path.Join("restaurants", strconv.Itoa(restaurantID), typeSlug, strconv.Itoa(num)+ext)
	if err := s.bunnyPut(ctx, objectPath, img, contentType); err != nil {
		return "", err
	}
	return objectPath, nil
}
