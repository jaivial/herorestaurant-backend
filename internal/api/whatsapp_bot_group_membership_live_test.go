package api

import (
	"context"
	"os"
	"testing"
)

// The membership check talks to the provider over HTTP. The provider needs
// the apikey header, and uazapiJSONRequest does not add it by itself: a
// missing header comes back as 401 and the membership cannot be proven.
//
// This test runs only when a live provider is pointed at with the environment
// variables below, so it never runs in CI by accident:
//
//	BOT_TEST_PROVIDER_URL=http://127.0.0.1:8114
//	BOT_TEST_PROVIDER_KEY=<apikey>
//	BOT_TEST_PROVIDER_INSTANCE=<instance>
//	BOT_TEST_GROUP_JID=<group@g.us>
func TestBotGroupMembersAgainstLiveProvider(t *testing.T) {
	base := os.Getenv("BOT_TEST_PROVIDER_URL")
	key := os.Getenv("BOT_TEST_PROVIDER_KEY")
	instance := os.Getenv("BOT_TEST_PROVIDER_INSTANCE")
	group := os.Getenv("BOT_TEST_GROUP_JID")
	if base == "" || key == "" || instance == "" || group == "" {
		t.Skip("set BOT_TEST_PROVIDER_URL/KEY/INSTANCE/GROUP_JID to run the live provider check")
	}

	// A nil payload must not yield members: the check has to fail closed.
	if m := botGroupMemberPhonesFromPayload(nil, group); !m.empty() {
		t.Fatal("a nil payload must not yield members")
	}

	// Exercise the real HTTP call through the same helper the gate uses.
	raw := botProviderFetchAllGroups(context.Background(), base, key, instance)
	if len(raw) == 0 {
		t.Fatal("the provider answered nothing: the apikey header or the endpoint is wrong")
	}
	members := botGroupMemberPhonesFromPayload(raw, group)
	if members.empty() {
		t.Fatalf("the provider answered but no member of %s was found: %s", group, string(raw))
	}
	t.Logf("members of %s: %d phones, %d lids", group, len(members.phones), len(members.lids))
}
