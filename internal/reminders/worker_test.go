package reminders

import (
	"context"
	"errors"
	"testing"
	"time"
)

type st struct {
	shifts   []Shift
	keys     map[string]bool
	released []string
}

func (s *st) DueShifts(context.Context, time.Time, time.Duration) ([]Shift, error) {
	return s.shifts, nil
}
func (s *st) Claim(_ context.Context, k string) (bool, error) {
	if s.keys[k] {
		return false, nil
	}
	s.keys[k] = true
	return true, nil
}
func (s *st) Release(_ context.Context, k string) error {
	delete(s.keys, k)
	s.released = append(s.released, k)
	return nil
}

type snd struct {
	n   int
	err error
}

func (s *snd) Send(context.Context, int, string, string) error { s.n++; return s.err }
func TestRunOnceIsIdempotent(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	db := &st{keys: map[string]bool{}, shifts: []Shift{{ID: 2, RestaurantID: 1, Phone: "34600111222", StartAt: now.Add(10 * time.Minute)}}}
	out := &snd{}
	w := Worker{Store: db, Sender: out, Now: func() time.Time { return now }, Config: Config{Lead: 10 * time.Minute}}
	n, e := w.RunOnce(context.Background())
	if e != nil || n != 1 || out.n != 1 {
		t.Fatalf("first n=%d e=%v sends=%d", n, e, out.n)
	}
	n, e = w.RunOnce(context.Background())
	if e != nil || n != 0 || out.n != 1 {
		t.Fatalf("duplicate n=%d e=%v sends=%d", n, e, out.n)
	}
}
func TestFailedSendCanRetry(t *testing.T) {
	db := &st{keys: map[string]bool{}, shifts: []Shift{{ID: 3, RestaurantID: 1, Phone: "346", StartAt: time.Now()}}}
	out := &snd{err: errors.New("down")}
	w := Worker{Store: db, Sender: out, Config: Config{Lead: 5 * time.Minute}}
	if _, e := w.RunOnce(context.Background()); e == nil {
		t.Fatal("expected error")
	}
	if len(db.released) != 1 {
		t.Fatal("claim not released")
	}
}
func TestGateSkips(t *testing.T) {
	db := &st{keys: map[string]bool{}, shifts: []Shift{{ID: 4, RestaurantID: 1, Phone: "346"}}}
	out := &snd{}
	w := Worker{Store: db, Sender: out, Gate: deny{}, Config: Config{Lead: 5 * time.Minute}}
	n, _ := w.RunOnce(context.Background())
	if n != 0 || out.n != 0 {
		t.Fatal("sent gated shift")
	}
}

type deny struct{}

func (deny) Allowed(context.Context, int, Shift) (bool, error) { return false, nil }
