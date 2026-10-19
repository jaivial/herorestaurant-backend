package api

import (
	"testing"
)

// The participant list is fetched from ONE tenant's provider instance, so
// the cache key must include that instance: two restaurants can share a
// WhatsApp group (the same group holding the bots of two tenants), and their
// member lists are not the same.
//
// A key of just the group id would let the first tenant to fill it poison the
// entry for the second.
func TestBotGroupMembersCacheKeyIncludesTheInstance(t *testing.T) {
	a := botGroupMembersCacheKey("instance-a", "120363430262328476@g.us")
	b := botGroupMembersCacheKey("instance-b", "120363430262328476@g.us")
	if a == b {
		t.Errorf("the same group on two instances must not share a cache key: %q", a)
	}
	// The same pair must always produce the same key.
	if a != botGroupMembersCacheKey("instance-a", "120363430262328476@g.us") {
		t.Error("the cache key must be stable")
	}
	// A different group on the same instance is a different key too.
	c := botGroupMembersCacheKey("instance-a", "999999999999@g.us")
	if a == c {
		t.Error("two groups of the same instance must not share a cache key")
	}
	// No combination of values may be read as another combination.
	if got := botGroupMembersCacheKey("a", "b"); got == botGroupMembersCacheKey("ab", "") {
		t.Error("the key must be unambiguous")
	}
	if got := botGroupMembersCacheKey("a", "bc"); got == botGroupMembersCacheKey("ab", "c") {
		t.Error("the key must be unambiguous")
	}
}
