package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"

	"preactvillacarmen/internal/httpx"
)

type boFichajeMemberRef struct {
	ID       int     `json:"id"`
	FullName string  `json:"fullName"`
	DNI      *string `json:"dni"`
}

type boFichajeActiveEntry struct {
	ID         int64  `json:"id"`
	MemberID   int    `json:"memberId"`
	MemberName string `json:"memberName"`
	WorkDate   string `json:"workDate"`
	StartTime  string `json:"startTime"`
	StartAtISO string `json:"startAtIso"`
}

type boFichajeSchedule struct {
	ID         int64  `json:"id"`
	MemberID   int    `json:"memberId"`
	MemberName string `json:"memberName"`
	Date       string `json:"date"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	UpdatedAt  string `json:"updatedAt"`
}

type boFichajeState struct {
	Now           string                 `json:"now"`
	Member        *boFichajeMemberRef    `json:"member"`
	ActiveEntry   *boFichajeActiveEntry  `json:"activeEntry"`
	ActiveEntries []boFichajeActiveEntry `json:"activeEntries"`
	ScheduleToday *boFichajeSchedule     `json:"scheduleToday"`
}

type boFichajeStartRequest struct {
	DNI      string `json:"dni"`
	Password string `json:"password"`
}

type boFichajeAdminMemberRequest struct {
	MemberID int `json:"memberId"`
}

type boFichajeEntryPatchRequest struct {
	StartTime *string `json:"startTime"`
	EndTime   *string `json:"endTime"`
}

type boHorariosAssignRequest struct {
	Date      string `json:"date"`
	MemberID  int    `json:"memberId"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

type boFichajeTimeEntry struct {
	ID            int64   `json:"id"`
	MemberID      int     `json:"memberId"`
	MemberName    string  `json:"memberName"`
	WorkDate      string  `json:"workDate"`
	StartTime     string  `json:"startTime"`
	EndTime       *string `json:"endTime"`
	MinutesWorked int     `json:"minutesWorked"`
	Source        string  `json:"source"`
}

type boHorariosMonthPoint struct {
	Date          string `json:"date"`
	AssignedCount int    `json:"assignedCount"`
}

type boClockMember struct {
	ID           int
	FirstName    string
	LastName     string
	FullName     string
	DNI          *string
	PasswordHash string
}

type boFichajeHub struct {
	mu    sync.RWMutex
	rooms map[int]map[*boFichajeClient]struct{}
}

type boFichajeClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func newBOFichajeHub() *boFichajeHub {
	return &boFichajeHub{rooms: map[int]map[*boFichajeClient]struct{}{}}
}

func (h *boFichajeHub) add(restaurantID int, c *boFichajeClient) {
	if restaurantID <= 0 || c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[restaurantID]
	if room == nil {
		room = map[*boFichajeClient]struct{}{}
		h.rooms[restaurantID] = room
	}
	room[c] = struct{}{}
}

func (h *boFichajeHub) remove(restaurantID int, c *boFichajeClient) {
	if restaurantID <= 0 || c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[restaurantID]
	if room == nil {
		return
	}
	delete(room, c)
	if len(room) == 0 {
		delete(h.rooms, restaurantID)
	}
}

func (h *boFichajeHub) list(restaurantID int) []*boFichajeClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[restaurantID]
	if len(room) == 0 {
		return nil
	}
	out := make([]*boFichajeClient, 0, len(room))
	for c := range room {
		out = append(out, c)
	}
	return out
}

func (h *boFichajeHub) broadcast(restaurantID int, payload any) {
	if restaurantID <= 0 {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	clients := h.list(restaurantID)
	for _, c := range clients {
		if err := c.writeText(raw); err != nil {
			h.remove(restaurantID, c)
			_ = c.close()
		}
	}
}

func (c *boFichajeClient) writeText(raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *boFichajeClient) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeText(raw)
}

func (c *boFichajeClient) ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second))
}

func (c *boFichajeClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

var boFichajeWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Session cookie auth is required, so we can accept cross-host local proxies.
		return true
	},
}

func (s *Server) runBOFichajeAutoCutLoop() {
	if s == nil || s.db == nil {
		return
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		_ = s.autoCutOpenEntries(ctx)
		cancel()
		<-ticker.C
	}
}

