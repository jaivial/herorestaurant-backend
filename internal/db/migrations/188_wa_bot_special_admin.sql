-- Coordination id: wa_bot_special_admin_v2
-- Durable proposals for the privileged-group special-booking tools. A proposal
-- is a server-stored, exact preview; it is applied only by an exact "OK <code>"
-- from the same verified actor in the same ORIGIN chat (the group, or that
-- member's own one-to-one chat), a current member of scope_group_jid, inside a single transaction
-- that also claims the proposal and stores the result.
-- State machine (mutable row): pending -> sent -> done | stale | expired |
-- superseded. `claimed` is never persisted: claim, write and done share one tx.
-- Only code_hash is stored (never the plaintext code).

CREATE TABLE IF NOT EXISTS wa_bot_op_proposals (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  scope_group_jid VARCHAR(96) NOT NULL,
  origin_chat_jid VARCHAR(96) NOT NULL,
  actor_jid VARCHAR(96) NOT NULL,
  source_message_id VARCHAR(96) NOT NULL DEFAULT '',
  op VARCHAR(48) NOT NULL,
  booking_id BIGINT NOT NULL,
  args_json MEDIUMTEXT NOT NULL,
  before_hash CHAR(64) NOT NULL,
  after_hash CHAR(64) NOT NULL,
  preview_text TEXT NOT NULL,
  code_hash CHAR(64) NOT NULL,
  state VARCHAR(16) NOT NULL DEFAULT 'pending',
  active TINYINT NULL DEFAULT 1,
  expires_at DATETIME NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  done_at DATETIME NULL,
  result_json TEXT NULL,
  notify_state VARCHAR(12) NOT NULL DEFAULT 'none',
  PRIMARY KEY (id),
  UNIQUE KEY uq_wa_op_active (restaurant_id, booking_id, active),
  KEY idx_wa_op_lookup (restaurant_id, scope_group_jid, origin_chat_jid, actor_jid, state),
  KEY idx_wa_op_notify (restaurant_id, notify_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Append-only audit: the code only ever INSERTs here (no UPDATE/DELETE).
-- detail_json holds hashes, states and amounts, never PII or codes.
CREATE TABLE IF NOT EXISTS wa_bot_op_events (
  id BIGINT NOT NULL AUTO_INCREMENT,
  proposal_id BIGINT NOT NULL DEFAULT 0,
  restaurant_id INT NOT NULL,
  event VARCHAR(32) NOT NULL,
  actor_jid VARCHAR(96) NOT NULL DEFAULT '',
  group_jid VARCHAR(96) NOT NULL DEFAULT '', -- origin chat
  detail_json TEXT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_wa_ev_prop (proposal_id),
  KEY idx_wa_ev_rate (restaurant_id, event, actor_jid, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
