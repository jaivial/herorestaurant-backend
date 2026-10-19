package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"preactvillacarmen/internal/config"
)

// The privileged CONTACTS decide the same thing as the privileged groups for a
// one-to-one chat (wa_bot_privileged_contacts_v1): the bot answers there with
// the whole backoffice catalog and a root auth.
//
// The route that writes the bot config (/bot/config) is open to any admin
// (role importance >= 90), so without a guard an admin could name their own
// phone number and elevate themselves to the privileged catalog.
//
// A non-root write must keep the stored value, not drop it and not set it.
func TestHandleBOBotConfigPutPrivilegedContactsNeedRoot(t *testing.T) {
	const contact = "34600000000@s.whatsapp.net"

	cases := []struct {
		name string
		role string
		body string
		want []botPrivilegedContact
	}{
		{
			name: "root can set the privileged contacts",
			role: "root",
			body: `{"privileged_contacts":[{"jid":"` + contact + `","name":"Yo","isGroup":false}],"tone":"formal"}`,
			want: []botPrivilegedContact{{JID: contact, Name: "Yo"}},
		},
		{
			name: "a non-root write cannot set them",
			role: "admin",
			body: `{"privileged_contacts":[{"jid":"` + contact + `","name":"Yo","isGroup":false}],"tone":"formal"}`,
			// The stored value survives: the handler reads it back instead of
			// taking the one in the body.
			want: []botPrivilegedContact{{JID: "34611111111@s.whatsapp.net", Name: "Guardado"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t) // skips when TEST_DB_DSN is not set
			rid := 987655
			seed := `{"privileged_contacts":[{"jid":"34611111111@s.whatsapp.net","name":"Guardado","isGroup":false}],"tone":"old"}`
			if _, err := db.Exec(`INSERT INTO whatsapp_bot_config (restaurant_id, config_json) VALUES (?, ?)`, rid, seed); err != nil {
				t.Skipf("cannot seed the config: %v", err)
			}
			defer db.Exec(`DELETE FROM whatsapp_bot_config WHERE restaurant_id = ?`, rid)

			s := &Server{cfg: config.Config{}, db: db}
			auth := boAuth{Role: tc.role, User: boUser{Role: tc.role}, ActiveRestaurantID: rid}
			ctx := withBOAuth(context.Background(), auth)

			req := httptest.NewRequest(http.MethodPut, "/api/admin/bot/config", strings.NewReader(tc.body))
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			s.handleBOBotConfigPut(rec, req)

			var stored botTenantConfig
			var raw string
			if err := db.QueryRow(`SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id = ?`, rid).Scan(&raw); err != nil {
				t.Fatalf("cannot read the stored config: %v", err)
			}
			if err := json.Unmarshal([]byte(raw), &stored); err != nil {
				t.Fatalf("the stored config is not valid json: %v", err)
			}
			got := stored.PrivilegedContacts
			if len(got) != len(tc.want) {
				t.Fatalf("stored privileged contacts = %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i].JID != tc.want[i].JID {
					t.Errorf("stored privileged contact %d = %q, want %q", i, got[i].JID, tc.want[i].JID)
				}
			}
		})
	}
}
