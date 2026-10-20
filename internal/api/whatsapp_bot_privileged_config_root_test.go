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

// The privileged groups decide where the bot answers with the whole
// backoffice catalog and a root auth. The route that writes the bot config
// is open to any admin (role importance >= 90), so without a guard an admin
// could name a group they belong to and elevate themselves to the privileged
// catalog.
//
// A non-root write must keep the stored value, not drop it and not set it.
func TestHandleBOBotConfigPutPrivilegedGroupsNeedRoot(t *testing.T) {
	const group = "120363430262328476@g.us"

	cases := []struct {
		name string
		role string
		body string
		want []string
	}{
		{
			name: "root can set the privileged groups",
			role: "root",
			body: `{"privileged_group_jids":["` + group + `"],"tone":"formal"}`,
			want: []string{group},
		},
		{
			name: "a non-root write cannot set them",
			role: "admin",
			body: `{"privileged_group_jids":["` + group + `"],"tone":"formal"}`,
			// nil means "not written by this request": the handler reads the
			// stored value instead, so whatever is already there survives.
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t) // skips when TEST_DB_DSN is not set
			rid := 987654
			// Store a known value so the non-root case has something to keep.
			seed := `{"privileged_group_jids":["120363111111111@g.us"],"tone":"old"}`
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
			got := stored.PrivilegedGroupJIDs
			if tc.want == nil {
				// A non-root must not have changed the stored groups.
				if len(got) != 1 || got[0] != "120363111111111@g.us" {
					t.Errorf("a non-root write changed the privileged groups: %v", got)
				}
				return
			}
			if len(got) != 1 || got[0] != tc.want[0] {
				t.Errorf("privileged groups = %v, want %v", got, tc.want)
			}
		})
	}
}
