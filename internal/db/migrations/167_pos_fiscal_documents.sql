-- Spanish fiscal groundwork for the till.
--
-- WHAT THIS IS, precisely: the internal structure a "factura simplificada"
-- needs so that numbering is per terminal and continuous, so a refund issues
-- a "factura rectificativa" that points back at the document it corrects, and
-- so every issued document is chained by a hash to the previous one in its
-- series. That chain is the shape VERI*FACTU requires, recorded ahead of time.
--
-- WHAT THIS IS NOT: it is NOT certified, NOT signed with a qualified
-- certificate, NOT remitted to the AEAT/BaiZip/any channel, and NOT compliant
-- with Real Decreto 1007/2023 on its own. Nothing here may be presented to a
-- guest or an inspector as a compliant fiscal document. The UI keeps saying
-- "no fiscal" for that reason; see docs/pos-feature-gap.md (G8).
--
-- Why store the hash chain anyway: when the certified component arrives, the
-- numbers and the chain are already historical facts. Renumbering later would
-- invalidate every issued document, so the series and the chain have to exist
-- from the first invoice even though the seal does not exist yet.
--
-- Hash chain: each document stores the SHA-256 of its own canonical content
-- plus the previous document's hash in the same series. Tampering with or
-- deleting a document breaks every later link, which is the property that
-- makes the chain useful rather than decorative.

