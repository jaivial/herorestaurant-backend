package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/config"
)

type stockOCRResult struct {
	Model      string                  `json:"model"`
	RawText    string                  `json:"rawText"`
	Extraction stockDocumentExtraction `json:"extraction"`
}

type stockOCRExtractor interface {
	Extract(context.Context, string, string, string, []byte) (stockOCRResult, error)
}

type paddleOCRExtractor struct {
	baseURL string
	model   string
	client  *http.Client
}

func stockOCRProviderName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "minimax":
		return "minimax"
	case "paddleocr", "paddleocr-vl", "paddleocr_vl":
		return "paddleocr"
	default:
		return value
	}
}

func newPaddleOCRExtractor(cfg config.Config) stockOCRExtractor {
	timeout := cfg.PaddleOCRTimeout
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &paddleOCRExtractor{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.PaddleOCRGatewayURL), "/"),
		model:   strings.TrimSpace(cfg.PaddleOCRModel),
		client:  &http.Client{Timeout: timeout},
	}
}

func (p *paddleOCRExtractor) Extract(ctx context.Context, documentType, mediaType, filename string, payload []byte) (stockOCRResult, error) {
	if p.baseURL == "" {
		return stockOCRResult{}, fmt.Errorf("paddleocr gateway is not configured")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("documentType", documentType); err != nil {
		return stockOCRResult{}, err
	}
	if err := writer.WriteField("model", p.model); err != nil {
		return stockOCRResult{}, err
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return stockOCRResult{}, err
	}
	if _, err = part.Write(payload); err != nil {
		return stockOCRResult{}, err
	}
	if err = writer.Close(); err != nil {
		return stockOCRResult{}, err
	}

	endpoint := strings.TrimRight(p.baseURL, "/") + "/extract"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return stockOCRResult{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return stockOCRResult{}, err
	}
	defer res.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return stockOCRResult{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return stockOCRResult{}, fmt.Errorf("paddleocr gateway http %d: %s", res.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var envelope struct {
		Success    bool                    `json:"success"`
		Message    string                  `json:"message"`
		Model      string                  `json:"model"`
		RawText    string                  `json:"rawText"`
		Extraction stockDocumentExtraction `json:"extraction"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return stockOCRResult{}, fmt.Errorf("decode paddleocr gateway response: %w", err)
	}
	if !envelope.Success {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = "request failed"
		}
		return stockOCRResult{}, fmt.Errorf("paddleocr gateway: %s", message)
	}
	model := strings.TrimSpace(envelope.Model)
	if model == "" {
		model = p.model
	}
	return stockOCRResult{Model: model, RawText: envelope.RawText, Extraction: envelope.Extraction}, nil
}
