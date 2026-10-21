package api

// Admin extras catalog (slice 4j): booking_propose_catalog_extra_create and
// booking_propose_catalog_extra_delete manage the restaurant's CUSTOM extras
// catalog (restaurant_booking_extras, migration 138), mirroring the panel's
// handleBOBookingExtrasCreate/Delete (extras.go:214-255).
//
// Canonical contract:
//   - create: name trimmed, 1-255 chars, slug = beverageOptionSlug(name);
//     INSERT ... ON DUPLICATE KEY UPDATE name=VALUES(name), active=1 — so an
//     existing slug is REACTIVATED (soft-deleted) or RENAMED (name casing
//     changed), never duplicated. is_custom=1, active=1.
//   - delete: soft UPDATE active=0 WHERE id AND restaurant_id AND is_custom=1.
//     The panel's handler returns success even when nothing changed; the bot
//     instead REJECTS an unknown, built-in or already inactive id (declared
//     fail-closed deviation: a silent no-op approval would be a lie to the
//     operator).
//   - bookings carry a SNAPSHOT (extras_json): deleting a catalog entry never
//     touches existing bookings; the preview says so.
//
// These ops have no natural booking: they share the single booking_id=0
// proposal slot per tenant (botProposalPersist dedup path), so a pending
// create/catalog/config proposal is superseded atomically by any new one,
// with a generic notice and no foreign data.
//
// Concurrency: the slug/id row is re-read FOR UPDATE inside the claim
// transaction and must match the proposed before-state; the final row is
// re-read and must match the bound after-state. The UNIQUE (restaurant_id,
// slug) key and the resurrection semantics are enforced at execute time, not
// just propose.
//
// Coordination id: wa_bot_admin_catalog_extra_v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

type botCatalogExtraRow struct {
	ID     int64
	Slug   string
	Name   string
	Custom bool
	Active bool
	Found  bool
}

