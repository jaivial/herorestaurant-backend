-- When part of a line that the kitchen has ALREADY been told about moves to
-- another check (one comensal takes 1 of the 3 beers onto their own bill), the
-- dish itself does not change: it is cooked once, and it is still cooked.
--
-- The kitchen's "what has been sent" is the sum of dispatch deltas per line. A
-- partial move splits the line into two rows, so without a record of the split
-- the original row would look over-sent (the next comanda tells the kitchen to
-- VOID one) and the new row would look unsent (the next comanda tells the
-- kitchen to cook it AGAIN). Both are wrong, and both cost real food.
--
-- A transfer moves the "already sent" quantity, per station, from one line to
-- the other. sent(line) = SUM(dispatch deltas) + SUM(transfers in) - SUM(out).
-- Rows are only ever inserted, never updated: the history is the audit.
CREATE TABLE IF NOT EXISTS pos_kitchen_line_transfers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  station_id BIGINT UNSIGNED NOT NULL,
  from_line_id BIGINT UNSIGNED NOT NULL,
  to_line_id BIGINT UNSIGNED NOT NULL,
  quantity DECIMAL(12,3) NOT NULL,
  created_by INT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_pos_kitchen_transfer_from (restaurant_id, from_line_id),
  KEY idx_pos_kitchen_transfer_to (restaurant_id, to_line_id),
  CONSTRAINT fk_pos_kitchen_transfer_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_pos_kitchen_transfer_station FOREIGN KEY (restaurant_id, station_id) REFERENCES pos_kitchen_stations (restaurant_id, id),
  CONSTRAINT fk_pos_kitchen_transfer_from FOREIGN KEY (restaurant_id, from_line_id) REFERENCES pos_ticket_lines (restaurant_id, id),
  CONSTRAINT fk_pos_kitchen_transfer_to FOREIGN KEY (restaurant_id, to_line_id) REFERENCES pos_ticket_lines (restaurant_id, id),
  CONSTRAINT chk_pos_kitchen_transfer_qty CHECK (quantity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
