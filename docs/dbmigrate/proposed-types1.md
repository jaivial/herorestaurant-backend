# proposed-types1 (PROPOSAL ONLY, applied: false)

Nothing in this document is applied. It does not touch internal/db/migrations, the immutable PG plan r4, or the r5 proof. Nothing here has been executed.

## TIME

No duration columns exist, so no plan revision is needed. Every MySQL TIME column is a clock time of day:

- bookings.reservation_time
- member_time_entries.start_time and end_time
- member_work_schedules.start_time and end_time
- pos_service_periods.start_time and end_time
- pos_settings.business_day_cutoff

The existing time(0) mapping is kept. If a duration column is added in future, map it to PG `interval` (or integer seconds) in a forward migration and change its callers in the same change.

## NUL in JSON

PG jsonb rejects U+0000 with error 22P05. Of roughly 75 jsonb columns, the source inventory found **none** containing NUL; the only NUL in the source is assistant_messages.content (one row), which already has a bytea adapter. New writes from user or LLM text could still contain NUL.

Options, per column, decided only after a reader audit:

- **(a)** Keep jsonb and fail the write explicitly. This is the current state: outbox payload_json only. The other columns get the raw 22P05 error.
- **(b)** Add a companion `<col>_raw bytea` that stores the exact original JSON when it contains NUL, plus a flag. Every reader must then prefer _raw. This is not done because readers use jsonb operators in SQL.
- **(c)** Change the column to text holding JSON (lossless). This breaks the jsonb operators in queries, so each one needs auditing first.

Recommended order: start with message_deliveries.payload_json, assistant_tool_audit.arguments_json and audit_log, because they carry user or LLM content.

