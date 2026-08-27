package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

func withChiURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// PUT /api/admin/config/ads/{adId} should succeed even when the new payload
// is byte-for-byte equal to what is already stored. The previous implementation
// treated MySQL's "0 rows changed" as "ad not found", which made a save click
// on an unchanged editor surface a misleading 404 to the backoffice.
//
// Live reproduction: edit ad 8 with the same name/active/content/ctas it
// already has and expect 200, not 404.
func TestHandleBOAdsUpdateSamePayloadReturnsOK(t *testing.T) {
	dsn := os.Getenv("BO_ADS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BO_ADS_TEST_MYSQL_DSN not set")
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("BO_ADS_TEST_MYSQL_DSN must include parseTime=true (got %q)", dsn)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var databaseName string
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&databaseName); err != nil {
		t.Fatalf("read database name: %v", err)
	}
	name := strings.ToLower(databaseName)
	isTestDatabase := strings.Contains(name, "test") || strings.Contains(name, "sandbox")
	if !isTestDatabase && os.Getenv("BO_ADS_TEST_ALLOW_NON_TEST_DB") != "1" {
		t.Fatalf("refusing destructive test against database %q; use a test/sandbox database or set BO_ADS_TEST_ALLOW_NON_TEST_DB=1", databaseName)
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`DELETE FROM restaurant_ads WHERE restaurant_id = 9991`,
	); err != nil {
		t.Fatalf("clear ads: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurants (id, slug, name) VALUES (9991, 'bo-ads-update-test', 'BO Ads Update Test')
		 ON DUPLICATE KEY UPDATE slug = VALUES(slug)`,
	); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurant_ads (id, restaurant_id, name, active, content_json, ctas_json, image_generation_status, created_at, updated_at)
		 VALUES (900001, 9991, 'unchanged', 0, JSON_ARRAY(JSON_OBJECT('id','i1','type','image','value','https://cdn.example/a.webp')), JSON_ARRAY(), 'idle', NOW(), NOW())`,
	); err != nil {
		t.Fatalf("seed ad: %v", err)
	}

	s := NewServer(db, config.Config{})
	auth := boAuth{ActiveRestaurantID: 9991, Role: "root"}

	payload, err := json.Marshal(map[string]any{
		"name":    "unchanged",
		"active":  false,
		"content": []map[string]any{{"id": "i1", "type": "image", "value": "https://cdn.example/a.webp"}},
		"ctas":    []any{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/admin/config/ads/900001", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withBOAuth(req.Context(), auth))
	req = withChiURLParam(req, "adId", "900001")
	rec := httptest.NewRecorder()
	s.handleBOAdsUpdate(rec, req)

	if rec.Code != http.StatusOK {
		body, _ := io.ReadAll(rec.Body)
		t.Fatalf("expected 200 OK for no-op PUT, got %d body=%s", rec.Code, string(body))
	}

	var resp struct {
		Success bool  `json:"success"`
		Ad      boAd  `json:"ad"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got %+v", resp)
	}
	if resp.Ad.ID != 900001 {
		t.Fatalf("expected ad id 900001 in response, got %d", resp.Ad.ID)
	}
	if resp.Ad.Name != "unchanged" {
		t.Fatalf("expected name unchanged, got %q", resp.Ad.Name)
	}
}

// Same as above but the ad does not belong to the active restaurant — should
// still return 404 (the row genuinely doesn't exist for that scope).
func TestHandleBOAdsUpdateMissingAdReturns404(t *testing.T) {
	dsn := os.Getenv("BO_ADS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BO_ADS_TEST_MYSQL_DSN not set")
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("BO_ADS_TEST_MYSQL_DSN must include parseTime=true (got %q)", dsn)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var databaseName string
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&databaseName); err != nil {
		t.Fatalf("read database name: %v", err)
	}
	name := strings.ToLower(databaseName)
	isTestDatabase := strings.Contains(name, "test") || strings.Contains(name, "sandbox")
	if !isTestDatabase && os.Getenv("BO_ADS_TEST_ALLOW_NON_TEST_DB") != "1" {
		t.Fatalf("refusing destructive test against database %q; use a test/sandbox database or set BO_ADS_TEST_ALLOW_NON_TEST_DB=1", databaseName)
	}

	s := NewServer(db, config.Config{})
	auth := boAuth{ActiveRestaurantID: 9991, Role: "root"}

	payload, _ := json.Marshal(map[string]any{
		"name":    "ghost",
		"active":  false,
		"content": []any{},
		"ctas":    []any{},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/admin/config/ads/777777", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withBOAuth(req.Context(), auth))
	req = withChiURLParam(req, "adId", "777777")
	rec := httptest.NewRecorder()
	s.handleBOAdsUpdate(rec, req)

	if rec.Code != http.StatusNotFound {
		body, _ := io.ReadAll(rec.Body)
		t.Fatalf("expected 404 for missing ad, got %d body=%s", rec.Code, string(body))
	}
}

// persistBOAdImageURL must write the new URL into content_json atomically.
// User-reported bug: after clicking 'Mejorar con IA' on an ad that already
// had an image, a page reload while the AI was processing — or even just
// after the handler returned and before the front-end's follow-up PUT
// landed — showed the old image because the backend saved the new file
// to storage but never wrote the URL back into content_json.
func TestPersistBOAdImageURLReplacesExistingImageValue(t *testing.T) {
	dsn := os.Getenv("BO_ADS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BO_ADS_TEST_MYSQL_DSN not set")
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("BO_ADS_TEST_MYSQL_DSN must include parseTime=true (got %q)", dsn)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var databaseName string
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&databaseName); err != nil {
		t.Fatalf("read database name: %v", err)
	}
	name := strings.ToLower(databaseName)
	isTestDatabase := strings.Contains(name, "test") || strings.Contains(name, "sandbox")
	if !isTestDatabase && os.Getenv("BO_ADS_TEST_ALLOW_NON_TEST_DB") != "1" {
		t.Fatalf("refusing destructive test against database %q; use a test/sandbox database or set BO_ADS_TEST_ALLOW_NON_TEST_DB=1", databaseName)
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `DELETE FROM restaurant_ads WHERE restaurant_id = 9992`); err != nil {
		t.Fatalf("clear ads: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurants (id, slug, name) VALUES (9992, 'bo-ads-persist-image-test', 'BO Ads Persist Image Test')
		 ON DUPLICATE KEY UPDATE slug = VALUES(slug)`,
	); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurant_ads (id, restaurant_id, name, active, content_json, ctas_json, image_generation_status, created_at, updated_at)
		 VALUES (900002, 9992, 'replace-me', 0,
		         JSON_ARRAY(JSON_OBJECT('id','old-img','type','image','value','https://cdn.example/old.webp'),
		                    JSON_OBJECT('id','k1','type','title','value','Cena')),
		         JSON_ARRAY(), 'idle', NOW(), NOW())`,
	); err != nil {
		t.Fatalf("seed ad: %v", err)
	}

	s := NewServer(db, config.Config{})
	const newURL = "https://cdn.example/new-enhanced.webp"
	if err := s.persistBOAdImageURL(ctx, 9992, 900002, newURL); err != nil {
		t.Fatalf("persistBOAdImageURL: %v", err)
	}

	ad, err := s.readBOAd(ctx, 9992, 900002)
	if err != nil {
		t.Fatalf("readBOAd: %v", err)
	}

	var imageValue string
	titleFound := false
	for _, item := range ad.Content {
		if item.Type == "image" {
			imageValue = item.Value
		}
		if item.Type == "title" && item.Value == "Cena" {
			titleFound = true
		}
	}
	if imageValue != newURL {
		t.Fatalf("image value not replaced: got %q want %q", imageValue, newURL)
	}
	if !titleFound {
		t.Fatalf("sibling content items were wiped: %+v", ad.Content)
	}
	if got := len(ad.Content); got != 2 {
		t.Fatalf("expected 2 content items (image + title), got %d (%+v)", got, ad.Content)
	}
}

// persistBOAdImageURL on an ad without any image element should append one
// rather than failing.
func TestPersistBOAdImageURLAppendsImageWhenMissing(t *testing.T) {
	dsn := os.Getenv("BO_ADS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BO_ADS_TEST_MYSQL_DSN not set")
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("BO_ADS_TEST_MYSQL_DSN must include parseTime=true (got %q)", dsn)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var databaseName string
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&databaseName); err != nil {
		t.Fatalf("read database name: %v", err)
	}
	name := strings.ToLower(databaseName)
	isTestDatabase := strings.Contains(name, "test") || strings.Contains(name, "sandbox")
	if !isTestDatabase && os.Getenv("BO_ADS_TEST_ALLOW_NON_TEST_DB") != "1" {
		t.Fatalf("refusing destructive test against database %q; use a test/sandbox database or set BO_ADS_TEST_ALLOW_NON_TEST_DB=1", databaseName)
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `DELETE FROM restaurant_ads WHERE restaurant_id = 9993`); err != nil {
		t.Fatalf("clear ads: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurants (id, slug, name) VALUES (9993, 'bo-ads-append-image-test', 'BO Ads Append Image Test')
		 ON DUPLICATE KEY UPDATE slug = VALUES(slug)`,
	); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurant_ads (id, restaurant_id, name, active, content_json, ctas_json, image_generation_status, created_at, updated_at)
		 VALUES (900003, 9993, 'no-image', 0, JSON_ARRAY(), JSON_ARRAY(), 'idle', NOW(), NOW())`,
	); err != nil {
		t.Fatalf("seed ad: %v", err)
	}

	s := NewServer(db, config.Config{})
	const newURL = "https://cdn.example/first-image.webp"
	if err := s.persistBOAdImageURL(ctx, 9993, 900003, newURL); err != nil {
		t.Fatalf("persistBOAdImageURL: %v", err)
	}

	ad, err := s.readBOAd(ctx, 9993, 900003)
	if err != nil {
		t.Fatalf("readBOAd: %v", err)
	}
	if len(ad.Content) != 1 {
		t.Fatalf("expected exactly 1 content item, got %+v", ad.Content)
	}
	if ad.Content[0].Type != "image" || ad.Content[0].Value != newURL {
		t.Fatalf("expected appended image element with new url, got %+v", ad.Content[0])
	}
}

// setBOAdImageGenerationStatus must actually persist the status. The
// user-reported bug: clicking 'Mejorar con IA' closed the modal and the
// front-end rendered its own skeleton, but the server-side status update was
// silently a no-op — the SQL placeholders are (status, id, restaurant_id)
// while the args were passed as (status, restaurantID, adID), so the WHERE
// clause swapped the two IDs and matched zero rows. On a page reload the
// editor therefore saw status='idle' and re-rendered the OLD image instead
// of the pending skeleton.
func TestSetBOAdImageGenerationStatusPersistsToReloadedAd(t *testing.T) {
	dsn := os.Getenv("BO_ADS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BO_ADS_TEST_MYSQL_DSN not set")
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("BO_ADS_TEST_MYSQL_DSN must include parseTime=true (got %q)", dsn)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var databaseName string
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&databaseName); err != nil {
		t.Fatalf("read database name: %v", err)
	}
	name := strings.ToLower(databaseName)
	isTestDatabase := strings.Contains(name, "test") || strings.Contains(name, "sandbox")
	if !isTestDatabase && os.Getenv("BO_ADS_TEST_ALLOW_NON_TEST_DB") != "1" {
		t.Fatalf("refusing destructive test against database %q; use a test/sandbox database or set BO_ADS_TEST_ALLOW_NON_TEST_DB=1", databaseName)
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `DELETE FROM restaurant_ads WHERE restaurant_id = 9994`); err != nil {
		t.Fatalf("clear ads: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurants (id, slug, name) VALUES (9994, 'bo-ads-status-test', 'BO Ads Status Test')
		 ON DUPLICATE KEY UPDATE slug = VALUES(slug)`,
	); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO restaurant_ads (id, restaurant_id, name, active, content_json, ctas_json, image_generation_status, created_at, updated_at)
		 VALUES (900004, 9994, 'status-test', 0, JSON_ARRAY(), JSON_ARRAY(), 'idle', NOW(), NOW())`,
	); err != nil {
		t.Fatalf("seed ad: %v", err)
	}

	s := NewServer(db, config.Config{})

	// Simulate the beginning of an AI enhance: status -> pending + started_at.
	s.setBOAdImageGenerationStatus(ctx, 9994, 900004, boAdImageGenerationPending, true)

	// Re-read through the same code path a page reload uses (SSR -> readBOAd).
	ad, err := s.readBOAd(ctx, 9994, 900004)
	if err != nil {
		t.Fatalf("readBOAd after pending: %v", err)
	}
	if ad.ImageGenerationStatus != boAdImageGenerationPending {
		t.Fatalf("expected status 'pending' after setBOAdImageGenerationStatus, got %q", ad.ImageGenerationStatus)
	}
	if ad.ImageGenerationStartedAt == "" {
		t.Fatalf("expected image_generation_started_at to be set for pending status")
	}

	// Simulate completion: status -> ready (no started_at overwrite).
	s.setBOAdImageGenerationStatus(ctx, 9994, 900004, boAdImageGenerationReady, false)
	ad, err = s.readBOAd(ctx, 9994, 900004)
	if err != nil {
		t.Fatalf("readBOAd after ready: %v", err)
	}
	if ad.ImageGenerationStatus != boAdImageGenerationReady {
		t.Fatalf("expected status 'ready' after setBOAdImageGenerationStatus, got %q", ad.ImageGenerationStatus)
	}

	// started_at must survive the ready transition.
	if ad.ImageGenerationStartedAt == "" {
		t.Fatalf("expected image_generation_started_at to survive the ready transition")
	}
}
