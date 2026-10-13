package api

import "testing"

// Coordination id: forky_media_transcribe_v1
func TestAssistantMediaSafeExt(t *testing.T) {
	cases := []struct {
		name string
		ct   string
		want string
	}{
		{"clip.webm", "audio/webm", ".webm"},
		{"voice", "audio/ogg", ".ogg"},
		{"note.mp4", "audio/mp4", ".mp4"},
		{"photo", "image/png", ".png"},
		{"scan.pdf", "application/pdf", ".pdf"},
		{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
		{"blob", "application/octet-stream", ".bin"},
		{"evil.exe", "application/x-msdownload", ".bin"},
		{"tricky.png.exe", "application/octet-stream", ".bin"},
	}
	for _, tc := range cases {
		if got := assistantMediaSafeExt(tc.name, tc.ct); got != tc.want {
			t.Fatalf("assistantMediaSafeExt(%q,%q) = %q, want %q", tc.name, tc.ct, got, tc.want)
		}
	}
}