func (s *Server) autoCutOpenEntries(ctx context.Context) error {
	now := time.Now().In(boMadridTZ)
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			e.id,
			e.restaurant_id,
			e.restaurant_member_id,
			DATE_FORMAT(e.work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(e.start_time, '%H:%i') AS start_time,
			TIME_FORMAT(s.end_time, '%H:%i') AS schedule_end,
			TRIM(CONCAT(COALESCE(m.first_name, ''), ' ', COALESCE(m.last_name, ''))) AS member_name
		FROM member_time_entries e
		LEFT JOIN member_work_schedules s
			ON s.restaurant_id = e.restaurant_id
			AND s.restaurant_member_id = e.restaurant_member_id
			AND s.work_date = e.work_date
		LEFT JOIN restaurant_members m
			ON m.id = e.restaurant_member_id
			AND m.restaurant_id = e.restaurant_id
		WHERE e.end_time IS NULL
		ORDER BY e.id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			entryID      int64
			restaurantID int
			memberID     int
			workDate     string
			startTime    string
			scheduleEnd  sql.NullString
			memberName   string
		)
		if err := rows.Scan(&entryID, &restaurantID, &memberID, &workDate, &startTime, &scheduleEnd, &memberName); err != nil {
			return err
		}

		cutoffHHMM := "23:59"
		if scheduleEnd.Valid && strings.TrimSpace(scheduleEnd.String) != "" {
			cutoffHHMM = strings.TrimSpace(scheduleEnd.String)
		}
		cutoffAt, err := time.ParseInLocation("2006-01-02 15:04", workDate+" "+cutoffHHMM, boMadridTZ)
		if err != nil {
			continue
		}
		if now.Before(cutoffAt) {
			continue
		}

		cutoffDateTime := cutoffAt.Format("2006-01-02 15:04:05")
		cutoffClock := cutoffAt.Format("15:04:05")
		res, err := s.db.ExecContext(ctx, `
			UPDATE member_time_entries
			SET
				end_time = ?,
				minutes_worked = GREATEST(0, TIMESTAMPDIFF(MINUTE, CONCAT(work_date, ' ', start_time), ?)),
				source = 'clock_autocut'
			WHERE id = ? AND restaurant_id = ? AND end_time IS NULL
		`, cutoffClock, cutoffDateTime, entryID, restaurantID)
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			continue
		}

		startAt, err := time.ParseInLocation("2006-01-02 15:04", workDate+" "+startTime, boMadridTZ)
		if err != nil {
			startAt = now
		}
		memberName = strings.TrimSpace(memberName)
		if memberName == "" {
			memberName = fmt.Sprintf("Miembro #%d", memberID)
		}
		active := &boFichajeActiveEntry{
			ID:         entryID,
			MemberID:   memberID,
			MemberName: memberName,
			WorkDate:   workDate,
			StartTime:  startTime,
			StartAtISO: startAt.Format(time.RFC3339),
		}
		s.broadcastBOFichajeEvent(restaurantID, "clock_stopped", active, nil)
	}
	return rows.Err()
}

func (s *Server) handleBOFichajePing(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "fichaje_ready",
	})
}

func (s *Server) handleBOFichajeState(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	state, err := s.buildBOFichajeState(r.Context(), a)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo estado de fichaje")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"state":   state,
	})
}

func (s *Server) handleBOFichajeStart(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boFichajeStartRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	dniNorm := normalizeBODNI(req.DNI)
	password := strings.TrimSpace(req.Password)
	if dniNorm == "" || password == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "DNI y contraseña son obligatorios",
		})
		return
	}

	member, err := s.getBOClockMemberForUser(r.Context(), a.ActiveRestaurantID, a.User.ID, dniNorm)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "DNI o contraseña inválidos",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando miembro")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(member.PasswordHash), []byte(password)); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "DNI o contraseña inválidos",
		})
		return
	}

	active, err := s.getBOActiveEntry(r.Context(), a.ActiveRestaurantID, member.ID, member.FullName)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando fichaje")
		return
	}

	if active == nil {
		now := time.Now().In(boMadridTZ)
		dateISO := now.Format("2006-01-02")
		startClock := now.Format("15:04:05")
		res, err := s.db.ExecContext(r.Context(), `
			INSERT INTO member_time_entries
				(restaurant_member_id, restaurant_id, work_date, start_time, end_time, minutes_worked, source)
			VALUES (?, ?, ?, ?, NULL, 0, 'clock')
		`, member.ID, a.ActiveRestaurantID, dateISO, startClock)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando fichaje")
			return
		}
		entryID, _ := res.LastInsertId()
		active = &boFichajeActiveEntry{
			ID:         entryID,
			MemberID:   member.ID,
			MemberName: member.FullName,
			WorkDate:   dateISO,
			StartTime:  startClock[:5],
			StartAtISO: now.Format(time.RFC3339),
		}
	}

	activeEntries, err := s.listBOActiveEntries(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo fichajes activos")
		return
	}

	state, err := s.buildBOFichajeStateWithMember(r.Context(), a.ActiveRestaurantID, member, activeEntries)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo estado")
		return
	}

	s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "clock_started", state.ActiveEntry, nil)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"state":   state,
	})
}

