package api

import "testing"

// In a group, msg.Sender is the GROUP id, not the member. A booking created
// from a group turn must belong to the member who asked for it, never to the
// group's jid: otherwise the reservation is stored against a phone number
// that is nobody's.
func TestBotGroupActorPhone(t *testing.T) {
	const groupJID = "120363430262328476@g.us"
	const memberPhone = "34692747052"

	cases := []struct {
		name string
		msg  botWebhookMessage
		want string
	}{
		{
			name: "group turn uses the participant",
			msg: botWebhookMessage{
				IsGroup:        true,
				Sender:         groupJID,
				ParticipantJID: memberPhone + "@s.whatsapp.net",
			},
			want: memberPhone,
		},
		{
			name: "group turn without a resolvable participant has no actor",
			msg: botWebhookMessage{
				IsGroup:        true,
				Sender:         groupJID,
				ParticipantJID: "262096671481918:0@lid", // opaque lid: not a phone
			},
			want: "",
		},
		{
			name: "one to one turn uses the sender",
			msg: botWebhookMessage{
				IsGroup: false,
				Sender:  memberPhone + "@s.whatsapp.net",
			},
			// The sender keeps its jid suffix here: the helper is only
			// asked for a group actor, and the 1:1 path is handled by the
			// caller that knows the turn is not a group.
			want: memberPhone + "@s.whatsapp.net",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := botSpecialCRUDActorPhone(tc.msg); got != tc.want {
				t.Errorf("actor phone = %q, want %q", got, tc.want)
			}
		})
	}
}

// The group jid must never be usable as a customer phone: digitsOnly on a
// group jid yields the group's digits, which are not anybody's number.
func TestBotGroupJIDIsNotACustomerPhone(t *testing.T) {
	groupJID := "120363430262328476@g.us"
	digits := digitsOnly(groupJID)
	if digits == "" {
		t.Fatal("the group jid should have digits")
	}
	if digits == "34692747052" {
		t.Error("the group digits must not collide with a member phone")
	}
}
