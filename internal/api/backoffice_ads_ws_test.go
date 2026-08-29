package api

import (
	"errors"
	"testing"
)

func TestClassifyBOAdAIError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"insufficient credits exact", errors.New(`wavespeed request failed (400): {"code":400,"message":"Insufficient credits. Please top up your account to continue."}`), boAdAIErrorInsufficientCredits},
		{"insufficient credits mixed case", errors.New("Wavespeed: INSUFFICIENT CREDITS, please Top Up Your Account"), boAdAIErrorInsufficientCredits},
		{"generic provider failure", errors.New("wavespeed request failed (502): error code: 502"), boAdAIErrorGeneric},
		{"nil error", nil, boAdAIErrorGeneric},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := classifyBOAdAIError(tc.err)
			if got != tc.want {
				t.Fatalf("code = %q, want %q (detail=%q)", got, tc.want, detail)
			}
		})
	}
}
