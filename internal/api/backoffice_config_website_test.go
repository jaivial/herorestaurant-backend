package api

import "testing"

func TestNormalizeRestaurantWebsiteURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare domain", in: " example.com/ ", want: "https://example.com"},
		{name: "http becomes https", in: "http://Example.com/", want: "https://example.com"},
		{name: "path preserved", in: "https://example.com/site/", want: "https://example.com/site"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeRestaurantWebsiteURL(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("normalizeRestaurantWebsiteURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}

	if _, err := normalizeRestaurantWebsiteURL("https://example.com/?redirect=bad"); err == nil {
		t.Fatal("query string should be rejected")
	}
}
