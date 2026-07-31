package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"preactvillacarmen/internal/config"
)

func TestStockOCRProviderNameNormalizesConfiguredValues(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "minimax"},
		{" MiniMax ", "minimax"},
		{"PADDLEOCR", "paddleocr"},
		{"paddleocr-vl", "paddleocr"},
		{"unknown", "unknown"},
	}
	for _, tc := range cases {
		if got := stockOCRProviderName(tc.input); got != tc.want {
			t.Fatalf("stockOCRProviderName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPaddleOCRExtractorSendsMultipartDocumentToLocalGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/extract" {
			t.Fatalf("unexpected gateway request: %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("expected multipart request, got %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if got := r.FormValue("documentType"); got != "INVOICE" {
			t.Fatalf("documentType = %q", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("file: %v", err)
		}
		defer file.Close()
		payload, err := io.ReadAll(file)
		if err != nil || string(payload) != "png-payload" || header.Filename != "invoice.png" {
			t.Fatalf("file payload=%q filename=%q err=%v", payload, header.Filename, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"model":   "PaddleOCR-VL-1.6",
			"rawText": "Proveedor: Local Foods",
			"extraction": map[string]any{
				"supplierName":   "Local Foods",
				"documentNumber": "F-42",
				"documentDate":   "2026-07-30",
				"confidence":     0.94,
				"lines": []map[string]any{{
					"description": "Harina",
					"quantity":    2,
					"unit":        "kg",
					"unitPrice":   1.5,
					"total":       3,
					"confidence":  0.9,
				}},
			},
		})
	}))
	defer server.Close()

	extractor := newPaddleOCRExtractor(config.Config{
		PaddleOCRGatewayURL: server.URL,
		PaddleOCRModel:      "PaddleOCR-VL-1.6",
	})
	result, err := extractor.Extract(context.Background(), "INVOICE", "image/png", "invoice.png", []byte("png-payload"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "PaddleOCR-VL-1.6" || result.RawText != "Proveedor: Local Foods" {
		t.Fatalf("unexpected provider result: %#v", result)
	}
	if result.Extraction.SupplierName != "Local Foods" || len(result.Extraction.Lines) != 1 {
		t.Fatalf("unexpected extraction: %#v", result.Extraction)
	}
}

func TestPaddleOCRExtractorRejectsUnsuccessfulGatewayResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"success":false,"message":"model unavailable"}`)
	}))
	defer server.Close()

	extractor := newPaddleOCRExtractor(config.Config{PaddleOCRGatewayURL: server.URL})
	_, err := extractor.Extract(context.Background(), "INVOICE", "image/png", "invoice.png", []byte("png-payload"))
	if err == nil || !strings.Contains(err.Error(), "paddleocr gateway") {
		t.Fatalf("expected gateway error, got %v", err)
	}
}
