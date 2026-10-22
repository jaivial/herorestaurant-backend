package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// WhatsApp bot proposal+OK flow for the simple BACKOFFICE CONFIG writes:
// open/close days, daily limit, mesas de dos/tres limits and salon condesa.
//
// Coordination id: wa_bot_config_admin_v1
//
// Until now the privileged group could not touch restaurant configuration
// from WhatsApp at all: every config change required the panel. This closes
// the gap for the four single-table contracts of backoffice_config.go,
// replicating the panel column semantics exactly:
//
//	config_day          -> handleBOConfigDaySet: restaurant_days upsert by
//	                       (restaurant_id, date); range mode = ONE multi-row
//	                       upsert with dedup preserve-order, all dates
//	                       validated first.
//	config_daily_limit  -> handleBOConfigDailyLimitSet: reservation_manager
//	                       DELETE+INSERT (0..500); here inside the claim TX
//	                       so it is atomic (the panel does it in two
//	                       statements).
//	config_mesas        -> handleBOConfigMesasDeDosSet /
//	                       handleBOConfigMesasDeTresSet: upsert with the
//	                       SAME normalizeLimitInput ("sin_limite"->999,
//	                       0..999, canonical Itoa).
//	config_salon_condesa -> handleBOConfigSalonCondesaSet: salon_condesa
//	                       upsert state 0/1.
//
// Declared divergences from the panel, all visible in the preview:
//   - The bot REQUIRES an explicit date: the panel defaults an empty date to
//     today; a chat proposal must name the exact date it binds to.
//   - config_day caps a batch at 31 dates (the panel has no cap).
//   - The proposal binds the BEFORE state: if anyone changes the same rows
//     from the panel between the preview and the OK, the proposal goes
//     stale instead of silently overwriting. The execute-path before-reads
//     lock the target rows FOR UPDATE inside the claim transaction, so a
//     concurrent panel write can neither slip between the check and the
//     write nor be silently overwritten.
//
// No external effect: none of these handlers notifies anyone, broadcasts or
// emits webhooks, and neither does this flow. The effect is immediate on
// customer-facing availability once applied.
//
// Preview privacy: current DB values are never shown ("(actual oculto)");
// only actor-provided dates and new values appear.

// ---------- args ----------

type botConfigDayArgs struct {
	DedupKey string   `json:"dedup_key"`
	Dates    []string `json:"dates"`
	IsOpen   *bool    `json:"is_open"`
}

type botConfigDailyLimitArgs struct {
	DedupKey string `json:"dedup_key"`
	Date     string `json:"date"`
	Limit    int    `json:"limit"`
}

type botConfigMesasArgs struct {
	DedupKey  string `json:"dedup_key"`
	TableKind string `json:"table_kind"`
	Date      string `json:"date"`
	Limit     string `json:"limit"`
}

type botConfigSalonCondesaArgs struct {
	DedupKey string `json:"dedup_key"`
	Date     string `json:"date"`
	State    *bool  `json:"state"`
}

// ---------- shared helpers ----------

func botConfigDecode(input json.RawMessage, dst any, fields string) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil {
		return botJSON(map[string]any{"error": "argumentos inválidos: esta herramienta solo admite " + fields}), true
	}
	return "", false
}

