package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func validTestEventInquiry() eventInquiryRequest {
	return eventInquiryRequest{Name: "María <Prueba>", Email: "guest@example.com", EmailRepeat: "guest@example.com", CountryCode: "34", Phone: "612345678", Adults: 20, Children: 3, EventType: "wedding", Date: time.Now().AddDate(0, 1, 0).Format("2006-01-02"), Notes: "Sin gluten", Language: "es"}
}

func TestValidateEventInquiry(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	valid := validTestEventInquiry()
	valid.Date = "2026-11-22"
	got, message := validateEventInquiry(valid, now)
	if message != "" || got.PhoneE164 != "34612345678" || got.EventName != "Boda" {
		t.Fatalf("valid inquiry: %+v, %q", got, message)
	}
	cases := []struct {
		name   string
		mutate func(*eventInquiryRequest)
	}{
		{"name", func(in *eventInquiryRequest) { in.Name = " " }},
		{"email", func(in *eventInquiryRequest) { in.Email = "bad" }},
		{"email repeat", func(in *eventInquiryRequest) { in.EmailRepeat = "other@example.com" }},
		{"phone", func(in *eventInquiryRequest) { in.Phone = "12" }},
		{"adults", func(in *eventInquiryRequest) { in.Adults = 0 }},
		{"children", func(in *eventInquiryRequest) { in.Children = -1 }},
		{"capacity", func(in *eventInquiryRequest) { in.Adults, in.Children = 499, 2 }},
		{"type", func(in *eventInquiryRequest) { in.EventType = "other" }},
		{"past", func(in *eventInquiryRequest) { in.Date = "2026-10-21" }},
		{"too far", func(in *eventInquiryRequest) { in.Date = "2028-10-23" }},
		{"notes", func(in *eventInquiryRequest) { in.Notes = strings.Repeat("a", 2001) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.mutate(&in)
			if _, msg := validateEventInquiry(in, now); msg == "" {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestEventInquiryTemplatesEscapeAndDisclaim(t *testing.T) {
	in, message := validateEventInquiry(validTestEventInquiry(), time.Now())
	if message != "" {
		t.Fatal(message)
	}
	html := buildEventInquiryEmail("Villa Carmen", "https://cdn.example/logo.png", in, true, restaurantBrandingCfg{})
	if strings.Contains(html, "María <Prueba>") || !strings.Contains(html, "María &lt;Prueba&gt;") {
		t.Fatal("customer data was not HTML escaped")
	}
	if !strings.Contains(html, "NO una reserva") || !strings.Contains(html, "Sin gluten") {
		t.Fatal("missing disclaimer or details")
	}
	english := validTestEventInquiry()
	english.Language = "en"
	normalized, _ := validateEventInquiry(english, time.Now())
	if !strings.Contains(eventInquiryWhatsApp(normalized, false), "NOT a reservation") {
		t.Fatal("missing English WhatsApp disclaimer")
	}
}

func TestPublicEventInquirySandboxDelivery(t *testing.T) {
	oldConfig, oldBranding, oldEmail, oldGateway, oldWhatsApp := eventInquiryEmailConfig, eventInquiryBranding, eventInquirySendEmail, eventInquiryGateway, eventInquirySendWhatsApp
	t.Cleanup(func() {
		eventInquiryEmailConfig, eventInquiryBranding, eventInquirySendEmail, eventInquiryGateway, eventInquirySendWhatsApp = oldConfig, oldBranding, oldEmail, oldGateway, oldWhatsApp
	})
	var emails []string
	var whatsApps []string
	eventInquiryEmailConfig = func(context.Context, *Server, int) (boEmailProviderConfig, error) {
		return boEmailProviderConfig{ID: 1, Provider: "gmail", IsActive: true, GmailFromEmail: "sender@example.com"}, nil
	}
	eventInquiryBranding = func(context.Context, *Server, int) (restaurantBrandingCfg, error) {
		return restaurantBrandingCfg{BrandName: "Villa Carmen", Email: "events@example.com"}, nil
	}
	eventInquirySendEmail = func(_ context.Context, _ boEmailProviderConfig, _, _, to, _, body string) error {
		emails = append(emails, to+" "+body)
		return nil
	}
	eventInquiryGateway = func(context.Context, *Server, int) (WhatsAppGateway, bool) { return &trackedGatewayStub{}, true }
	eventInquirySendWhatsApp = func(_ context.Context, _ *Server, id int, _ WhatsAppGateway, to, body string) error {
		if id != 1 {
			return fmt.Errorf("wrong restaurant")
		}
		whatsApps = append(whatsApps, to+" "+body)
		return nil
	}
	s := &Server{rateLimit: make(map[string]*rateLimitState)}
	t.Setenv("DEFAULT_RESTAURANT_ID", "1")
	valid := validTestEventInquiry()
	body, _ := json.Marshal(valid)
	req := httptest.NewRequest(http.MethodPost, "/api/public/event-inquiries", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handlePublicEventInquiry(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(emails) != 2 || !strings.HasPrefix(emails[0], "guest@example.com ") || !strings.HasPrefix(emails[1], "events@example.com ") {
		t.Fatalf("email recipients=%d %#v", len(emails), emails)
	}
	if len(whatsApps) != 4 {
		t.Fatalf("WhatsApp calls=%d", len(whatsApps))
	}
	for i, phone := range append([]string{"34612345678"}, eventInquiryInternalWhatsApp...) {
		if !strings.HasPrefix(whatsApps[i], phone+" ") || !strings.Contains(whatsApps[i], "NO una reserva") {
			t.Fatalf("WhatsApp %d: %q", i, whatsApps[i])
		}
	}
	invalid := valid
	invalid.EmailRepeat = "different@example.com"
	body, _ = json.Marshal(invalid)
	rec = httptest.NewRecorder()
	s.handlePublicEventInquiry(rec, httptest.NewRequest(http.MethodPost, "/api/public/event-inquiries", bytes.NewReader(body)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid status=%d", rec.Code)
	}
	if len(emails) != 2 || len(whatsApps) != 4 {
		t.Fatal("invalid inquiry triggered a provider")
	}
}
