// Package reminders delivers idempotent pre-shift notifications.
package reminders

import (
	"context"
	"fmt"
	"time"
)

type Shift struct {
	ID                int64
	RestaurantID      int
	MemberID          int64
	MemberName, Phone string
	StartAt           time.Time
}

// Store supplies shifts and atomically claims delivery keys. Claim must return
// false when a key has already been successfully claimed (including retries by
// another process).
type Store interface {
	DueShifts(context.Context, time.Time, time.Duration) ([]Shift, error)
	Claim(context.Context, string) (bool, error)
}
type Releaser interface {
	Release(context.Context, string) error
}
type Sender interface {
	Send(context.Context, int, string, string) error
}
type Gate interface {
	Allowed(context.Context, int, Shift) (bool, error)
}
type GateFunc func(context.Context, int, Shift) (bool, error)

func (f GateFunc) Allowed(ctx context.Context, restaurantID int, shift Shift) (bool, error) {
	return f(ctx, restaurantID, shift)
}

type AllowAll struct{}

func (AllowAll) Allowed(context.Context, int, Shift) (bool, error) { return true, nil }

type Config struct {
	Lead      time.Duration
	ScanEvery time.Duration
}
type Worker struct {
	Store  Store
	Sender Sender
	Gate   Gate
	Now    func() time.Time
	Config Config
}

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	if w.Store == nil || w.Sender == nil {
		return 0, fmt.Errorf("reminders: store and sender are required")
	}
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	lead := w.Config.Lead
	if lead <= 0 {
		lead = 10 * time.Minute
	}
	shifts, err := w.Store.DueShifts(ctx, now, lead)
	if err != nil {
		return 0, err
	}
	sent := 0
	gate := w.Gate
	if gate == nil {
		gate = AllowAll{}
	}
	for _, sh := range shifts {
		if sh.ID <= 0 || sh.RestaurantID <= 0 || sh.Phone == "" {
			continue
		}
		allowed, e := gate.Allowed(ctx, sh.RestaurantID, sh)
		if e != nil {
			return sent, e
		}
		if !allowed {
			continue
		}
		key := DeliveryKey(sh, lead)
		claimed, e := w.Store.Claim(ctx, key)
		if e != nil {
			return sent, e
		}
		if !claimed {
			continue
		}
		text := Message(sh, lead)
		if e = w.Sender.Send(ctx, sh.RestaurantID, sh.Phone, text); e != nil {
			if r, ok := w.Store.(Releaser); ok {
				_ = r.Release(ctx, key)
			}
			return sent, e
		}
		sent++
	}
	return sent, nil
}
func DeliveryKey(s Shift, lead time.Duration) string {
	return fmt.Sprintf("pre_shift:%d:%d", s.RestaurantID, s.ID)
}
func Message(s Shift, lead time.Duration) string {
	name := s.MemberName
	if name == "" {
		name = "equipo"
	}
	return fmt.Sprintf("Hola %s, recuerda que tu turno comienza a las %s.", name, s.StartAt.Format("15:04"))
}
func (w *Worker) Run(ctx context.Context) {
	every := w.Config.ScanEvery
	if every <= 0 {
		every = time.Minute
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, e := w.RunOnce(ctx); e != nil { /* caller owns logging */
			}
		}
	}
}
