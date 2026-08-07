package api

import (
	"testing"
	"time"
)

func TestConfirmationOneShotAndBinding(t *testing.T) {
	s := newConfirmationStore()
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
	s := newConfirmationStore()
	tok, _ := s.Issue("u", "r", "tool", "args", "sid", time.Minute)
	if e := s.Consume(tok, "u", "other", "tool", "args", "sid"); e == nil {
		t.Fatal("cross tenant token accepted")
	}
}