// botConfigValidateDates validates and dedups (preserve order), exactly the
// panel range-mode contract, with the bot's 31-date cap.
func botConfigValidateDates(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	dates := make([]string, 0, len(raw))
	for _, d := range raw {
		d = strings.TrimSpace(d)
		if d == "" || !isValidISODate(d) {
			return nil, errors.New("fecha inválida (YYYY-MM-DD)")
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		dates = append(dates, d)
	}
	if len(dates) == 0 {
		return nil, errors.New("falta la fecha")
	}
	if len(dates) > 31 {
		return nil, errors.New("demasiadas fechas de una vez (máximo 31)")
	}
	return dates, nil
}

func botConfigValidateDate(raw string) (string, error) {
	d := strings.TrimSpace(raw)
	if d == "" || !isValidISODate(d) {
		return "", errors.New("fecha inválida (YYYY-MM-DD)")
	}
	return d, nil
}

// ---------- before-state readers (bind the proposal to the current rows) ----------

func botConfigDayBefore(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, rid int, dates []string, forUpdate bool) (map[string]int, error) {
	cur := map[string]int{}
	if len(dates) == 0 {
		return cur, nil
	}
	ph := make([]string, 0, len(dates))
	args := []any{rid}
	for _, d := range dates {
		ph = append(ph, "?")
		args = append(args, d)
	}
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	rows, err := q.QueryContext(ctx, "SELECT date, is_open FROM restaurant_days WHERE restaurant_id = ? AND date IN ("+strings.Join(ph, ",")+")"+lock, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var open int
		if err := rows.Scan(&d, &open); err != nil {
			return nil, err
		}
		cur[d] = open
	}
	return cur, rows.Err()
}

func botConfigDailyLimitBefore(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, rid int, date string, forUpdate bool) ([]int, error) {
	out := []int{}
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	rows, err := q.QueryContext(ctx, "SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? ORDER BY id"+lock, rid, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func botConfigMesasBefore(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, table, date string, forUpdate bool) (*string, error) {
	var v string
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	err := q.QueryRowContext(ctx, "SELECT dailyLimit FROM "+table+" WHERE restaurant_id = ? AND reservationDate = ?"+lock, rid, date).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func botConfigSalonCondesaBefore(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, date string, forUpdate bool) (*int, error) {
	var v int
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	err := q.QueryRowContext(ctx, "SELECT state FROM salon_condesa WHERE restaurant_id = ? AND date = ?"+lock, rid, date).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ---------- config_day ----------

func botConfigDayDedupKey(rid int, dates []string, isOpen bool) string {
	return botSha(fmt.Sprintf("cfgday|%d|%s|%t", rid, strings.Join(dates, ","), isOpen))
}

func botConfigDayBeforeKey(cur map[string]int) string {
	return botSha("cfgday_before|" + botCanon(cur))
}

func botConfigDayAfterKey(rid int, dates []string, isOpen bool) string {
	write := map[string]int{}
	v := 0
	if isOpen {
		v = 1
	}
	for _, d := range dates {
		write[d] = v
	}
	return botSha("cfgdayv1|" + botCanon(map[string]any{"rid": rid, "write": write}))
}

func (s *Server) botToolConfigDayPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in botConfigDayArgs
	if msgOut, failed := botConfigDecode(input, &in, "dates (lista de YYYY-MM-DD, máximo 31) e is_open (true=abierto, false=cerrado)"); failed {
		return msgOut, nil
	}
	dates, err := botConfigValidateDates(in.Dates)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	if in.IsOpen == nil {
		return botJSON(map[string]any{"error": "falta is_open: true para abrir, false para cerrar"}), nil
	}
	in.Dates = dates
	cur, cerr := botConfigDayBefore(ctx, s.db, rid, dates, false)
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer el estado actual"}), nil
	}
	in.DedupKey = botConfigDayDedupKey(rid, dates, *in.IsOpen)
	code, codeErr := botSpecialNewCode()
	if codeErr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	estado := "CERRAR"
	if *in.IsOpen {
		estado = "ABRIR"
	}
	preview := fmt.Sprintf("Propuesta: %s las reservas para %d fecha(s): %s. Estado actual: (actual oculto). Efecto inmediato en la disponibilidad que ven los clientes (web y bot); no se envía ningún aviso.", estado, len(dates), strings.Join(dates, ", "))
	before := botConfigDayBeforeKey(cur)
	after := botConfigDayAfterKey(rid, dates, *in.IsOpen)
	return s.botProposalPersist(ctx, rid, msg, "config_day", 0, in, in.DedupKey, actor, origin, before, after, preview, code)
}

func (s *Server) botConfigDayExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botConfigDayArgs
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	dates, err := botConfigValidateDates(in.Dates)
	if err != nil {
		return stale("la validación ya no pasa: " + err.Error())
	}
	if in.IsOpen == nil {
		return stale("la propuesta no tiene estado; pide una propuesta nueva")
	}
	if botConfigDayDedupKey(rid, dates, *in.IsOpen) != in.DedupKey {
		return stale("la propuesta no coincide con los datos validados; pide una propuesta nueva")
	}
	cur, cerr := botConfigDayBefore(ctx, tx, rid, dates, true)
	if cerr != nil {
		return "", errors.New("no se pudo releer el estado actual; no se aplicó nada")
	}
	if botConfigDayBeforeKey(cur) != p.Before {
		return stale("el estado actual cambió desde la vista previa (alguien lo tocó); pide una propuesta nueva")
	}
	if botConfigDayAfterKey(rid, dates, *in.IsOpen) != p.After {
		return stale("los datos resueltos cambiaron desde la vista previa; pide una propuesta nueva")
	}
	// Same write as handleBOConfigDaySet range mode: ONE multi-row upsert.
	isOpenInt := 0
	if *in.IsOpen {
		isOpenInt = 1
	}
	placeholders := make([]string, 0, len(dates))
	args := make([]any, 0, len(dates)*3)
	for _, d := range dates {
		placeholders = append(placeholders, "(?, ?, ?)")
		args = append(args, rid, d, isOpenInt)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO restaurant_days (restaurant_id, date, is_open) VALUES "+
		strings.Join(placeholders, ", ")+" ON DUPLICATE KEY UPDATE is_open = VALUES(is_open)", args...); err != nil {
		return "", errors.New("no se pudo actualizar restaurant_days; no se aplicó nada")
	}
	// Post-write verification inside the TX: the rows now read back exactly
	// the intended write set.
	back, berr := botConfigDayBefore(ctx, tx, rid, dates, false)
	if berr != nil {
		return "", errors.New("no se pudo verificar la escritura; no se aplicó nada")
	}
	if !botConfigDayWriteMatches(back, dates, isOpenInt) {
		return "", errors.New("la verificación posterior no coincide; no se aplicó nada")
	}
	estado := "abierta(s)"
	if !*in.IsOpen {
		estado = "cerrada(s)"
	}
	notice := fmt.Sprintf("Reservas %s para %d fecha(s): %s.", estado, len(dates), strings.Join(dates, ", "))
	rj, _ := json.Marshal(map[string]any{"op": p.Op, "dates": dates, "is_open": *in.IsOpen, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "dates": dates, "is_open": *in.IsOpen, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		return "", errors.New("no pude confirmar si se aplicó: consulta el resultado de la propuesta o el panel antes de repetir")
	}
	return notice, nil
}

func botConfigDayWriteMatches(cur map[string]int, dates []string, isOpenInt int) bool {
	if len(cur) != len(dates) {
		return false
	}
	for _, d := range dates {
		v, ok := cur[d]
		if !ok || v != isOpenInt {
			return false
		}
	}
	return true
}

// ---------- config_daily_limit ----------

func botConfigDailyLimitDedupKey(rid int, date string, limit int) string {
	return botSha(fmt.Sprintf("cfglimit|%d|%s|%d", rid, date, limit))
}

func botConfigDailyLimitBeforeKey(cur []int) string {
	return botSha("cfglimit_before|" + botCanon(cur))
}

func botConfigDailyLimitAfterKey(rid int, date string, limit int) string {
	return botSha("cfglimitv1|" + botCanon(map[string]any{"rid": rid, "date": date, "limit": limit}))
}

func (s *Server) botToolConfigDailyLimitPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in botConfigDailyLimitArgs
	if msgOut, failed := botConfigDecode(input, &in, "date (YYYY-MM-DD) y limit (0-500)"); failed {
		return msgOut, nil
	}
	date, err := botConfigValidateDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	if in.Limit < 0 || in.Limit > 500 {
		return botJSON(map[string]any{"error": "límite inválido: debe estar entre 0 y 500"}), nil
	}
	in.Date = date
	cur, cerr := botConfigDailyLimitBefore(ctx, s.db, rid, date, false)
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer el estado actual"}), nil
	}
	in.DedupKey = botConfigDailyLimitDedupKey(rid, date, in.Limit)
	code, codeErr := botSpecialNewCode()
	if codeErr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: fijar el LÍMITE DE COMENSALES del día %s en %d. Valor actual: (actual oculto). Efecto inmediato en la disponibilidad que ven los clientes; no se envía ningún aviso.", date, in.Limit)
	before := botConfigDailyLimitBeforeKey(cur)
	after := botConfigDailyLimitAfterKey(rid, date, in.Limit)
	return s.botProposalPersist(ctx, rid, msg, "config_daily_limit", 0, in, in.DedupKey, actor, origin, before, after, preview, code)
}

func (s *Server) botConfigDailyLimitExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botConfigDailyLimitArgs
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	date, err := botConfigValidateDate(in.Date)
	if err != nil {
		return stale("la validación ya no pasa: " + err.Error())
	}
	if in.Limit < 0 || in.Limit > 500 {
		return stale("límite inválido; pide una propuesta nueva")
	}
	if botConfigDailyLimitDedupKey(rid, date, in.Limit) != in.DedupKey {
		return stale("la propuesta no coincide con los datos validados; pide una propuesta nueva")
	}
	cur, cerr := botConfigDailyLimitBefore(ctx, tx, rid, date, true)
	if cerr != nil {
		return "", errors.New("no se pudo releer el estado actual; no se aplicó nada")
	}
	if botConfigDailyLimitBeforeKey(cur) != p.Before {
		return stale("el estado actual cambió desde la vista previa (alguien lo tocó); pide una propuesta nueva")
	}
	if botConfigDailyLimitAfterKey(rid, date, in.Limit) != p.After {
		return stale("los datos resueltos cambiaron desde la vista previa; pide una propuesta nueva")
	}
	// Same write as handleBOConfigDailyLimitSet (DELETE+INSERT) but atomic
	// inside the claim transaction.
	if _, err := tx.ExecContext(ctx, "DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?", rid, date); err != nil {
		return "", errors.New("no se pudo actualizar reservation_manager; no se aplicó nada")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO reservation_manager (restaurant_id, reservationDate, dailyLimit) VALUES (?, ?, ?)", rid, date, in.Limit); err != nil {
		return "", errors.New("no se pudo actualizar reservation_manager; no se aplicó nada")
	}
	back, berr := botConfigDailyLimitBefore(ctx, tx, rid, date, false)
	if berr != nil || len(back) != 1 || back[0] != in.Limit {
		return "", errors.New("la verificación posterior no coincide; no se aplicó nada")
	}
	notice := fmt.Sprintf("Límite de comensales del día %s fijado en %d.", date, in.Limit)
	rj, _ := json.Marshal(map[string]any{"op": p.Op, "date": date, "limit": in.Limit, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "date": date, "limit": in.Limit, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		return "", errors.New("no pude confirmar si se aplicó: consulta el resultado de la propuesta o el panel antes de repetir")
	}
	return notice, nil
}

// ---------- config_mesas (dos | tres) ----------

func botConfigMesasTable(kind string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "dos":
		return "mesas_de_dos", "MESAS DE DOS", nil
	case "tres":
		return "mesas_de_tres", "MESAS DE TRES", nil
	}
	return "", "", errors.New("table_kind inválido: usa \"dos\" o \"tres\"")
}

