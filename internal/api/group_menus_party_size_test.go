package api

import "testing"

func TestIsPartySizeClosedGroupMenuType(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "closed_group", raw: "closed_group", want: true},
		{name: "closed_group uppercase and spaces", raw: "  CLOSED_GROUP  ", want: true},
		{name: "closed conventional", raw: "closed_conventional", want: false},
		{name: "empty defaults away from closed group", raw: "", want: false},
		{name: "a la carte group is excluded", raw: "a_la_carte_group", want: false},
		{name: "special is excluded", raw: "special", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPartySizeClosedGroupMenuType(tt.raw); got != tt.want {
				t.Fatalf("isPartySizeClosedGroupMenuType(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
