-- Documents attached to a booking (menus, event dossiers, PDFs, sheets...).
-- Coordination id: booking_documents_v1
--
-- WHY A DRAFT STATE EXISTS: in /app/reservas/anadir the staff attach files
-- BEFORE the booking exists (the row is only created on "complete booking").
-- So a document is first a DRAFT: restaurant_id + uploaded_by identify its
-- owner, booking_id stays NULL, and the booking-create call binds the ids it
-- was given. A draft that is never bound stays invisible to every other
-- screen and can only be deleted by the user who uploaded it.
--
-- STORAGE ZONE: the restaurant's regular BunnyCDN zone (bunnyPut /
-- bunnyPullURL / bunnyDelete), NOT the private one, because a booking document
-- is a guest-facing artifact: the WhatsApp gateway fetches it by URL to forward
-- it, exactly as it already does for the receipt PDF and the booking QR. The
-- object key ends in a uuid, so the URL is unguessable.
--
-- title is editable and is what the customer sees; original_filename is kept
-- verbatim for the download's Content-Disposition. size_bytes is the STORED
-- size (images may be re-encoded to webp, so it can differ from the upload).
CREATE TABLE IF NOT EXISTS booking_documents (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  -- NULL while the document is a draft; set by the booking-create call.
  booking_id BIGINT UNSIGNED NULL,
  -- Draft owner: the backoffice user who uploaded it. A draft is private to
  -- its uploader until it is bound to a booking.
  uploaded_by INT NOT NULL,
  title VARCHAR(255) NOT NULL DEFAULT '',
  original_filename VARCHAR(255) NOT NULL DEFAULT '',
  content_type VARCHAR(150) NOT NULL DEFAULT 'application/octet-stream',
  size_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
  -- Object key inside the restaurant's BunnyCDN zone.
  storage_path VARCHAR(512) NOT NULL DEFAULT '',
  deleted_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_booking_documents_tenant_id (restaurant_id, id),
  KEY idx_booking_documents_booking (restaurant_id, booking_id, deleted_at, id),
  KEY idx_booking_documents_draft (restaurant_id, uploaded_by, booking_id, created_at),
  CONSTRAINT fk_booking_documents_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- "Enviar copia al cliente": when true the booking's documents travel with the
-- confirmation email and the confirmation WhatsApp message. Same shape as
-- bookings.is_event (booking_is_event_v1): a plain flag the editor toggles.
SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'send_documents_to_client'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE bookings ADD COLUMN send_documents_to_client TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