CREATE TABLE IF NOT EXISTS pos_fiscal_series (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    -- Which till issues from this series. 'main' is the single-terminal
    -- default; a second till gets its own row so each has its own numbering,
    -- which is what the AEAT expects for separate series.
    terminal_key VARCHAR(64) NOT NULL DEFAULT 'main',
    -- The letters printed in front of the number, e.g. 'FS' for simplificada
    -- and 'FR' for rectificativa.
    series_prefix VARCHAR(16) NOT NULL,
    -- Document type this series issues.
    series_type ENUM('SIMPLIFICADA','RECTIFICATIVA') NOT NULL,
    -- Next number to issue within the series. Never reused, never rewound.
    next_number BIGINT UNSIGNED NOT NULL DEFAULT 1,
    -- Hash of the last document issued from this series, NULL at the start.
    -- The chain seed, so a new document can link to its predecessor.
    last_hash CHAR(64) NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_fiscal_series_tenant_id (restaurant_id, id),
    -- One series per restaurant, terminal and type. A till cannot end up with
    -- two competing simplificada series, which would make the numbering
    -- ambiguous exactly where the law requires it to be unambiguous.
    UNIQUE KEY uq_pos_fiscal_series_scope (restaurant_id, terminal_key, series_type),
    CONSTRAINT fk_pos_fiscal_series_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT chk_pos_fiscal_series_number CHECK (next_number >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_fiscal_documents (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    series_id BIGINT UNSIGNED NOT NULL,
    -- The document number inside its series, 1, 2, 3... Never reused.
    series_number BIGINT UNSIGNED NOT NULL,
    document_type ENUM('SIMPLIFICADA','RECTIFICATIVA') NOT NULL,
    -- Full human-readable number as printed, e.g. 'FS-2026-0007'.
    full_number VARCHAR(48) NOT NULL,
    -- Terminal that issued it, copied from the series so the document stands on
    -- its own even if the series row is later edited.
    terminal_key VARCHAR(64) NOT NULL DEFAULT 'main',
    issued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- The sale this document bills. A rectificativa points at the original
    -- document through corrects_document_id, not here. Both are ON DELETE SET
    -- NULL so deleting a draft ticket cannot cascade away an issued invoice:
    -- the fiscal record has to outlive the sale it describes. MySQL requires the
    -- leading column of a SET NULL composite key to be nullable, hence the
    -- explicit NULL default on restaurant_id usage here.
    ticket_id BIGINT UNSIGNED NULL,
    visit_id BIGINT UNSIGNED NULL,
    -- For a rectificativa: the document being corrected. NULL for a normal
    -- simplified invoice.
    corrects_document_id BIGINT UNSIGNED NULL,
    -- The corrected document's printed number, copied so a rectifying invoice
    -- names what it corrects even if the original is later archived elsewhere.
    corrects_number VARCHAR(48) NULL,
    -- Reason a rectificativa was issued (required by the rectified-invoice
    -- rules; NULL on a normal invoice).
    correction_reason VARCHAR(255) NULL,
    -- Issuer: the restaurant. Its NIF is the seller's tax id.
    issuer_name VARCHAR(180) NOT NULL,
    issuer_tax_id VARCHAR(40) NOT NULL,
    -- Buyer. Left null for a consumer, which is the whole point of a
    -- simplified invoice; set when a NIF was given.
    customer_name VARCHAR(180) NULL,
    customer_tax_id VARCHAR(40) NULL,
    -- Money, in integer cents, with the VAT already inside the gross amount
    -- exactly as the POS stores it (base = gross / (1 + rate)).
    base_cents BIGINT NOT NULL,
    tax_cents BIGINT NOT NULL,
    surcharge_cents BIGINT NOT NULL DEFAULT 0,
    discount_cents BIGINT NOT NULL DEFAULT 0,
    total_cents BIGINT NOT NULL,
    -- Per-rate VAT breakdown as a JSON object keyed by rate, e.g. '{"10":300}'.
    -- The same integer-cent arithmetic the ticket shows.
    vat_breakdown_json VARCHAR(512) NULL,
    -- Lines snapshot, JSON, so the document keeps saying what it said even if
    -- the product is renamed or repriced later.
    lines_json MEDIUMTEXT NULL,
    -- Duplicate ("duplicado") copies share the document but carry their own
    -- copy number so the guest's copy and the restaurant's file copy are
    -- distinguishable in an audit.
    copy_number INT NOT NULL DEFAULT 1,
    -- SHA-256 over this document's canonical content.
    content_hash CHAR(64) NOT NULL,
    -- content_hash of the previous document in the same series.
    previous_hash CHAR(64) NULL,
    -- Honest about what this is. Always true today: nothing here is certified
    -- or filed. It exists so that a future migration to a real VERI*FACTU
    -- chain can find exactly the rows that still need a seal, instead of
    -- guessing from timestamps.
    is_certified TINYINT(1) NOT NULL DEFAULT 0,
    filed_at DATETIME NULL,
    external_id VARCHAR(120) NULL,
    created_by INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_fiscal_documents_tenant_id (restaurant_id, id),
    -- A number is issued once per series, forever. This unique key is the
    -- actual guarantee behind "no number is ever reused"; the counter in
    -- pos_fiscal_series is the fast path, this is the backstop.
    UNIQUE KEY uq_pos_fiscal_documents_number (series_id, series_number),
    UNIQUE KEY uq_pos_fiscal_documents_full (restaurant_id, full_number, copy_number),
    KEY idx_pos_fiscal_documents_ticket (restaurant_id, ticket_id),
    KEY idx_pos_fiscal_documents_issued (restaurant_id, issued_at),
    CONSTRAINT fk_pos_fiscal_documents_series FOREIGN KEY (series_id) REFERENCES pos_fiscal_series(id) ON DELETE RESTRICT,
    CONSTRAINT fk_pos_fiscal_documents_ticket FOREIGN KEY (restaurant_id, ticket_id) REFERENCES pos_tickets(restaurant_id, id) ON DELETE RESTRICT,
    -- RESTRICT, not CASCADE: deleting an issued invoice that another invoice
    -- corrects would silently orphan the correction. The series is append-only
    -- and a correction is not reversible by dropping a row.
    CONSTRAINT fk_pos_fiscal_documents_corrects FOREIGN KEY (restaurant_id, corrects_document_id) REFERENCES pos_fiscal_documents(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_pos_fiscal_documents_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT chk_pos_fiscal_documents_money CHECK (base_cents >= 0 AND tax_cents >= 0 AND total_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
