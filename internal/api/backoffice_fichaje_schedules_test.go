package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/config"
)

func TestScheduleOverlaps(t *testing.T) {
	cases := []struct {
		name          string
		startA, endA  string
		startB, endB  string
		expectOverlap bool
	}{
		{name: "adjacent does not overlap", startA: "09:00", endA: "12:00", startB: "12:00", endB: "15:00", expectOverlap: false},
		{name: "gap does not overlap", startA: "09:00", endA: "12:00", startB: "14:00", endB: "17:00", expectOverlap: false},
		{name: "contained overlaps", startA: "09:00", endA: "18:00", startB: "14:00", endB: "17:00", expectOverlap: true},
		{name: "partial overlaps", startA: "09:00", endA: "13:00", startB: "12:00", endB: "15:00", expectOverlap: true},
		{name: "invalid ignored", startA: "9:00", endA: "12:00", startB: "12:00", endB: "15:00", expectOverlap: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scheduleOverlaps(c.startA, c.endA, c.startB, c.endB); got != c.expectOverlap {
				t.Fatalf("scheduleOverlaps(%q,%q,%q,%q)=%v want %v", c.startA, c.endA, c.startB, c.endB, got, c.expectOverlap)
			}
		})
	}
}

func fichajeTestServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM member_work_schedules WHERE restaurant_id=1`,
			`DELETE FROM restaurant_members WHERE restaurant_id=1`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
			}
		}
		db.Close()
	})
	if _, err := db.Exec(`INSERT IGNORE INTO restaurants(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	return NewServer(db, config.Config{})
}

func horarioReq(method, path, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	for k, v := range params {
		routeCtx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(withBOAuth(ctx, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
}

func insertTestMember(t *testing.T, s *Server, id int, firstName, lastName string) int {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO restaurant_members(id, restaurant_id, first_name, last_name) VALUES(?,1,?,?)`, id, firstName, lastName); err != nil {
		t.Fatal(err)
	}
	return id
}

func assignHorario(t *testing.T, s *Server, memberID int, date, start, end string) int64 {
	t.Helper()
	rec := httptest.NewRecorder()
	body := `{"date":"` + date + `","memberId":` + strconv.Itoa(memberID) + `,"startTime":"` + start + `","endTime":"` + end + `"}`
	s.handleBOHorariosAssign(rec, horarioReq("POST", "/api/admin/horarios", body, nil))
	var out struct {
		Success  bool `json:"success"`
		Schedule struct {
			ID int64 `json:"id"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("assign unmarshal: %v body=%s", err, rec.Body.String())
	}
	if !out.Success {
		t.Fatalf("assign failed: %s", rec.Body.String())
	}
	if out.Schedule.ID == 0 {
		t.Fatalf("no schedule id in %s", rec.Body.String())
	}
	return out.Schedule.ID
}

func TestAssignAllowsMultipleSchedulesPerDay(t *testing.T) {
	s := fichajeTestServer(t)
	memberID := insertTestMember(t, s, 201, "Multi", "Shift")

	morning := assignHorario(t, s, memberID, "2026-08-10", "09:00", "12:00")
	afternoon := assignHorario(t, s, memberID, "2026-08-10", "14:00", "17:00")
	evening := assignHorario(t, s, memberID, "2026-08-10", "18:00", "20:00")

	if morning == afternoon || afternoon == evening || morning == evening {
		t.Fatal("expected three distinct schedule ids")
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM member_work_schedules WHERE restaurant_id=1 AND restaurant_member_id=? AND work_date='2026-08-10'`, memberID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("got %d schedules want 3", count)
	}
}

func TestAssignRejectsOverlappingSchedule(t *testing.T) {
	s := fichajeTestServer(t)
	memberID := insertTestMember(t, s, 202, "Over", "Lap")

	assignHorario(t, s, memberID, "2026-08-10", "09:00", "12:00")

	rec := httptest.NewRecorder()
	s.handleBOHorariosAssign(rec, horarioReq("POST", "/api/admin/horarios",
		`{"date":"2026-08-10","memberId":`+strconv.Itoa(memberID)+`,"startTime":"11:00","endTime":"13:00"}`, nil))
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Success {
		t.Fatalf("expected overlap rejection, got success: %s", rec.Body.String())
	}
	if out.Message == "" {
		t.Fatal("expected an overlap error message")
	}

	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM member_work_schedules WHERE restaurant_id=1 AND restaurant_member_id=? AND work_date='2026-08-10'`, memberID).Scan(&count)
	if count != 1 {
		t.Fatalf("got %d schedules want 1 after rejected overlap", count)
	}
}

func TestUpdateRejectsOverlapWithOtherShift(t *testing.T) {
	s := fichajeTestServer(t)
	memberID := insertTestMember(t, s, 203, "Update", "Overlap")

	first := assignHorario(t, s, memberID, "2026-08-10", "09:00", "12:00")
	assignHorario(t, s, memberID, "2026-08-10", "14:00", "17:00")

	// Extending the first shift into the second one must be rejected.
	rec := httptest.NewRecorder()
	s.handleBOHorariosUpdate(rec, horarioReq("PATCH", "/api/admin/horarios/"+strconv.FormatInt(first, 10),
		`{"startTime":"09:00","endTime":"15:00"}`, map[string]string{"id": strconv.FormatInt(first, 10)}))
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Success {
		t.Fatalf("expected overlap rejection on update, got success: %s", rec.Body.String())
	}
}
