-- Migration 048: Invoice comments table
CREATE TABLE IF NOT EXISTS bo_invoice_comments (
  id INT NOT NULL AUTO_INCREMENT,
  invoice_id INT NOT NULL,
  content TEXT NOT NULL,
  user_id INT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_bo_invoice_comments_invoice (invoice_id),
  CONSTRAINT fk_bo_invoice_comments_invoice FOREIGN KEY (invoice_id) REFERENCES invoices(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_invoice_comments_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
