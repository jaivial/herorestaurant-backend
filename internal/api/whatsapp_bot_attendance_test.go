package api

import "testing"

func TestBotAttendanceCommandAliases(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"start", "start"},
		{"iniciar", "start"},
		{"stop", "stop"},
		{"detener", "stop"},
		{"status", "status"},
		{"estado", "status"},
		{"fichaje", "status"},
		// Commands may be sent with the optional slash and ordinary surrounding
		// whitespace; recognition remains exact after normalization.
		{" /START ", "start"},
		{"/detener", "stop"},
	}

	for _, tt := range tests {
		got, ok := botAttendanceCommand(tt.text)
		if !ok || got != tt.want {
			t.Errorf("botAttendanceCommand(%q) = (%q, %t), want (%q, true)", tt.text, got, ok, tt.want)
		}
	}
}

func TestBotAttendanceCommandRejectsNonExactCommands(t *testing.T) {
	for _, text := range []string{
		"", "hello", "start now", "starting", "stop!", "estado ahora",
		"//start", "/ start", "fichajes", "iniciarr",
	} {
		if got, ok := botAttendanceCommand(text); ok {
			t.Errorf("botAttendanceCommand(%q) = (%q, true), want unrecognized", text, got)
		}
	}
}
