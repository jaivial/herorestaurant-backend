package api

import (
	"encoding/json"
	"fmt"
	"testing"
)

// --- gate: membership (wa_bot_group_membership_v1) ------------------------
//
// The privileged group hands the bot the whole forky catalog with a root
// auth. The sender must therefore be a member of that group. These tests pin
// the properties that matter:
//
//  1. a member is accepted, in both identity forms,
//  2. a non-member is rejected,
//  3. a LID is never accepted as a phone number (no crossed false positives),
//  4. when the provider cannot be reached, the turn is REJECTED (fail closed).

const (
	testPrivGroup   = "120363430262328476@g.us"
	testMemberPhone = "34692747052"
	testStranger    = "34600000000"
)

// groupMembersPayload builds a provider answer for /group/fetchAllGroups.
// The lid is a realistic Baileys opaque id: its digits are NOT the phone, so
// both forms have to be registered separately.
func groupMembersPayload(groupID string, phones ...string) []byte {
	type part struct {
		ID          string `json:"id"`
		PhoneNumber string `json:"phoneNumber"`
		Jid         string `json:"jid"`
	}
	type grp struct {
		ID           string `json:"id"`
		Subject      string `json:"subject"`
		Participants []part `json:"participants"`
	}
	g := grp{ID: groupID, Subject: "Bot Alquería"}
	for i, p := range phones {
		lid := fmt.Sprintf("2620966714819%02d@lid", i)
		g.Participants = append(g.Participants, part{ID: lid, PhoneNumber: p, Jid: p + "@s.whatsapp.net"})
	}
	b, _ := json.Marshal([]grp{g})
	return b
}

func TestBotGroupSenderInMembers(t *testing.T) {
	members := botGroupMemberPhonesFromPayload(
		groupMembersPayload(testPrivGroup, "34960255536", testMemberPhone),
		testPrivGroup,
	)
	if members.empty() {
		t.Fatal("the member list must not be empty")
	}

	cases := []struct {
		name   string
		sender string
		want   bool
	}{
		{"member by phone jid", testMemberPhone + "@s.whatsapp.net", true},
		{"member by lid", "262096671481901@lid", true},
		{"member by lid with the device suffix", "262096671481901:0@lid", true},
		{"non member is rejected", testStranger + "@s.whatsapp.net", false},
		{"empty sender is rejected", "", false},
		{"a lid is not a phone number", "34692747052@lid", false},
		{"a phone is not a lid", "262096671481901@s.whatsapp.net", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := botGroupSenderInMembers(members, tc.sender); got != tc.want {
				t.Errorf("botGroupSenderInMembers(%q) = %v, want %v", tc.sender, got, tc.want)
			}
		})
	}
}

// A provider outage must NOT grant the privileged catalog: the check fails
// closed, so the turn is served as an ordinary group turn.
func TestBotGroupSenderInMembersFailsClosedWithoutAList(t *testing.T) {
	empty := newBotGroupMembers()
	if botGroupSenderInMembers(empty, testMemberPhone+"@s.whatsapp.net") {
		t.Error("a member must not be accepted when the list could not be obtained")
	}
}

// The group in the provider answer must be the one being asked about: a
// provider that lists a different group proves nothing.
func TestBotGroupMemberPhonesIgnoresOtherGroups(t *testing.T) {
	members := botGroupMemberPhonesFromPayload(
		groupMembersPayload("999999999999@g.us", testMemberPhone),
		testPrivGroup,
	)
	if !members.empty() {
		t.Errorf("membership of another group must not be accepted, got %d entries", len(members.phones))
	}
}

// A member is registered by both its phone and its lid, because a mention
// (and a sender field) can carry either form.
func TestBotGroupMemberPhonesRegistersPhoneAndLid(t *testing.T) {
	members := botGroupMemberPhonesFromPayload(
		groupMembersPayload(testPrivGroup, testMemberPhone),
		testPrivGroup,
	)
	if _, ok := members.phones[testMemberPhone]; !ok {
		t.Error("the phone must be registered")
	}
	if _, ok := members.lids["262096671481900"]; !ok {
		t.Error("the lid must be registered too")
	}
}

// A malformed provider answer must not be read as "everyone is a member".
func TestBotGroupMemberPhonesRejectsGarbage(t *testing.T) {
	if m := botGroupMemberPhonesFromPayload([]byte("not json"), testPrivGroup); !m.empty() {
		t.Error("a malformed payload must yield no members")
	}
	if m := botGroupMemberPhonesFromPayload([]byte("[]"), testPrivGroup); !m.empty() {
		t.Error("an empty group list must yield no members")
	}
}

// The crossed false positive: a member whose LID ends in the same digits as
// another member's phone must not be accepted as that phone, and the other
// way round. This is why phones and lids are kept apart.
func TestBotGroupMembersDoNotCrossPhonesAndLids(t *testing.T) {
	raw := []byte(`[{"id":"120363430262328476@g.us","subject":"Bot Alquería","participants":[` +
		`{"id":"34692747052:0@lid","phoneNumber":"34960255536@s.whatsapp.net"}]}]`)

	members := botGroupMemberPhonesFromPayload(raw, testPrivGroup)
	// The phone of that member is 34960255536 and its lid is 34692747052.
	// "34692747052@s.whatsapp.net" is NOT that member: those digits belong to
	// the lid, and no phone matches them.
	if botGroupSenderInMembers(members, "34692747052@s.whatsapp.net") {
		t.Error("a lid must not be accepted as a phone number")
	}
	if !botGroupSenderInMembers(members, "34960255536@s.whatsapp.net") {
		t.Error("the real phone must be accepted")
	}
	if !botGroupSenderInMembers(members, "34692747052:0@lid") {
		t.Error("the real lid must be accepted")
	}
}