func (s *Server) handleBOFichajeStop(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	member, err := s.getBOClockMemberForUser(r.Context(), a.ActiveRestaurantID, a.User.ID, "")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "No hay miembro vinculado a tu usuario para fichar",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	active, err := s.getBOActiveEntry(r.Context(), a.ActiveRestaurantID, member.ID, member.FullName)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando fichaje")
		return
	}
	if active == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No tienes un fichaje activo",
		})
		return
	}

	now := time.Now().In(boMadridTZ)
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE member_time_entries
		SET
			end_time = ?,
			minutes_worked = GREATEST(0, TIMESTAMPDIFF(MINUTE, CONCAT(work_date, ' ', start_time), ?)),
			source = 'clock'
		WHERE id = ? AND restaurant_id = ?
	`, now.Format("15:04:05"), now.Format("2006-01-02 15:04:05"), active.ID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cerrando fichaje")
		return
	}

	activeEntries, err := s.listBOActiveEntries(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo fichajes activos")
		return
	}

	state, err := s.buildBOFichajeStateWithMember(r.Context(), a.ActiveRestaurantID, member, activeEntries)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo estado")
		return
	}

	s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "clock_stopped", active, nil)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"state":   state,
	})
}

func (s *Server) handleBOFichajeAdminStart(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boFichajeAdminMemberRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	if req.MemberID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "memberId inválido",
		})
		return
	}

	member, err := s.getBOClockMemberByID(r.Context(), a.ActiveRestaurantID, req.MemberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	active, err := s.getBOActiveEntry(r.Context(), a.ActiveRestaurantID, member.ID, member.FullName)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando fichaje")
		return
	}
	if active == nil {
		now := time.Now().In(boMadridTZ)
		dateISO := now.Format("2006-01-02")
		startClock := now.Format("15:04:05")
		res, err := s.db.ExecContext(r.Context(), `
			INSERT INTO member_time_entries
				(restaurant_member_id, restaurant_id, work_date, start_time, end_time, minutes_worked, source)
			VALUES (?, ?, ?, ?, NULL, 0, 'clock')
		`, member.ID, a.ActiveRestaurantID, dateISO, startClock)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando fichaje")
			return
		}
		entryID, _ := res.LastInsertId()
		active = &boFichajeActiveEntry{
			ID:         entryID,
			MemberID:   member.ID,
			MemberName: member.FullName,
			WorkDate:   dateISO,
			StartTime:  startClock[:5],
			StartAtISO: now.Format(time.RFC3339),
		}
	}

	s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "clock_started", active, nil)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"activeEntry": active,
	})
}

func (s *Server) handleBOFichajeAdminStop(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boFichajeAdminMemberRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	if req.MemberID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "memberId inválido",
		})
		return
	}

	member, err := s.getBOClockMemberByID(r.Context(), a.ActiveRestaurantID, req.MemberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	active, err := s.getBOActiveEntry(r.Context(), a.ActiveRestaurantID, member.ID, member.FullName)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando fichaje")
		return
	}
	if active == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "El miembro no tiene un fichaje activo",
		})
		return
	}

	now := time.Now().In(boMadridTZ)
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE member_time_entries
		SET
			end_time = ?,
			minutes_worked = GREATEST(0, TIMESTAMPDIFF(MINUTE, CONCAT(work_date, ' ', start_time), ?)),
			source = 'clock'
		WHERE id = ? AND restaurant_id = ? AND end_time IS NULL
	`, now.Format("15:04:05"), now.Format("2006-01-02 15:04:05"), active.ID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cerrando fichaje")
		return
	}

	s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "clock_stopped", active, nil)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"activeEntry": active,
	})
}

func (s *Server) handleBOFichajeEntriesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date, err := parseBODateQuery(r.URL.Query().Get("date"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "date inválida",
		})
		return
	}

	memberID, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("memberId")))
	if err != nil || memberID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "memberId inválido",
		})
		return
	}

	if _, err := s.getBOClockMemberByID(r.Context(), a.ActiveRestaurantID, memberID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	entries, err := s.listBOTimeEntriesByMemberAndDate(r.Context(), a.ActiveRestaurantID, memberID, date.Format("2006-01-02"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo registros")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"date":     date.Format("2006-01-02"),
		"memberId": memberID,
		"entries":  entries,
	})
}

func (s *Server) handleBOFichajeEntryPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	entryID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id inválido",
		})
		return
	}

	var req boFichajeEntryPatchRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	if req.StartTime == nil && req.EndTime == nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Nada que actualizar",
		})
		return
	}

	current, err := s.getBOTimeEntryByID(r.Context(), a.ActiveRestaurantID, int64(entryID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Registro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo registro")
		return
	}

	nextStart := current.StartTime
	nextEnd := current.EndTime
	if req.StartTime != nil {
		if current.EndTime == nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "No se puede editar la hora de inicio de un fichaje activo",
			})
			return
		}
		start, err := parseStrictHHMM(*req.StartTime)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Hora de inicio inválida",
			})
			return
		}
		nextStart = start
	}

	if req.EndTime != nil {
		end, err := parseStrictHHMM(*req.EndTime)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Hora de fin inválida",
			})
			return
		}
		nextEnd = &end
	}

	if nextEnd != nil {
		startT, _ := time.Parse("15:04", nextStart)
		endT, _ := time.Parse("15:04", *nextEnd)
		if !endT.After(startT) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "La hora de fin debe ser mayor que la de inicio",
			})
			return
		}
	}

	var endArg any
	if nextEnd != nil {
		endArg = *nextEnd + ":00"
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE member_time_entries
		SET
			start_time = ?,
			end_time = ?,
			minutes_worked = CASE
				WHEN ? IS NULL THEN 0
				ELSE GREATEST(0, TIMESTAMPDIFF(MINUTE, CONCAT(work_date, ' ', ?), CONCAT(work_date, ' ', ?)))
			END
		WHERE id = ? AND restaurant_id = ?
	`, nextStart+":00", endArg, endArg, nextStart+":00", endArg, entryID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando registro")
		return
	}

	updated, err := s.getBOTimeEntryByID(r.Context(), a.ActiveRestaurantID, int64(entryID))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo registro actualizado")
		return
	}

	if current.EndTime == nil && updated.EndTime != nil {
		startAt, err := time.ParseInLocation("2006-01-02 15:04", current.WorkDate+" "+current.StartTime, boMadridTZ)
		if err != nil {
			startAt = time.Now().In(boMadridTZ)
		}
		s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "clock_stopped", &boFichajeActiveEntry{
			ID:         current.ID,
			MemberID:   current.MemberID,
			MemberName: current.MemberName,
			WorkDate:   current.WorkDate,
			StartTime:  current.StartTime,
			StartAtISO: startAt.Format(time.RFC3339),
		}, nil)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"entry":   updated,
	})
}

func (s *Server) handleBOHorariosList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date, err := parseBODateQuery(r.URL.Query().Get("date"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "date inválida",
		})
		return
	}

	schedules, err := s.queryBOHorariosByDate(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo horarios")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"date":      date.Format("2006-01-02"),
		"schedules": schedules,
	})
}

func (s *Server) handleBOHorariosMonth(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	now := time.Now().In(boMadridTZ)
	year := now.Year()
	month := int(now.Month())

	if raw := strings.TrimSpace(r.URL.Query().Get("year")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 2000 || v > 2100 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "year inválido"})
			return
		}
		year = v
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("month")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 12 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "month inválido"})
			return
		}
		month = v
	}

	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, boMadridTZ)
	end := start.AddDate(0, 1, -1)

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT DATE_FORMAT(work_date, '%Y-%m-%d') AS d, COUNT(*) AS c
		FROM member_work_schedules
		WHERE restaurant_id = ? AND work_date BETWEEN ? AND ?
		GROUP BY work_date
		ORDER BY work_date ASC
	`, a.ActiveRestaurantID, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo calendario de horarios")
		return
	}
	defer rows.Close()

	points := make([]boHorariosMonthPoint, 0, 40)
	for rows.Next() {
		var p boHorariosMonthPoint
		if err := rows.Scan(&p.Date, &p.AssignedCount); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo calendario")
			return
		}
		points = append(points, p)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"year":    year,
		"month":   month,
		"days":    points,
	})
}

func (s *Server) handleBOHorariosAssign(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boHorariosAssignRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	if req.MemberID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "memberId inválido"})
		return
	}

	date, err := parseBODateQuery(req.Date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "date inválida"})
		return
	}
	startHHMM, err := parseStrictHHMM(req.StartTime)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Hora de entrada inválida"})
		return
	}
	endHHMM, err := parseStrictHHMM(req.EndTime)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Hora de salida inválida"})
		return
	}

	startT, _ := time.Parse("15:04", startHHMM)
	endT, _ := time.Parse("15:04", endHHMM)
	if !endT.After(startT) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "La hora de salida debe ser mayor que la de entrada"})
		return
	}

	var firstName, lastName string
	err = s.db.QueryRowContext(r.Context(), `
		SELECT first_name, last_name
		FROM restaurant_members
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
		LIMIT 1
	`, req.MemberID, a.ActiveRestaurantID).Scan(&firstName, &lastName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Miembro no encontrado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando miembro")
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO member_work_schedules
			(restaurant_member_id, restaurant_id, work_date, start_time, end_time)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			start_time = VALUES(start_time),
			end_time = VALUES(end_time),
			updated_at = CURRENT_TIMESTAMP,
			id = LAST_INSERT_ID(id)
	`, req.MemberID, a.ActiveRestaurantID, date.Format("2006-01-02"), startHHMM+":00", endHHMM+":00")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando horario")
		return
	}
	scheduleID, _ := res.LastInsertId()

	schedule, err := s.getBOHorarioByID(r.Context(), a.ActiveRestaurantID, scheduleID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo horario guardado")
		return
	}
	if strings.TrimSpace(schedule.MemberName) == "" {
		schedule.MemberName = strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
	}

	s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "schedule_updated", nil, &schedule)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"schedule": schedule,
	})
}

type boHorariosUpdateRequest struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

func (s *Server) handleBOHorariosUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	scheduleID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	var req boHorariosUpdateRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	startHHMM, err := parseStrictHHMM(req.StartTime)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Hora de entrada inválida",
		})
		return
	}
	endHHMM, err := parseStrictHHMM(req.EndTime)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Hora de salida inválida",
		})
		return
	}

	startT, _ := time.Parse("15:04", startHHMM)
	endT, _ := time.Parse("15:04", endHHMM)
	if !endT.After(startT) {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "La hora de salida debe ser mayor que la de entrada",
		})
		return
	}

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE member_work_schedules
		SET start_time = ?, end_time = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND restaurant_id = ?
	`, startHHMM+":00", endHHMM+":00", scheduleID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando horario")
		return
	}

	schedule, err := s.getBOHorarioByID(r.Context(), a.ActiveRestaurantID, int64(scheduleID))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo horario")
		return
	}

	s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "schedule_updated", nil, &schedule)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"schedule": schedule,
	})
}

