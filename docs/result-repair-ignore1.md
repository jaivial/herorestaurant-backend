# result-repair-ignore1: do-not-contact suppression IGNORE safety + outbox width safety

Code only. `go build -p 2 ./...` exits 0, run twice. There were no tests, fixtures or harness, and nothing was executed. No SQL ran, nothing was read from or written to a DB, and nothing was sent. Runtime-verified count is 0. PG is still `ready:false`, and the server still refuses `DB_DRIVER=postgres`. The r5 proof is untouched.

Inventory r82 (`/tmp/villacarmen-bot-20261111/sql-inventory-r82.json`) has 2485 sites and 280 files. Classification is unchanged from r81:

| Kind | Sites |
|---|---|
| native | 845 |
| portable | 1472 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

- Blocked: 0. Provisional: 0. Helper-internal sites excluded: 13.
- These edits changed arguments and Go guards only. No SQL site was added or removed.

## Schema facts
These come from the PG plan r4 and the MySQL schema.json.

**`campaign_suppressions`**
- Columns: `target varchar(190)`, `reason varchar(255)`, `channel varchar(16)`, `source varchar(32)`.
- `booking_id` is nullable and has no foreign key.
- The only unique key is `uq_suppression(restaurant_id, channel, target)`. `id` is an identity column.
- Collation: ci_ai on PG, utf8mb4_general_ci on MySQL.
- Widths are counted in characters, not bytes, on both engines.

**`message_deliveries`**
- Columns: `delivery_key varchar(191)` (unique), `recipient varchar(255)`, `event varchar(64)`, `error text` (CHECK octet_length ≤ 65535), `payload_json jsonb`.

## Bugs found and fixed explicitly on both dialects
1. **Target truncated into a different recipient.** The bounce `pending_verify` insert used MySQL `IGNORE`, and the PG variant used `LEFT(target,190)`. Both cut an address over 190 characters down to 190. That stored a different target, and the original address stayed contactable.
   - **Fix:** a new shared helper `campaignSuppressionKey` in `internal/api/campaign_suppression_target.go`. It takes the canonical form (`campaignCanonicalTarget`) and checks it is valid UTF-8, has no NUL, and is at most 190 characters (counted as runes).
   - Writers now **skip** targets that fail the check instead of truncating them. The bounce path logs only the byte length, never the address.
   - The PG `LEFT` was removed.
2. **Fail-closed reader.** `campaignSuppressionFilter.blocked()` now returns true for any target that `campaignSuppressionKey` rejects. Such a target can never be stored, so it is never contactable.
   - Every send and requeue gate goes through `blocked()`: audience at `backoffice_campaigns.go` lines 799, 1354 and 1503, `campaign_auto_retake:200`, `campaign_email_retry:132`, and `campaign_email_validation:79`/`:87`.
   - **Behaviour change:** overlong or invalid targets are now excluded from campaigns. Email validation counts them as already suppressed.
3. **Byte limit applied where the column counts characters.** `public_unsubscribe` and `email_validation` checked `len()` in bytes against 190, so they silently refused valid multibyte targets of 190 characters or fewer. Those contacts were left unsuppressed. Both now use the character check.
4. **Over-long or invalid reasons.** Over 255 characters, PG raised 22001; with invalid bytes it raised 22021. Either way the do-not-contact row was lost.
   - This affected `whatsapp_bot_unsubscribe` and `whatsapp_unreachable`. `public_unsubscribe` was only partly protected by its `CAST`.
   - **Fix:** all three now pass the reason through `campaignSuppressionReason`, which replaces invalid bytes with U+FFFD, drops NUL, and cuts to 255 characters.
   - For valid text this equals the MySQL `IGNORE` character truncation. The replacement of invalid bytes is a technical choice for reason text only; it is never applied to a target.
5. **Bounce diagnostic split mid-character.** The 300-byte cut could end inside a character. PG then rejected the event insert (22021), the code hit `continue`, and the suppression was skipped.
   - **Fix:** `utf8BytePrefix(diag, 300)` keeps the 300-byte cut but backs off to a character boundary.
   - What MySQL actually stored for a split character is **still unproven**: there is no driver or warning evidence. The fix is labelled a technical fix, not equivalence.
   - A diagnostic that already contains invalid bytes before the cut point still fails on PG. That stays an open gate. The fix would be to decouple the suppression from the event insert error; this is proposed, not done.
6. **Outbox (`whatsapp_outbox.enqueueWhatsAppDelivery`).**
   - **Width refusal:** under MySQL `IGNORE`, an over-wide `delivery_key` was cut to 191. Two keys sharing that prefix then collided, and the second message was dropped silently. Over-wide `recipient`/`event` values were also cut.
     - **Fix:** the function now returns an explicit error on both dialects when the key exceeds 191, the recipient 255, or the event 64 characters.
   - **Cause-text fix:** the cause text used a byte cut that could split a character and fail on PG with 22021.
     - **Fix:** it now uses `utf8BytePrefix` plus `ToValidUTF8`.
   - **Unchanged:** the duplicate `delivery_key` still means "do nothing", as an idempotent enqueue.
7. **`standard_conforming_strings=on` is now pinned** in `sqldb` pool RuntimeParams. Every PG path goes through these pools: `OpenPostgres`, `OpenPostgresUTC`, and botstore through both.

## Remaining semantic gates (not waived)
- **Suppression lost to an accent-variant collision.** Under ci_ai on PG (or general_ci on MySQL), the unique key treats `josé@x` and `jose@x` as the same target. The second insert does nothing, but the Go filter matches exact keys, so the second variant stays contactable. This is the source's behaviour too.
  - Proposed fix: fold keys in the filter with the same collation semantics, or do a SQL-side collation lookup. Not done yet, because Go folding does not equal the collation.
- **Hard bounce ODKU / ON CONFLICT on targets over 190.** This path is now unreachable because of the skip above.
- **NUL in outbox payload text.** A NUL becomes `\u0000` in jsonb, which PG rejects. This is the existing NUL/JSON gate.
- **Diagnostic with mid-string invalid bytes on PG.** See item 5.
- All general gates stay open: 1117 import-proof pending (`ready:false`), 4 NOT VALID FKs with 731 orphans, IGNORE (categories 140→120, fiscal series), gap locks, NUL/JSON, search, TIME, UUID, multikey, collation/PAD SPACE/numeric, fiscal hash, 136 on-update triggers, bare jsonb `?`, Stripe key retention and processed reconciliation, domain DNS proof, and the MySQL-only utilities.

## Manual QA plan (later phase, local PG only)
- Insert each writer's suppression with:
  - a target of 190 vs 191 characters;
  - a multibyte target;
  - a reason of 300 characters;
  - a diagnostic split mid-character.
- Then verify the audience excludes each case.
- Verify an over-wide outbox key returns an error.

## Next
1. Remaining IGNORE sites: categories (140→120) and fiscal series.
2. Stripe processed flag and reconciliation.
3. Accent-collision suppression lookup.
4. Gap-lock advisory locks.
5. TIME / NUL / collation adapters.
6. Read-only local PG QA plan.

Files changed:
- `internal/api/campaign_suppression_target.go` (new)
- `backoffice_campaigns.go`
- `campaign_bounce_sync.go`
- `campaign_email_validation.go`
- `public_unsubscribe.go`
- `whatsapp_bot_unsubscribe.go`
- `whatsapp_unreachable.go`
- `whatsapp_outbox.go`
- `internal/sqldb/sqldb.go`

Backups: `/tmp/ign1.bak.tar`, `/tmp/ign1b.bak.tar`.

