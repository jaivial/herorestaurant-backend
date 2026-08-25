package api

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestBotConversationStorePersistsFullHistoryBeyondLegacyLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.sqlite")
	store, err := newBotConversationStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := store.Append(ctx, 1, "34692747052", role, fmt.Sprintf("m-%02d", i), "", "test"); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	got, err := store.History(ctx, 1, "34692747052")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 25 {
		t.Fatalf("history len=%d want=25", len(got))
	}
	if got[0].Content[0].Text != "m-00" || got[24].Content[0].Text != "m-24" {
		t.Fatalf("unexpected edges: first=%q last=%q", got[0].Content[0].Text, got[24].Content[0].Text)
	}
}

func TestBotConversationStoreSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.sqlite")
	ctx := context.Background()
	first, err := newBotConversationStore(path)
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	if err := first.Append(ctx, 7, "34600000001", "assistant", "Reserva confirmada", "", "booking_confirmation"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	second, err := newBotConversationStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	got, err := second.History(ctx, 7, "34600000001")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 2 || got[1].Content[0].Text != "Reserva confirmada" {
		t.Fatalf("reopened history=%+v", got)
	}
}

type trackedGatewayStub struct {
	textErr error
	menuErr error
}

func (g *trackedGatewayStub) SendText(context.Context, string, string) error { return g.textErr }
func (g *trackedGatewayStub) SendMenu(context.Context, string, string, []string) error {
	return g.menuErr
}
func (g *trackedGatewayStub) SendMedia(context.Context, string, waMedia) error       { return nil }
func (g *trackedGatewayStub) SendLocation(context.Context, string, waLocation) error { return nil }
func (g *trackedGatewayStub) SendContact(context.Context, string, waContact) error   { return nil }
func (g *trackedGatewayStub) Provision(context.Context, string) (waProvision, error) {
	return waProvision{}, nil
}
func (g *trackedGatewayStub) Connect(context.Context, string) (waConnState, error) {
	return waConnState{}, nil
}
func (g *trackedGatewayStub) Status(context.Context) (waConnState, error)             { return waConnState{}, nil }
func (g *trackedGatewayStub) Disconnect(context.Context) error                        { return nil }
func (g *trackedGatewayStub) Delete(context.Context) error                            { return nil }
func (g *trackedGatewayStub) RegisterWebhook(context.Context, string, []string) error { return nil }
func (g *trackedGatewayStub) ParseInboundMessage([]byte) (waInbound, bool)            { return waInbound{}, false }
func (g *trackedGatewayStub) ParseConnectionEvent([]byte) (waConnEvent, bool) {
	return waConnEvent{}, false
}

func TestTrackedWhatsAppSendPersistsOnlySuccessfulOutbound(t *testing.T) {
	store, err := newBotConversationStore(filepath.Join(t.TempDir(), "context.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := &Server{botConversation: store}
	ctx := context.Background()
	gw := &trackedGatewayStub{}
	if err := s.sendWhatsAppTextTracked(ctx, 1, gw, "34692747052", "Confirmada", "booking_confirmation"); err != nil {
		t.Fatalf("tracked text: %v", err)
	}
	gw.menuErr = fmt.Errorf("provider down")
	if err := s.sendWhatsAppMenuTracked(ctx, 1, gw, "34692747052", "Reconfirma", []string{"Sí", "No"}, "booking_reconfirmation"); err == nil {
		t.Fatal("expected provider failure")
	}
	got, err := store.History(ctx, 1, "34692747052")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Content[0].Text != "Confirmada" {
		t.Fatalf("history=%+v", got)
	}
}

func TestBotConversationStoreKeepsSecurityMessagesOutOfLLMHistory(t *testing.T) {
	store, err := newBotConversationStore(filepath.Join(t.TempDir(), "context.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Append(ctx, 1, "34692747052", "assistant", "reset-token-secret", "", "security_password_reset"); err != nil {
		t.Fatal(err)
	}
	var persisted int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_messages WHERE restaurant_id=1 AND user_phone='34692747052'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 1 {
		t.Fatalf("persisted=%d want=1", persisted)
	}
	history, err := store.History(ctx, 1, "34692747052")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("security message leaked into LLM history: %+v", history)
	}
}
