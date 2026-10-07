-- One account per comensal.
--
-- The POS could already split a table into several checks ("Separar comanda"),
-- but the checks were anonymous: with three guests on a table the waiter saw
-- "Cuenta 1 / Cuenta 2 / Cuenta 3" and no way to know which plate went to whom,
-- which is exactly when separate checks matter most (a table that wants to pay
-- apart, or one guest paying for their own share).
--
-- guest_label is the comensal's name as written on the check. It is optional and
-- never required: the account works exactly the same without it, so a table that
-- does not want to give names is not forced into a guest-management form.
--
-- 60 chars is enough for "Mesa 4 - Pérez, Rodríguez y Martínez" style labels
-- and short enough to print on a thermal receipt line without wrapping badly.
ALTER TABLE pos_tickets
    ADD COLUMN guest_label VARCHAR(60) NULL AFTER ticket_number,
    ADD INDEX idx_pos_tickets_guest (restaurant_id, visit_id, guest_label);