func botConfigMesasDedupKey(rid int, table, date, limit string) string {
	return botSha(fmt.Sprintf("cfgmesas|%d|%s|%s|%s", rid, table, date, limit))
}

func botConfigMesasBeforeKey(cur *string) string {
	v := any(nil)
	if cur != nil {
		v = *cur
	}
	return botSha("cfgmesas_before|" + botCanon(map[string]any{"limit": v}))
}

func botConfigMesasAfterKey(rid int, table, date, limit string) string {
	return botSha("cfgmesasv1|" + botCanon(map[string]any{"rid": rid, "table": table, "date": date, "limit": limit}))
}

func (s *Server) botToolConfigMesasPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in botConfigMesasArgs
	if msgOut, failed := botConfigDecode(input, &in, "table_kind (\"dos\" o \"tres\"), date (YYYY-MM-DD) y limit (número 0-999 o \"sin_limite\")"); failed {
		return msgOut, nil
	}
	table, label, kerr := botConfigMesasTable(in.TableKind)
	if kerr != nil {
		return botJSON(map[string]any{"error": kerr.Error()}), nil
	}
	date, err := botConfigValidateDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	// Same normalization the panel applies (normalizeLimitInput):
	// "sin_limite"->999, 0..999, canonical Itoa.
	limit, lerr := normalizeLimitInput(in.Limit)
	if lerr != nil {
		return botJSON(map[string]any{"error": lerr.Error()}), nil
	}
	in.Date = date
	in.Limit = limit
	cur, cerr := botConfigMesasBefore(ctx, s.db, rid, table, date, false)
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer el estado actual"}), nil
	}
	in.DedupKey = botConfigMesasDedupKey(rid, table, date, limit)
	code, codeErr := botSpecialNewCode()
	if codeErr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	shown := limit
	if limit == "999" {
		shown = "sin límite (999)"
	}
	preview := fmt.Sprintf("Propuesta: fijar el límite diario de %s para el %s en %s. Valor actual: (actual oculto). Efecto inmediato en la disponibilidad que ven los clientes; no se envía ningún aviso.", label, date, shown)
	before := botConfigMesasBeforeKey(cur)
	after := botConfigMesasAfterKey(rid, table, date, limit)
	return s.botProposalPersist(ctx, rid, msg, "config_mesas", 0, in, in.DedupKey, actor, origin, before, after, preview, code)
}

