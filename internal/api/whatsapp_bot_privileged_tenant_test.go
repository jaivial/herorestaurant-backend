package api

import (
	"testing"

	"preactvillacarmen/internal/config"
)

// The privileged group is a per-restaurant setting: the tenant config names
// it, so one restaurant's privileged group never applies to another.
func TestBotGroupIsPrivilegedPerTenant(t *testing.T) {
	const group = "120363430262328476@g.us"

	cases := []struct {
		name   string
		env    []string
		tenant botTenantConfig
		in     string
		want   bool
	}{
		{
			name:   "tenant names the group",
			tenant: botTenantConfig{PrivilegedGroupJIDs: []string{group}},
			in:     group,
			want:   true,
		},
		{
			name:   "tenant does not name the group",
			tenant: botTenantConfig{PrivilegedGroupJIDs: []string{"999@g.us"}},
			in:     group,
			want:   false,
		},
		{
			name:   "env still applies when the tenant is silent",
			env:    []string{group},
			tenant: botTenantConfig{},
			in:     group,
			want:   true,
		},
		{
			name:   "tenant adds a group the env does not list",
			env:    []string{"111@g.us"},
			tenant: botTenantConfig{PrivilegedGroupJIDs: []string{group}},
			in:     group,
			want:   true,
		},
		{
			name:   "a phone number in the tenant list never enables it",
			tenant: botTenantConfig{PrivilegedGroupJIDs: []string{"34600111222@s.whatsapp.net"}},
			in:     "34600111222@s.whatsapp.net",
			want:   false,
		},
		{
			name:   "empty tenant and empty env are always false",
			tenant: botTenantConfig{},
			in:     group,
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: config.Config{BotPrivilegedGroupJIDs: tc.env}}
			if got := s.botGroupIsPrivileged(tc.in, tc.tenant); got != tc.want {
				t.Errorf("botGroupIsPrivileged(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