func (s *Server) handleBOHorariosDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	scheduleID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	// Get schedule before deleting for broadcast
	schedule, err := s.getBOHorarioByID(r.Context(), a.ActiveRestaurantID, int64(scheduleID))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo horario")
		return
	}

	_, err = s.db.ExecContext(r.Context(), `
		DELETE FROM member_work_schedules
		WHERE id = ? AND restaurant_id = ?
	`, scheduleID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando horario")
		return
	}

	s.broadcastBOFichajeEvent(a.ActiveRestaurantID, "schedule_deleted", nil, &schedule)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleBOHorariosMySchedule(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// Get the current user's member ID from the session
	if a.MemberID == nil || *a.MemberID == 0 {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
			"success": false,
			"message": "No tienes un miembro asociado",
		})
		return
	}

	memberID := *a.MemberID

	// Get date range from query params or default to current month
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	from := time.Now()
	to := time.Now()

	if fromStr != "" && toStr != "" {
		var err error
		from, err = parseBODate(fromStr)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "from invalido",
			})
			return
		}
		to, err = parseBODate(toStr)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "to invalido",
			})
			return
		}
	} else {
		// Default to current month
		from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, boMadridTZ)
		to = from.AddDate(0, 1, -1)
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, restaurant_member_id, date, start_time, end_time, created_at, updated_at
		FROM member_schedules
		WHERE restaurant_id = ? AND restaurant_member_id = ? AND date BETWEEN ? AND ?
		ORDER BY date ASC
	`, a.ActiveRestaurantID, memberID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando horarios")
		return
	}
	defer rows.Close()

	type scheduleRow struct {
		ID                int
		MemberID          int
		Date              string
		StartTime         string
		EndTime           string
		CreatedAt         time.Time
		UpdatedAt         time.Time
	}

	schedules := make([]map[string]any, 0)
	for rows.Next() {
		var sch scheduleRow
		err := rows.Scan(&sch.ID, &sch.MemberID, &sch.Date, &sch.StartTime, &sch.EndTime, &sch.CreatedAt, &sch.UpdatedAt)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo horarios")
			return
		}

		// Get member name
		var firstName, lastName string
		nameRow := s.db.QueryRowContext(r.Context(), `
			SELECT first_name, last_name FROM restaurant_members WHERE id = ? AND restaurant_id = ?
		`, sch.MemberID, a.ActiveRestaurantID)
		_ = nameRow.Scan(&firstName, &lastName)

		schedules = append(schedules, map[string]any{
			"id":          sch.ID,
			"memberId":    sch.MemberID,
			"memberName":  strings.TrimSpace(firstName + " " + lastName),
			"date":        sch.Date,
			"startTime":   sch.StartTime,
			"endTime":     sch.EndTime,
			"createdAt":   sch.CreatedAt.Format(time.RFC3339),
			"updatedAt":   sch.UpdatedAt.Format(time.RFC3339),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"schedules": schedules,
		"from":      from.Format("2006-01-02"),
		"to":        to.Format("2006-01-02"),
	})
}

func (s *Server) handleBOFichajeWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	conn, err := boFichajeWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &boFichajeClient{conn: conn}
	s.fichajeHub.add(a.ActiveRestaurantID, client)
	defer func() {
		s.fichajeHub.remove(a.ActiveRestaurantID, client)
		_ = client.close()
	}()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	state, err := s.buildBOFichajeState(r.Context(), a)
	if err == nil {
		_ = client.writeJSON(map[string]any{
			"type":          "hello",
			"restaurantId":  a.ActiveRestaurantID,
			"at":            time.Now().In(boMadridTZ).Format(time.RFC3339),
			"activeEntry":   state.ActiveEntry,
			"activeEntries": state.ActiveEntries,
		})
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if len(raw) == 0 {
				continue
			}
			var msg struct {
				Type         string `json:"type"`
				RestaurantID int    `json:"restaurantId"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(msg.Type))
			if typ != "join_restaurant" && typ != "join_restaurante" {
				continue
			}
			if msg.RestaurantID > 0 && msg.RestaurantID != a.ActiveRestaurantID {
				continue
			}
			state, err := s.buildBOFichajeState(r.Context(), a)
			if err != nil {
				continue
			}
			_ = client.writeJSON(map[string]any{
				"type":          "joined",
				"restaurantId":  a.ActiveRestaurantID,
				"at":            time.Now().In(boMadridTZ).Format(time.RFC3339),
				"activeEntry":   state.ActiveEntry,
				"activeEntries": state.ActiveEntries,
			})
		}
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-readDone:
			return
		case <-ping.C:
			if err := client.ping(); err != nil {
				return
			}
		}
	}
}

