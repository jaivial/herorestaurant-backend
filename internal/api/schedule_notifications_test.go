package api

import "testing"

func TestScheduleChangeMessage(t *testing.T) {
	schedule := boFichajeSchedule{MemberName: "Ana", Date: "2025-03-20", StartTime: "09:00", EndTime: "17:00"}
	subject, text, html := scheduleChangeMessage("Casa", schedule, false)
	if subject != "Casa · Horario asignado" {
		t.Fatalf("subject=%q", subject)
	}
	if text != "Hola, tu horario ha sido asignado para el 2025-03-20: 09:00 - 17:00." {
		t.Fatalf("text=%q", text)
	}
	if html == "" {
		t.Fatal("expected html")
	}
	subject, _, _ = scheduleChangeMessage("Casa", schedule, true)
	if subject != "Casa · Horario actualizado" {
		t.Fatalf("updated subject=%q", subject)
	}
}
