package api

import (
	"testing"
	"time"
)

func TestConfirmationOneShotAndBinding(t *testing.T) {
	s := newConfirmationStore(nil)
	tok, e := s.Issue("u", "r", "tool", "args", "sid", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Consume(tok, "u", "r", "tool", "args", "sid"); e != nil {
		t.Fatal(e)
	}
	if e = s.Consume(tok, "u", "r", "tool", "args", "sid"); e == nil {
		t.Fatal("replay accepted")
	}
}
func TestConfirmationBinding(t *testing.T) {
	s := newConfirmationStore(nil)
	tok, _ := s.Issue("u", "r", "tool", "args", "sid", time.Minute)
	if e := s.Consume(tok, "u", "other", "tool", "args", "sid"); e == nil {
		t.Fatal("cross tenant token accepted")
	}
}

func TestConfirmationArgumentsCanonicalAndBound(t *testing.T) {
	s := newConfirmationStore(nil)
	a := confirmationArguments([]byte(`{"booking_id":4,"confirmed":false}`))
	tok, _ := s.Issue("u", "r", "delete_booking", a, "sid", time.Minute)
	if e := s.Consume(tok, "u", "r", "delete_booking", confirmationArguments([]byte(`{"booking_id":5,"confirmed":true,"confirmation_token":"x"}`)), "sid"); e == nil {
		t.Fatal("argument change accepted")
	}
}