func botScanCatalogExtra(row *sql.Row) (botCatalogExtraRow, error) {
	var r botCatalogExtraRow
	var custom, active int
	err := row.Scan(&r.ID, &r.Slug, &r.Name, &custom, &active)
	if err == sql.ErrNoRows {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.Custom, r.Active, r.Found = custom != 0, active != 0, true
	return r, nil
}

// botCatalogBeforeKey binds the exact before-state of the slug's row.
func botCatalogBeforeKey(r botCatalogExtraRow) string {
	if !r.Found {
		return botSha("catextra-before|absent")
	}
	return botSha("catextra-before|" + botCanon(map[string]any{"id": r.ID, "name": r.Name, "is_custom": r.Custom, "active": r.Active}))
}

// botCatalogCreateAfterKey binds the exact intended final state.
func botCatalogCreateAfterKey(slug, name string) string {
	return botSha("catextra-after|" + botCanon(map[string]any{"slug": slug, "name": name, "active": 1}))
}

func botCatalogDeleteAfterKey(id int64) string {
	return botSha("catextra-after|" + botCanon(map[string]any{"id": id, "active": 0}))
}

// botToolCatalogExtraCreatePropose previews creating (or reactivating /
// renaming) a custom catalog extra.
func (s *Server) botToolCatalogExtraCreatePropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		Name string `json:"name"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		return botJSON(map[string]any{"error": "argumentos inválidos: name"}), nil
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 255 {
		return botJSON(map[string]any{"error": "nombre inválido (1-255 caracteres)"}), nil
	}
	slug := beverageOptionSlug(name)
	if slug == "" {
		return botJSON(map[string]any{"error": "ese nombre no genera un identificador válido"}), nil
	}
	cur, err := botScanCatalogExtra(s.db.QueryRowContext(ctx, `SELECT id, slug, name, is_custom, active FROM restaurant_booking_extras WHERE restaurant_id = ? AND slug = ?`, rid, slug))
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo leer el catálogo"}), nil
	}
	if cur.Found && !cur.Custom {
		// The panel's upsert would happily rename/reactivate a BUILT-IN extra
		// with the same slug; the bot never touches built-ins (same rule as
		// delete), so a colliding name must be changed, not forced.
		return botJSON(map[string]any{"error": "ese identificador corresponde a un extra fijo del sistema; usa otro nombre"}), nil
	}
	if cur.Found && cur.Active && cur.Name == name {
		return botJSON(map[string]any{"error": "ese extra ya existe y está activo"}), nil
	}
	action := "crea el extra"
	switch {
	case cur.Found && !cur.Active:
		action = "reactiva el extra (existía desactivado)"
	case cur.Found && cur.Active && cur.Name != name:
		action = "renombra el extra (mismo identificador, nombre distinto)"
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: CATÁLOGO DE EXTRAS — %s «%s» (identificador %s). El extra queda disponible para nuevas reservas; las reservas existentes no cambian (guardan su propia copia). No se avisa a ningún cliente.", action, name, slug)
	args := map[string]any{"booking_id": 0, "name": name, "slug": slug}
	return s.botProposalPersist(ctx, rid, msg, "catalog_extra_create", 0, args, "catextra:"+slug, actor, origin, botCatalogBeforeKey(cur), botCatalogCreateAfterKey(slug, name), preview, code)
}

// botToolCatalogExtraDeletePropose previews soft-deleting a custom extra.
func (s *Server) botToolCatalogExtraDeletePropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		ExtraID int64 `json:"extra_id"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.ExtraID <= 0 {
		return botJSON(map[string]any{"error": "argumentos inválidos: extra_id"}), nil
	}
	cur, err := botScanCatalogExtra(s.db.QueryRowContext(ctx, `SELECT id, slug, name, is_custom, active FROM restaurant_booking_extras WHERE restaurant_id = ? AND id = ?`, rid, in.ExtraID))
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo leer el catálogo"}), nil
	}
	if !cur.Found {
		return botJSON(map[string]any{"error": "no hay un extra con ese id"}), nil
	}
	if !cur.Custom {
		return botJSON(map[string]any{"error": "ese extra es fijo del sistema y no se puede eliminar"}), nil
	}
	if !cur.Active {
		return botJSON(map[string]any{"error": "ese extra ya está desactivado"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: CATÁLOGO DE EXTRAS — desactiva el extra «%s» (id %d). Deja de ofrecerse en nuevas reservas. Las reservas que ya lo tienen NO cambian: guardan su propia copia. No se avisa a ningún cliente.", cur.Name, cur.ID)
	args := map[string]any{"booking_id": 0, "extra_id": cur.ID, "slug": cur.Slug}
	return s.botProposalPersist(ctx, rid, msg, "catalog_extra_delete", 0, args, "catextra:"+cur.Slug, actor, origin, botCatalogBeforeKey(cur), botCatalogDeleteAfterKey(cur.ID), preview, code)
}

// botCatalogExtraCreateExecute runs INSIDE the claim transaction: the slug row
// is re-read FOR UPDATE and must match the proposed before-state; the final
// row is re-read and must match the bound after-state.
func (s *Server) botCatalogExtraCreateExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if json.Unmarshal([]byte(p.Args), &in) != nil || in.Name == "" || in.Slug == "" {
		return "", errors.New("propuesta ilegible")
	}
	cur, err := botScanCatalogExtra(tx.QueryRowContext(ctx, `SELECT id, slug, name, is_custom, active FROM restaurant_booking_extras WHERE restaurant_id = ? AND slug = ? FOR UPDATE`, rid, in.Slug))
	if err != nil {
		return "", errors.New("error interno")
	}
	if botCatalogBeforeKey(cur) != p.Before {
		return stale("el catálogo cambió desde la vista previa; pide una propuesta nueva")
	}
	if cur.Found && !cur.Custom {
		// Bound by the before-hash already; explicit guard for clarity: the
		// upsert below must never rename/reactivate a built-in extra.
		return stale("ese identificador corresponde a un extra fijo del sistema; pide una propuesta nueva")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO restaurant_booking_extras (restaurant_id, slug, name, is_custom, active) VALUES (?, ?, ?, 1, 1) ON DUPLICATE KEY UPDATE name = VALUES(name), active = 1`, rid, in.Slug, in.Name); err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	final, err := botScanCatalogExtra(tx.QueryRowContext(ctx, `SELECT id, slug, name, is_custom, active FROM restaurant_booking_extras WHERE restaurant_id = ? AND slug = ?`, rid, in.Slug))
	if err != nil || !final.Found || !final.Active || final.Name != in.Name {
		return "", errors.New("verificación fallida: el extra no quedó como se propuso; no se aplicó nada")
	}
	if botCatalogCreateAfterKey(final.Slug, final.Name) != p.After {
		return "", errors.New("verificación fallida: el resultado no coincide con la vista previa; no se aplicó nada")
	}
	notice := fmt.Sprintf("Catálogo de extras: «%s» (%s) disponible para nuevas reservas. Las reservas existentes no cambian.", final.Name, final.Slug)
	rj, _ := json.Marshal(map[string]any{"op": p.Op, "extra_id": final.ID, "slug": final.Slug, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "extra_id": final.ID, "slug": final.Slug, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_catalog_extra_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; revisa el catálogo antes de repetir")
	}
	return notice, nil
}

// botCatalogExtraDeleteExecute runs INSIDE the claim transaction.
func (s *Server) botCatalogExtraDeleteExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in struct {
		ExtraID int64  `json:"extra_id"`
		Slug    string `json:"slug"`
	}
	if json.Unmarshal([]byte(p.Args), &in) != nil || in.ExtraID <= 0 {
		return "", errors.New("propuesta ilegible")
	}
	cur, err := botScanCatalogExtra(tx.QueryRowContext(ctx, `SELECT id, slug, name, is_custom, active FROM restaurant_booking_extras WHERE restaurant_id = ? AND id = ? FOR UPDATE`, rid, in.ExtraID))
	if err != nil {
		return "", errors.New("error interno")
	}
	if botCatalogBeforeKey(cur) != p.Before {
		return stale("el catálogo cambió desde la vista previa; pide una propuesta nueva")
	}
	res, err := tx.ExecContext(ctx, `UPDATE restaurant_booking_extras SET active = 0 WHERE id = ? AND restaurant_id = ? AND is_custom = 1`, in.ExtraID, rid)
	if err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", errors.New("verificación fallida: el extra no quedó como se propuso; no se aplicó nada")
	}
	final, err := botScanCatalogExtra(tx.QueryRowContext(ctx, `SELECT id, slug, name, is_custom, active FROM restaurant_booking_extras WHERE restaurant_id = ? AND id = ?`, rid, in.ExtraID))
	if err != nil || !final.Found || final.Active {
		return "", errors.New("verificación fallida: el extra no quedó como se propuso; no se aplicó nada")
	}
	if botCatalogDeleteAfterKey(final.ID) != p.After {
		return "", errors.New("verificación fallida: el resultado no coincide con la vista previa; no se aplicó nada")
	}
	notice := fmt.Sprintf("Catálogo de extras: «%s» (id %d) desactivado. Las reservas que ya lo tenían conservan su copia.", cur.Name, cur.ID)
	rj, _ := json.Marshal(map[string]any{"op": p.Op, "extra_id": cur.ID, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "extra_id": cur.ID, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_catalog_extra_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; revisa el catálogo antes de repetir")
	}
	return notice, nil
}