func (s *Server) botConfigMesasExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botConfigMesasArgs
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	table, label, kerr := botConfigMesasTable(in.TableKind)
	if kerr != nil {
		return stale("la validación ya no pasa: " + kerr.Error())
	}
	date, err := botConfigValidateDate(in.Date)
	if err != nil {
		return stale("la validación ya no pasa: " + err.Error())
	}
	limit, lerr := normalizeLimitInput(in.Limit)
	if lerr != nil {
		return stale("la validación ya no pasa: " + lerr.Error())
	}
	if botConfigMesasDedupKey(rid, table, date, limit) != in.DedupKey {
		return stale("la propuesta no coincide con los datos validados; pide una propuesta nueva")
	}
	cur, cerr := botConfigMesasBefore(ctx, tx, rid, table, date, true)
	if cerr != nil {
		return "", errors.New("no se pudo releer el estado actual; no se aplicó nada")
	}
	if botConfigMesasBeforeKey(cur) != p.Before {
		return stale("el estado actual cambió desde la vista previa (alguien lo tocó); pide una propuesta nueva")
	}
	if botConfigMesasAfterKey(rid, table, date, limit) != p.After {
		return stale("los datos resueltos cambiaron desde la vista previa; pide una propuesta nueva")
	}
	// Same upsert as the panel handlers.
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" (restaurant_id, reservationDate, dailyLimit) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE dailyLimit = VALUES(dailyLimit)", rid, date, limit); err != nil {
		return "", errors.New("no se pudo actualizar " + table + "; no se aplicó nada")
	}
	back, berr := botConfigMesasBefore(ctx, tx, rid, table, date, false)
	if berr != nil || back == nil || *back != limit {
		return "", errors.New("la verificación posterior no coincide; no se aplicó nada")
	}
	shown := limit
	if limit == "999" {
		shown = "sin límite (999)"
	}
	notice := fmt.Sprintf("Límite diario de %s para el %s fijado en %s.", label, date, shown)
	rj, _ := json.Marshal(map[string]any{"op": p.Op, "table": table, "date": date, "limit": limit, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "table": table, "date": date, "limit": limit, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		return "", errors.New("no pude confirmar si se aplicó: consulta el resultado de la propuesta o el panel antes de repetir")
	}
	return notice, nil
}

