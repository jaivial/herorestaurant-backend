package api

import "testing"

// Baileys appends a device suffix to the LID ("242554100576489:0@lid"), so
// the same member can arrive with or without it. Both forms must resolve to
// the same identity, otherwise the membership check depends on which form the
// provider used that day.
func TestBotGroupLidDigitsStripsTheDeviceSuffix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"242554100576489:0@lid", "242554100576489"},
		{"242554100576489@lid", "242554100576489"},
		{"242554100576489:12@lid", "242554100576489"},
		{"", ""},
		{"no-digits", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := botGroupLidDigits(tc.in); got != tc.want {
				t.Errorf("botGroupLidDigits(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The phone form only accepts a real phone jid: the digits of an opaque LID
// are not a phone number, and treating them as one is what lets a lid be
// mistaken for somebody's number.
func TestBotGroupPhoneDigitsRefusesOtherKinds(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"34692747052@s.whatsapp.net", "34692747052"},
		{"+34692747052@s.whatsapp.net", "34692747052"},
		{"262096671481918:0@lid", ""},
		{"262096671481918@lid", ""},
		{"120363430262328476@g.us", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := botGroupPhoneDigits(tc.in); got != tc.want {
				t.Errorf("botGroupPhoneDigits(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A member registered by the provider with a device suffix must still be
// recognised when the sender arrives without it, and the other way round.
func TestBotGroupMemberMatchesAcrossLidForms(t *testing.T) {
	// This is the shape the real provider answers with: the id carries the
	// device suffix and the phone is in phoneNumber.
	raw := []byte(`[{"id":"120363430262328476@g.us","subject":"Bot Alquería","participants":[` +
		`{"id":"242554100576489:0@lid","phoneNumber":"34692747052@s.whatsapp.net"},` +
		`{"id":"262096671481918:0@lid","phoneNumber":"34960255536@s.whatsapp.net"}]}]`)

	members := botGroupMemberPhonesFromPayload(raw, "120363430262328476@g.us")
	if members.empty() {
		t.Fatal("the member list must not be empty")
	}
	for _, sender := range []string{
		"34692747052@s.whatsapp.net",
		"242554100576489:0@lid",
		"242554100576489@lid",
	} {
		if !botGroupSenderInMembers(members, sender) {
			t.Errorf("member %q must be recognised (phones=%v lids=%v)", sender, members.phones, members.lids)
		}
	}
	// Someone who is not in the group stays out, whatever form they use.
	for _, stranger := range []string{
		"34600000000@s.whatsapp.net",
		"999999999999:0@lid",
		"999999999999@lid",
	} {
		if botGroupSenderInMembers(members, stranger) {
			t.Errorf("stranger %q must not be recognised", stranger)
		}
	}
}
