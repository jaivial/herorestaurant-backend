package api

import "testing"

func TestFindOpenAIImageURLIgnoresInputEcho(t *testing.T) {
	payload := map[string]any{
		"code":    float64(200),
		"message": "success",
		"data": map[string]any{
			"id":     "abc",
			"status": "created",
			"input": map[string]any{
				"images": []any{"https://d1q70pf5vjeyhc.cloudfront.net/media/x/images/1787056909170655448_fUBKU3cm.webp"},
			},
			"outputs": []any{},
			"urls": map[string]any{
				"get": "https://api.wavespeed.ai/api/v3/predictions/abc/result",
			},
		},
	}
	if got := findOpenAIImageURL(payload); got != "https://api.wavespeed.ai/api/v3/predictions/abc/result" {
		t.Fatalf("expected result endpoint URL, got %q", got)
	}

	completed := map[string]any{
		"data": map[string]any{
			"input": map[string]any{
				"images": []any{"https://cloudfront.example/input.webp"},
			},
			"outputs": []any{"https://cloudfront.example/output.jpeg"},
		},
	}
	if got := findOpenAIImageURL(completed); got != "https://cloudfront.example/output.jpeg" {
		t.Fatalf("expected output URL, got %q", got)
	}
}