func (s *Server) buildBOFichajeState(ctx context.Context, a boAuth) (boFichajeState, error) {
	activeEntries, err := s.listBOActiveEntries(ctx, a.ActiveRestaurantID)
	if err != nil {
		return boFichajeState{}, err
	}

	member, err := s.getBOClockMemberForUser(ctx, a.ActiveRestaurantID, a.User.ID, "")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return boFichajeState{
				Now:           time.Now().In(boMadridTZ).Format(time.RFC3339),
				ActiveEntries: activeEntries,
			}, nil
		}
		return boFichajeState{}, err
	}
	return s.buildBOFichajeStateWithMember(ctx, a.ActiveRestaurantID, member, activeEntries)
}

func (s *Server) buildBOFichajeStateWithMember(ctx context.Context, restaurantID int, member boClockMember, activeEntries []boFichajeActiveEntry) (boFichajeState, error) {
	active, err := s.getBOActiveEntry(ctx, restaurantID, member.ID, member.FullName)
	if err != nil {
		return boFichajeState{}, err
	}

	today := boTodayDate().Format("2006-01-02")
	schedule, err := s.getBOHorarioByMemberAndDate(ctx, restaurantID, member.ID, today)
	if err != nil {
		return boFichajeState{}, err
	}

	return boFichajeState{
		Now:           time.Now().In(boMadridTZ).Format(time.RFC3339),
		Member:        &boFichajeMemberRef{ID: member.ID, FullName: member.FullName, DNI: member.DNI},
		ActiveEntry:   active,
		ActiveEntries: activeEntries,
		ScheduleToday: schedule,
	}, nil
}

func (s *Server) getBOClockMemberForUser(ctx context.Context, restaurantID, userID int, requiredDNINorm string) (boClockMember, error) {
	q := `
		SELECT
			m.id,
			m.first_name,
			m.last_name,
			m.dni,
			u.password_hash
		FROM restaurant_members m
		JOIN bo_users u ON u.id = ?
		WHERE m.restaurant_id = ?
			AND m.is_active = 1
			AND (
				m.bo_user_id = u.id
				OR (
					m.bo_user_id IS NULL
					AND m.email IS NOT NULL
					AND LOWER(TRIM(m.email)) = LOWER(TRIM(u.email))
				)
			)
	`
	args := []any{userID, restaurantID}
	if strings.TrimSpace(requiredDNINorm) != "" {
		q += " AND UPPER(REPLACE(COALESCE(m.dni, ''), ' ', '')) = ?"
		args = append(args, requiredDNINorm)
	}
	q += " ORDER BY m.id ASC LIMIT 1"

	var (
		member       boClockMember
		dni          sql.NullString
		passwordHash string
	)
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&member.ID, &member.FirstName, &member.LastName, &dni, &passwordHash)
	if err != nil {
		return boClockMember{}, err
	}

	member.PasswordHash = strings.TrimSpace(passwordHash)
	if member.PasswordHash == "" {
		return boClockMember{}, sql.ErrNoRows
	}
	member.DNI = nullStringPtr(dni)
	member.FullName = strings.TrimSpace(strings.Join([]string{member.FirstName, member.LastName}, " "))
	if member.FullName == "" {
		member.FullName = fmt.Sprintf("Miembro #%d", member.ID)
	}
	return member, nil
}

