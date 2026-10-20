-- Coordination id: camp-email-retries
-- Bounded retry of e-mail rows that Titan explicitly refused with a TEMPORARY
-- SMTP reply (421/450/451/452/454: the message was NOT accepted).
-- done: 0 = waiting, 1 = finished (sent, skipped or attempts exhausted),
--       2 = claimed by a worker (never retried again if it is left in this
--           state: an interrupted send is ambiguous and must not duplicate).
CREATE TABLE IF NOT EXISTS campaign_email_retries (
  recipient_id BIGINT NOT NULL,
  attempts INT NOT NULL DEFAULT 1,
  next_at DATETIME NOT NULL,
  done TINYINT NOT NULL DEFAULT 0,
  last_error VARCHAR(300) DEFAULT NULL,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (recipient_id),
  KEY idx_email_retry_due (done, next_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
