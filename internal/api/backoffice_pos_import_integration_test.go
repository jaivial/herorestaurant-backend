package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

// Regression: comida_items uses utf8mb4_unicode_ci while VINOS uses
// utf8mb4_general_ci. The import-preview UNION must not fail with
// "Illegal mix of collations" and must list both sources.
func TestPOSImportPreviewMixedCollations(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	statements := []string{
		`DELETE FROM pos_products`, `DELETE FROM pos_product_categories`, `DELETE FROM comida_items`, `DELETE FROM VINOS`, `DELETE FROM restaurants`,
		`INSERT INTO restaurants(id) VALUES(1)`,
		// Force the collation mismatch found in production data.
		`ALTER TABLE comida_items MODIFY nombre VARCHAR(255) COLLATE utf8mb4_unicode_ci NOT NULL, MODIFY categoria VARCHAR(100) COLLATE utf8mb4_unicode_ci NULL`,
		`ALTER TABLE VINOS MODIFY nombre VARCHAR(255) COLLATE utf8mb4_general_ci NOT NULL, MODIFY tipo VARCHAR(100) COLLATE utf8mb4_general_ci NULL`,
		`INSERT INTO comida_items(id,restaurant_id,source_type,nombre,precio,categoria,active) VALUES(8,1,'platos','Arroz a banda',12.50,'Arroces',1)`,
		// Production data has 255-char names; pos_products.name is varchar(180).
		`INSERT INTO comida_items(id,restaurant_id,source_type,nombre,precio,categoria,active) VALUES(9,1,'platos',REPEAT('Lasaña ',36),14.00,'Principales',1)`,
		`INSERT INTO VINOS(num,restaurant_id,nombre,precio,tipo,active) VALUES(22,1,'MIÑAXOIA GODELLO',21.00,'BLANCO',1)`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	s := NewServer(db, config.Config{})
	request := httptest.NewRequest(http.MethodPost, "/admin/pos/products/import-preview", strings.NewReader(""))
	request = request.WithContext(withBOAuth(request.Context(), boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
	recorder := httptest.NewRecorder()
	s.handleBOPOSImportPreview(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool `json:"success"`
		Items   []struct {
			SourceType string `json:"sourceType"`
			Name       string `json:"name"`
		} `json:"items"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || len(response.Items) != 3 {
		t.Fatalf("expected 3 preview items, got %+v", response)
	}
	// Confirm import works end-to-end with the same mixed collations.
	confirmBody := `{"items":[{"sourceType":"COMIDA_ITEM","sourceId":8},{"sourceType":"COMIDA_ITEM","sourceId":9},{"sourceType":"VINO","sourceId":22}]}`
	confirmRequest := httptest.NewRequest(http.MethodPost, "/admin/pos/products/import-confirm", strings.NewReader(confirmBody))
	routeCtx := chi.NewRouteContext()
	ctxWithRoute := context.WithValue(confirmRequest.Context(), chi.RouteCtxKey, routeCtx)
	confirmRequest = confirmRequest.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
	confirmRecorder := httptest.NewRecorder()
	s.handleBOPOSImportConfirm(confirmRecorder, confirmRequest)
	if confirmRecorder.Code != http.StatusOK {
		t.Fatalf("confirm code=%d body=%s", confirmRecorder.Code, confirmRecorder.Body.String())
	}
	var products int
	if err = db.QueryRow(`SELECT COUNT(*) FROM pos_products WHERE restaurant_id=1`).Scan(&products); err != nil {
		t.Fatal(err)
	}
	if products != 3 {
		t.Fatalf("expected 3 imported products, got %d", products)
	}
	// Category linking must also survive the collation mismatch (pc.name=v.tipo).
	var uncategorized int
	if err = db.QueryRow(`SELECT COUNT(*) FROM pos_products WHERE restaurant_id=1 AND category_id IS NULL`).Scan(&uncategorized); err != nil {
		t.Fatal(err)
	}
	if uncategorized != 0 {
		t.Fatalf("expected all products categorized, got %d uncategorized", uncategorized)
	}
}