func (s *Server) getBOActiveEntry(ctx context.Context, restaurantID, memberID int, memberName string) (*boFichajeActiveEntry, error) {
	var (
		entryID   int64
		workDate  string
		startTime string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			id,
			DATE_FORMAT(work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(start_time, '%H:%i') AS start_time
		FROM member_time_entries
		WHERE restaurant_id = ? AND restaurant_member_id = ? AND end_time IS NULL
		ORDER BY work_date DESC, id DESC
		LIMIT 1
	`, restaurantID, memberID).Scan(&entryID, &workDate, &startTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	startAt, err := time.ParseInLocation("2006-01-02 15:04", workDate+" "+startTime, boMadridTZ)
	if err != nil {
		startAt = time.Now().In(boMadridTZ)
	}

	return &boFichajeActiveEntry{
		ID:         entryID,
		MemberID:   memberID,
		MemberName: memberName,
		WorkDate:   workDate,
		StartTime:  startTime,
		StartAtISO: startAt.Format(time.RFC3339),
	}, nil
}

func (s *Server) listBOActiveEntries(ctx context.Context, restaurantID int) ([]boFichajeActiveEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			e.id,
			e.restaurant_member_id,
			DATE_FORMAT(e.work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(e.start_time, '%H:%i') AS start_time,
			TRIM(CONCAT(m.first_name, ' ', m.last_name)) AS member_name
		FROM member_time_entries e
		JOIN restaurant_members m ON m.id = e.restaurant_member_id AND m.restaurant_id = e.restaurant_id
		WHERE e.restaurant_id = ? AND e.end_time IS NULL AND m.is_active = 1
		ORDER BY e.work_date ASC, e.start_time ASC, e.id ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]boFichajeActiveEntry, 0, 32)
	for rows.Next() {
		var (
			item      boFichajeActiveEntry
			memberRaw string
		)
		if err := rows.Scan(&item.ID, &item.MemberID, &item.WorkDate, &item.StartTime, &memberRaw); err != nil {
			return nil, err
		}
		item.MemberName = strings.TrimSpace(memberRaw)
		if item.MemberName == "" {
			item.MemberName = fmt.Sprintf("Miembro #%d", item.MemberID)
		}
		startAt, err := time.ParseInLocation("2006-01-02 15:04", item.WorkDate+" "+item.StartTime, boMadridTZ)
		if err != nil {
			startAt = time.Now().In(boMadridTZ)
		}
		item.StartAtISO = startAt.Format(time.RFC3339)
		out = append(out, item)
	}
	return out, nil
}

func (s *Server) getBOClockMemberByID(ctx context.Context, restaurantID, memberID int) (boClockMember, error) {
	var (
		member boClockMember
		dni    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, first_name, last_name, dni
		FROM restaurant_members
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
		LIMIT 1
	`, memberID, restaurantID).Scan(&member.ID, &member.FirstName, &member.LastName, &dni)
	if err != nil {
		return boClockMember{}, err
	}
	member.DNI = nullStringPtr(dni)
	member.FullName = strings.TrimSpace(strings.Join([]string{member.FirstName, member.LastName}, " "))
	if member.FullName == "" {
		member.FullName = fmt.Sprintf("Miembro #%d", member.ID)
	}
	return member, nil
}

func (s *Server) listBOTimeEntriesByMemberAndDate(ctx context.Context, restaurantID, memberID int, dateISO string) ([]boFichajeTimeEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			e.id,
			e.restaurant_member_id,
			DATE_FORMAT(e.work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(e.start_time, '%H:%i') AS start_time,
			TIME_FORMAT(e.end_time, '%H:%i') AS end_time,
			COALESCE(e.minutes_worked, 0) AS minutes_worked,
			COALESCE(e.source, 'clock') AS source,
			TRIM(CONCAT(COALESCE(m.first_name, ''), ' ', COALESCE(m.last_name, ''))) AS member_name
		FROM member_time_entries e
		LEFT JOIN restaurant_members m ON m.id = e.restaurant_member_id AND m.restaurant_id = e.restaurant_id
		WHERE e.restaurant_id = ? AND e.restaurant_member_id = ? AND e.work_date = ?
		ORDER BY e.start_time ASC, e.id ASC
	`, restaurantID, memberID, dateISO)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]boFichajeTimeEntry, 0, 8)
	for rows.Next() {
		var (
			item      boFichajeTimeEntry
			endTime   sql.NullString
			memberRaw string
		)
		if err := rows.Scan(&item.ID, &item.MemberID, &item.WorkDate, &item.StartTime, &endTime, &item.MinutesWorked, &item.Source, &memberRaw); err != nil {
			return nil, err
		}
		item.MemberName = strings.TrimSpace(memberRaw)
		if item.MemberName == "" {
			item.MemberName = fmt.Sprintf("Miembro #%d", item.MemberID)
		}
		if endTime.Valid && strings.TrimSpace(endTime.String) != "" {
			v := strings.TrimSpace(endTime.String)
			item.EndTime = &v
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) getBOTimeEntryByID(ctx context.Context, restaurantID int, entryID int64) (boFichajeTimeEntry, error) {
	var (
		item      boFichajeTimeEntry
		endTime   sql.NullString
		memberRaw string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			e.id,
			e.restaurant_member_id,
			DATE_FORMAT(e.work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(e.start_time, '%H:%i') AS start_time,
			TIME_FORMAT(e.end_time, '%H:%i') AS end_time,
			COALESCE(e.minutes_worked, 0) AS minutes_worked,
			COALESCE(e.source, 'clock') AS source,
			TRIM(CONCAT(COALESCE(m.first_name, ''), ' ', COALESCE(m.last_name, ''))) AS member_name
		FROM member_time_entries e
		LEFT JOIN restaurant_members m ON m.id = e.restaurant_member_id AND m.restaurant_id = e.restaurant_id
		WHERE e.id = ? AND e.restaurant_id = ?
		LIMIT 1
	`, entryID, restaurantID).Scan(&item.ID, &item.MemberID, &item.WorkDate, &item.StartTime, &endTime, &item.MinutesWorked, &item.Source, &memberRaw)
	if err != nil {
		return boFichajeTimeEntry{}, err
	}
	item.MemberName = strings.TrimSpace(memberRaw)
	if item.MemberName == "" {
		item.MemberName = fmt.Sprintf("Miembro #%d", item.MemberID)
	}
	if endTime.Valid && strings.TrimSpace(endTime.String) != "" {
		v := strings.TrimSpace(endTime.String)
		item.EndTime = &v
	}
	return item, nil
}

func (s *Server) queryBOHorariosByDate(ctx context.Context, restaurantID int, date time.Time) ([]boFichajeSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			s.id,
			s.restaurant_member_id,
			m.first_name,
			m.last_name,
			DATE_FORMAT(s.work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(s.start_time, '%H:%i') AS start_time,
			TIME_FORMAT(s.end_time, '%H:%i') AS end_time,
			DATE_FORMAT(s.updated_at, '%Y-%m-%dT%H:%i:%sZ') AS updated_at
		FROM member_work_schedules s
		JOIN restaurant_members m ON m.id = s.restaurant_member_id AND m.restaurant_id = s.restaurant_id
		WHERE s.restaurant_id = ? AND s.work_date = ? AND m.is_active = 1
		ORDER BY m.last_name ASC, m.first_name ASC, m.id ASC
	`, restaurantID, date.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]boFichajeSchedule, 0, 32)
	for rows.Next() {
		var (
			it                  boFichajeSchedule
			firstName, lastName string
		)
		if err := rows.Scan(&it.ID, &it.MemberID, &firstName, &lastName, &it.Date, &it.StartTime, &it.EndTime, &it.UpdatedAt); err != nil {
			return nil, err
		}
		it.MemberName = strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
		if it.MemberName == "" {
			it.MemberName = fmt.Sprintf("Miembro #%d", it.MemberID)
		}
		out = append(out, it)
	}
	return out, nil
}

func (s *Server) getBOHorarioByID(ctx context.Context, restaurantID int, scheduleID int64) (boFichajeSchedule, error) {
	var (
		it                  boFichajeSchedule
		firstName, lastName string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			s.id,
			s.restaurant_member_id,
			m.first_name,
			m.last_name,
			DATE_FORMAT(s.work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(s.start_time, '%H:%i') AS start_time,
			TIME_FORMAT(s.end_time, '%H:%i') AS end_time,
			DATE_FORMAT(s.updated_at, '%Y-%m-%dT%H:%i:%sZ') AS updated_at
		FROM member_work_schedules s
		JOIN restaurant_members m ON m.id = s.restaurant_member_id AND m.restaurant_id = s.restaurant_id
		WHERE s.restaurant_id = ? AND s.id = ?
		LIMIT 1
	`, restaurantID, scheduleID).Scan(&it.ID, &it.MemberID, &firstName, &lastName, &it.Date, &it.StartTime, &it.EndTime, &it.UpdatedAt)
	if err != nil {
		return boFichajeSchedule{}, err
	}
	it.MemberName = strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
	if it.MemberName == "" {
		it.MemberName = fmt.Sprintf("Miembro #%d", it.MemberID)
	}
	return it, nil
}

func (s *Server) getBOHorarioByMemberAndDate(ctx context.Context, restaurantID, memberID int, dateISO string) (*boFichajeSchedule, error) {
	var (
		it      boFichajeSchedule
		name    string
		updated sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			s.id,
			DATE_FORMAT(s.work_date, '%Y-%m-%d') AS work_date,
			TIME_FORMAT(s.start_time, '%H:%i') AS start_time,
			TIME_FORMAT(s.end_time, '%H:%i') AS end_time,
			DATE_FORMAT(s.updated_at, '%Y-%m-%dT%H:%i:%sZ') AS updated_at,
			TRIM(CONCAT(m.first_name, ' ', m.last_name)) AS member_name
		FROM member_work_schedules s
		JOIN restaurant_members m ON m.id = s.restaurant_member_id AND m.restaurant_id = s.restaurant_id
		WHERE s.restaurant_id = ? AND s.restaurant_member_id = ? AND s.work_date = ?
		LIMIT 1
	`, restaurantID, memberID, dateISO).Scan(&it.ID, &it.Date, &it.StartTime, &it.EndTime, &updated, &name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	it.MemberID = memberID
	it.MemberName = strings.TrimSpace(name)
	if it.MemberName == "" {
		it.MemberName = fmt.Sprintf("Miembro #%d", memberID)
	}
	if updated.Valid {
		it.UpdatedAt = strings.TrimSpace(updated.String)
	}
	return &it, nil
}

func (s *Server) broadcastBOFichajeEvent(restaurantID int, eventType string, activeEntry *boFichajeActiveEntry, schedule *boFichajeSchedule) {
	if s.fichajeHub == nil || restaurantID <= 0 {
		return
	}
	payload := map[string]any{
		"type":         eventType,
		"restaurantId": restaurantID,
		"at":           time.Now().In(boMadridTZ).Format(time.RFC3339),
	}
	if activeEntry != nil || eventType == "clock_stopped" {
		payload["activeEntry"] = activeEntry
	}
	if schedule != nil {
		payload["schedule"] = schedule
	}
	s.fichajeHub.broadcast(restaurantID, payload)
}

func parseBODateQuery(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return boTodayDate(), nil
	}
	return parseBODate(raw)
}

var hhmmStrictRe = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)

func parseStrictHHMM(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if !hhmmStrictRe.MatchString(v) {
		return "", errors.New("invalid HH:MM")
	}
	return v, nil
}

func normalizeBODNI(raw string) string {
	v := strings.ToUpper(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, " ", "")
	return v
}
