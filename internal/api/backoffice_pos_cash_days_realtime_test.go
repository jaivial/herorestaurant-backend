package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// posCashDayWSClient attaches a real websocket client to the tables hub, so the
// events are asserted after going through the actual upgrade, the envelope and
// the JSON encoding rather than through a stub.
func posCashDayWSClient(t *testing.T, s *Server) (*websocket.Conn, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := boTablesWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.tablesHub.add(1, &boTablesClient{conn: conn})
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return conn, func() { conn.Close(); server.Close() }
}

// readPOSEvent waits for an event of the given type, skipping the unrelated POS
// events the same hub carries.
func readPOSEvent(t *testing.T, conn *websocket.Conn, eventType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %s: %v", eventType, err)
		}
		var payload map[string]any
		if err = json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("bad event json %q: %v", raw, err)
		}
		if payload["type"] == eventType {
			return payload
		}
	}
}

func TestPOSCashDayOpenAndCloseBroadcast(t *testing.T) {
	_, s := posCashDayTestDB(t)
	conn, done := posCashDayWSClient(t, s)
	defer done()

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"date":"2024-03-07","openingCashCents":5000}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open: got %d %s", recorder.Code, recorder.Body.String())
	}
	event := readPOSEvent(t, conn, "pos_cash_day_opened")
	if event["restaurantId"].(float64) != 1 {
		t.Fatalf("event is not scoped to the restaurant: %v", event)
	}
	data, _ := event["data"].(map[string]any)
	day, _ := data["cashDay"].(map[string]any)
	if day == nil || day["date"] != "2024-03-07" || day["status"] != "OPEN" {
		t.Fatalf("unexpected opened payload: %v", data)
	}
	// The whole day travels with the event so a client does not need a refetch.
	if day["openingCashCents"].(float64) != 5000 || day["openedByName"] != "Cash Day" {
		t.Fatalf("opened payload is not the full cash day: %v", day)
	}
	id := int64(day["id"].(float64))

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/1/close", `{"countedCashCents":5000}`, map[string]string{"id": strconv.FormatInt(id, 10)}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("close: got %d %s", recorder.Code, recorder.Body.String())
	}
	event = readPOSEvent(t, conn, "pos_cash_day_closed")
	data, _ = event["data"].(map[string]any)
	day, _ = data["cashDay"].(map[string]any)
	if day == nil || day["status"] != "CLOSED" || day["closedByName"] != "Cash Day" {
		t.Fatalf("unexpected closed payload: %v", data)
	}
}

func TestPOSCashDayTotalsBroadcastMatchesEndpoint(t *testing.T) {
	db, s := posCashDayTestDB(t)
	if _, err := db.Exec(`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(900,1,'2024-03-07','OPEN',7,0)`); err != nil {
		t.Fatal(err)
	}
	conn, done := posCashDayWSClient(t, s)
	defer done()

	// A cover adjustment is one of the operations that must move the day totals.
	recorder := httptest.NewRecorder()
	s.handleBOPOSCoverAdjustment(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/covers/adjustments",
		`{"date":"2024-03-07","serviceType":"DINNER","delta":3,"reason":"Terraza","idempotencyKey":"adj-1"}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("adjustment: got %d %s", recorder.Code, recorder.Body.String())
	}
	event := readPOSEvent(t, conn, "pos_cash_day_totals")
	data, _ := event["data"].(map[string]any)
	if data["date"] != "2024-03-07" || data["covers"].(float64) != 3 {
		t.Fatalf("unexpected totals payload: %v", data)
	}

	// The figure on the wire must be the one the endpoint reports, otherwise the
	// live widget and the calendar would disagree.
	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDaysRange(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days?from=2024-03-07&to=2024-03-07", "", nil))
	rows, _ := decodeCashDayBody(t, recorder)["data"].([]any)
	row, _ := rows[0].(map[string]any)
	for _, key := range []string{"totalGrossCents", "covers", "ticketCount"} {
		if data[key] != row[key] {
			t.Fatalf("%s differs: socket %v, endpoint %v", key, data[key], row[key])
		}
	}
}

// A broadcast runs after the business operation has committed, so a hub that is
// unusable must not turn a completed sale into an error.
func TestPOSCashDayBroadcastFailureDoesNotBreakOperation(t *testing.T) {
	db, s := posCashDayTestDB(t)
	if _, err := db.Exec(`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(900,1,'2024-03-07','OPEN',7,0)`); err != nil {
		t.Fatal(err)
	}
	s.tablesHub = nil

	recorder := httptest.NewRecorder()
	s.handleBOPOSCoverAdjustment(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/covers/adjustments",
		`{"date":"2024-03-07","serviceType":"DINNER","delta":3,"reason":"Terraza","idempotencyKey":"adj-1"}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("the adjustment must still succeed, got %d %s", recorder.Code, recorder.Body.String())
	}
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_cover_adjustments WHERE restaurant_id=1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("the adjustment must be committed, got %d rows", stored)
	}
}
