-- Forky voice notes (coord forky_media_transcribe_v1): the clip's BunnyCDN
-- pull URL lives on the message row itself, so a reloaded chat session
-- replays the audio on the exact message (and timestamp) that produced it.
-- The old client-side registry died with the tab.
--
-- content keeps the whisper transcript: that is the text the model reads as
-- the user's prompt, and the muted caption the UI shows under the player.
ALTER TABLE assistant_messages
  ADD COLUMN audio_url VARCHAR(512) NULL DEFAULT NULL,
  ADD COLUMN audio_duration_ms INT UNSIGNED NOT NULL DEFAULT 0;