// ---------- config_salon_condesa ----------

func botConfigSalonCondesaDedupKey(rid int, date string, state bool) string {
	return botSha(fmt.Sprintf("cfgcondesa|%d|%s|%t", rid, date, state))
}

func botConfigSalonCondesaBeforeKey(cur *int) string {
	v := any(nil)
	if cur != nil {
		v = *cur
	}
	return botSha("cfgcondesa_before|" + botCanon(map[string]any{"state": v}))
}

func botConfigSalonCondesaAfterKey(rid int, date string, state bool) string {
	return botSha("cfgcondesav1|" + botCanon(map[string]any{"rid": rid, "date": date, "state": state}))
}

func (s *Server) botToolConfigSalonCondesaPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in botConfigSalonCondesaArgs
	if msgOut, failed := botConfigDecode(input, &in, "date (YYYY-MM-DD) y state (true=activado, false=desactivado)"); failed {
		return msgOut, nil
	}
	date, err := botConfigValidateDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	if in.State == nil {
		return botJSON(map[string]any{"error": "falta state: true para activar, false para desactivar"}), nil
	}
	in.Date = date
	cur, cerr := botConfigSalonCondesaBefore(ctx, s.db, rid, date, false)
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer el estado actual"}), nil
	}
	in.DedupKey = botConfigSalonCondesaDedupKey(rid, date, *in.State)
	code, codeErr := botSpecialNewCode()
	if codeErr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	estado := "DESACTIVAR"
	if *in.State {
		estado = "ACTIVAR"
	}
	preview := fmt.Sprintf("Propuesta: %s el salón condesa para el %s. Estado actual: (actual oculto). Efecto inmediato en la disponibilidad que ven los clientes; no se envía ningún aviso.", estado, date)
	before := botConfigSalonCondesaBeforeKey(cur)
	after := botConfigSalonCondesaAfterKey(rid, date, *in.State)
	return s.botProposalPersist(ctx, rid, msg, "config_salon_condesa", 0, in, in.DedupKey, actor, origin, before, after, preview, code)
}

func (s *Server) botConfigSalonCondesaExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botConfigSalonCondesaArgs
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	date, err := botConfigValidateDate(in.Date)
	if err != nil {
		return stale("la validación ya no pasa: " + err.Error())
	}
	if in.State == nil {
		return stale("la propuesta no tiene estado; pide una propuesta nueva")
	}
	if botConfigSalonCondesaDedupKey(rid, date, *in.State) != in.DedupKey {
		return stale("la propuesta no coincide con los datos validados; pide una propuesta nueva")
	}
	cur, cerr := botConfigSalonCondesaBefore(ctx, tx, rid, date, true)
	if cerr != nil {
		return "", errors.New("no se pudo releer el estado actual; no se aplicó nada")
	}
	if botConfigSalonCondesaBeforeKey(cur) != p.Before {
		return stale("el estado actual cambió desde la vista previa (alguien lo tocó); pide una propuesta nueva")
	}
	if botConfigSalonCondesaAfterKey(rid, date, *in.State) != p.After {
		return stale("los datos resueltos cambiaron desde la vista previa; pide una propuesta nueva")
	}
	stateInt := 0
	if *in.State {
		stateInt = 1
	}
	// Same upsert as handleBOConfigSalonCondesaSet.
	if _, err := tx.ExecContext(ctx, "INSERT INTO salon_condesa (restaurant_id, date, state) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE state = VALUES(state)", rid, date, stateInt); err != nil {
		return "", errors.New("no se pudo actualizar salon_condesa; no se aplicó nada")
	}
	back, berr := botConfigSalonCondesaBefore(ctx, tx, rid, date, false)
	if berr != nil || back == nil || *back != stateInt {
		return "", errors.New("la verificación posterior no coincide; no se aplicó nada")
	}
	estado := "desactivado"
	if *in.State {
		estado = "activado"
	}
	notice := fmt.Sprintf("Salón condesa %s para el %s.", estado, date)
	rj, _ := json.Marshal(map[string]any{"op": p.Op, "date": date, "state": *in.State, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "date": date, "state": *in.State, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		return "", errors.New("no pude confirmar si se aplicó: consulta el resultado de la propuesta o el panel antes de repetir")
	}
	return notice, nil
}
