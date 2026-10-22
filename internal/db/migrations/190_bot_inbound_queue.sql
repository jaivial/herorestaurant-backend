-- Coordination id: wa_bot_inbound_queue_v1
--
-- Durable inbound queue for the WhatsApp agent turns. The burst coalescer and
-- the turn semaphore were memory-only: a deploy or a crash silently dropped
-- every accepted-but-unprocessed turn (incident 2026-10-22 14:35, group Bot
-- Alqueria: the quick ack went out and the turn died with the container).
-- Every accepted inbound message is persisted BEFORE it enters the burst
-- coalescer; rows still pending/running at boot are replayed.
--
-- state: pending (accepted) | running (turn claimed it) | done (answered) |
-- failed (turn errored) | shed (load-shedding refused it) | expired (too old
-- to replay).
CREATE TABLE IF NOT EXISTS bot_inbound_queue (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT            NOT NULL,
    chat_key     VARCHAR(190)    NOT NULL,
    message_id   VARCHAR(120)    NOT NULL,
    payload_json MEDIUMTEXT      NOT NULL,
    state        VARCHAR(16)     NOT NULL DEFAULT 'pending',
    created_at   TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at   TIMESTAMP       NULL DEFAULT NULL,
    PRIMARY KEY (id),
    KEY idx_inbound_state (state, created_at),
    KEY idx_inbound_msg (restaurant_id, message_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
