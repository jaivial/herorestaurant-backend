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

func (s *Server) bunnyConfigForContext(ctx context.Context) (bunnyCDNConfig, bool) {
	restaurantID, ok := restaurantIDFromContext(ctx)
	if !ok {
		return bunnyCDNConfig{}, false
	}
	cfg, err := s.loadBunnyCDNConfig(ctx, restaurantID)
	return cfg, err == nil
}

func (s *Server) bunnyConfiguredContext(ctx context.Context) bool {
	cfg, ok := s.bunnyConfigForContext(ctx)
	return ok && bunnyPublicConfigured(cfg)
}

func (s *Server) bunnyPrivateConfiguredContext(ctx context.Context) bool {
	cfg, ok := s.bunnyConfigForContext(ctx)
	return ok && bunnyPrivateConfiguredForConfig(cfg)
}

func (s *Server) bunnyMembersConfiguredContext(ctx context.Context) bool {
	cfg, ok := s.bunnyConfigForContext(ctx)
	return ok && bunnyMembersConfiguredForConfig(cfg)
}

func (s *Server) bunnyPullURL(ctx context.Context, objectPath string) string {
	cfg, ok := s.bunnyConfigForContext(ctx)
	if !ok {
		return ""
	}
	base := strings.TrimRight(cfg.PublicPullBaseURL, "/")
	p := strings.TrimLeft(objectPath, "/")
	return base + "/" + p
}

func (s *Server) bunnyMembersPullURL(ctx context.Context, objectPath string) string {
	cfg, ok := s.bunnyConfigForContext(ctx)
	if !ok {
		return ""
	}
	base := strings.TrimRight(cfg.MemberPullBaseURL, "/")
	p := strings.TrimLeft(objectPath, "/")
	return base + "/" + p
}

func (s *Server) bunnyPut(ctx context.Context, objectPath string, payload []byte, contentType string) error {
	cfg, ok := s.bunnyConfigForContext(ctx)
	if !ok || !bunnyPublicConfigured(cfg) {
		return errors.New("BunnyCDN storage not configured")
	}
	return bunnyPutWithCredentials(ctx, cfg.PublicStorageZone, cfg.PublicStorageAccessKey, objectPath, payload, contentType)
}

func (s *Server) bunnyPrivatePut(ctx context.Context, objectPath string, payload []byte, contentType string) error {
	cfg, ok := s.bunnyConfigForContext(ctx)
	if !ok || !bunnyPrivateConfiguredForConfig(cfg) {
		return errors.New("BunnyCDN private storage not configured")
	}
	return bunnyPutWithCredentials(ctx, cfg.PrivateStorageZone, cfg.PrivateStorageAccessKey, objectPath, payload, contentType)
}

func (s *Server) bunnyPrivateGet(ctx context.Context, objectPath string) ([]byte, string, error) {
	cfg, ok := s.bunnyConfigForContext(ctx)
	if !ok || !bunnyPrivateConfiguredForConfig(cfg) {
		return nil, "", errors.New("BunnyCDN private storage not configured")
	}
	u := "https://storage.bunnycdn.com/" + url.PathEscape(cfg.PrivateStorageZone) + "/" + bunnyEscapePath(objectPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("AccessKey", cfg.PrivateStorageAccessKey)
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("bunny private download failed (%d)", res.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(res.Body, stockDocumentMaxBytes+1))
	if err != nil || len(payload) > stockDocumentMaxBytes {
		return nil, "", errors.New("invalid private document payload")
	}
	return payload, res.Header.Get("Content-Type"), nil
}

func (s *Server) bunnyPrivateDelete(ctx context.Context, objectPath string) error {
	cfg, ok := s.bunnyConfigForContext(ctx)
	if !ok || !bunnyPrivateConfiguredForConfig(cfg) {
		return errors.New("BunnyCDN private storage not configured")
	}
	return bunnyDeleteWithCredentials(ctx, cfg.PrivateStorageZone, cfg.PrivateStorageAccessKey, objectPath)
}

func (s *Server) bunnyMembersPut(ctx context.Context, objectPath string, payload []byte, contentType string) error {
	cfg, ok := s.bunnyConfigForContext(ctx)
	if !ok || !bunnyMembersConfiguredForConfig(cfg) {
		return errors.New("BunnyCDN member storage not configured")
	}
	return bunnyPutWithCredentials(ctx, cfg.MemberStorageZone, cfg.MemberStorageAccessKey, objectPath, payload, contentType)
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

func (s *Server) UploadWineImageV2(ctx context.Context, tipo string, num int, img []byte, contentType string) (string, error) {
	if num <= 0 {
		return "", errors.New("invalid wine num")
	}
	if len(img) == 0 {
		return "", errors.New("empty image")
	}
	slug := wineTypeSlug(tipo)
	generationVersion := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	fileName := strconv.Itoa(num) + "-" + generationVersion + ".webp"
	objectPath := path.Join("images", "vinos", slug, fileName)
	if err := s.bunnyPut(ctx, objectPath, img, contentType); err != nil {
		return "", err
	}
	return objectPath, nil
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
