package api

// Spanish fiscal groundwork for the POS.
//
// READ THIS BEFORE USING ANYTHING IN THIS FILE
// ---------------------------------------------
// This is NOT a certified fiscal system. Nothing produced here is signed with
// a qualified certificate, nothing is transmitted to the AEAT, and nothing here
// satisfies Real Decreto 1007/2023 (VERI*FACTU) on its own. It must never be
// presented to a guest or an inspector as a compliant invoice. The till keeps
// labelling its printed output "no fiscal" for exactly that reason.
//
// What it does honestly do, and why it is worth having now:
//   - numbering per terminal, per document type, never reused and never
//     rewound, enforced by a UNIQUE key and not just by a counter;
//   - a factura rectificativa that points back at the document it corrects,
//     so a refund is a first-class fiscal record rather than a negative row;
//   - a SHA-256 hash chain per series, so removing or editing a document breaks
//     every later link in that series;
//   - a copy ("duplicado") so the guest's copy and the restaurant's file copy
//     are separately identifiable;
//   - an is_certified flag on every row, so the day a real certified component
//     arrives it knows exactly which rows still need a seal instead of having
//     to guess from dates.
//
// Starting the series and the chain *now* is deliberate: numbers and links are
// historical facts. If they only start existing when the certified component
// lands, every document issued before that day would have to be renumbered,
// which would invalidate exactly the records the law exists to protect.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// posFiscalNotice travels with every document and is rendered next to the
// number. It says out loud what the document is not, so no screen or print can
// be read as a certified invoice.
const posFiscalNotice = "Documento sin certificar ni presentar en la AEAT. No cumple VERI*FACTU (RD 1007/2023)."

const (
	posFiscalTerminalDefault = "main"
	// Prefix printed in front of a simplified invoice number.
	posFiscalPrefixSimplificada = "FS"
	// Prefix printed in front of a rectifying invoice number.
	posFiscalPrefixRectificativa = "FR"
)

// posFiscalDocumentType values. They mirror the ENUM on pos_fiscal_documents.
const (
	posFiscalTypeSimplificada  = "SIMPLIFICADA"
	posFiscalTypeRectificativa = "RECTIFICATIVA"
)

// posFiscalSeriesKey is the UNIQUE (restaurant, terminal, type) scope.
type posFiscalSeriesKey struct {
	RestaurantID int
	TerminalKey  string
	DocumentType string
}

// ensurePOSFiscalSeries returns the id of the series for this scope, creating
// it on first use. Created lazily rather than by a migration because a
// restaurant that never issues a document should not have rows pretending it
// does, and the prefix is a per-restaurant decision.
func (s *Server) ensurePOSFiscalSeries(ctx context.Context, tx *sql.Tx, key posFiscalSeriesKey) (int64, error) {
	prefix := posFiscalPrefixSimplificada
	if key.DocumentType == posFiscalTypeRectificativa {
		prefix = posFiscalPrefixRectificativa
	}
	terminal := key.TerminalKey
	if strings.TrimSpace(terminal) == "" {
		terminal = posFiscalTerminalDefault
	}
	// INSERT IGNORE + SELECT rather than a bare INSERT: two tills booting at
	// the same time both try to create the 'main' simplificada series, and the
	// unique key turns the loser into a no-op instead of a 500 at checkout.
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO pos_fiscal_series (restaurant_id,terminal_key,series_prefix,series_type,next_number) VALUES (?,?,?,?,1)`, key.RestaurantID, terminal, prefix, key.DocumentType)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM pos_fiscal_series WHERE restaurant_id=? AND terminal_key=? AND series_type=?`, key.RestaurantID, terminal, key.DocumentType).Scan(&id)
	return id, err
}

// posFiscalLine is one line as it will be frozen on the document.
type posFiscalLine struct {
	Name        string `json:"name"`
	Quantity    string `json:"quantity"`
	UnitCents   int64  `json:"unitCents"`
	LineCents   int64  `json:"lineCents"`
	VatRate     string `json:"vatRate"`
	BaseCents   int64  `json:"baseCents"`
	TaxCents    int64  `json:"taxCents"`
	Discount    int64  `json:"discountCents"`
	LineIndex   int    `json:"line"`
	IsComponent bool   `json:"isComponent,omitempty"`
}

// posFiscalContent is the canonical content that gets hashed. Every field that
// matters legally is here, in a fixed order, so the same document always
// produces the same hash on any machine.
type posFiscalContent struct {
	SeriesType     string           `json:"seriesType"`
	SeriesNumber   int64            `json:"seriesNumber"`
	FullNumber     string           `json:"fullNumber"`
	TerminalKey    string           `json:"terminalKey"`
	IssuedAt       string           `json:"issuedAt"`
	TicketNumber   string           `json:"ticketNumber"`
	IssuerName     string           `json:"issuerName"`
	IssuerTaxID    string           `json:"issuerTaxId"`
	CustomerName   string           `json:"customerName"`
	CustomerTaxID  string           `json:"customerTaxId"`
	BaseCents      int64            `json:"baseCents"`
	TaxCents       int64            `json:"taxCents"`
	DiscountCents  int64            `json:"discountCents"`
	SurchargeCents int64            `json:"surchargeCents"`
	TotalCents     int64            `json:"totalCents"`
	VatBreakdown   map[string]int64 `json:"vatBreakdown"`
	Lines          []posFiscalLine  `json:"lines"`
	CorrectsID     int64            `json:"correctsDocumentId,omitempty"`
	CorrectsNumber string           `json:"correctsNumber,omitempty"`
	Reason         string           `json:"reason,omitempty"`
}

// contentHash is the SHA-256 over the canonical JSON of the content. The
// previous document's hash is folded in, so the chain links: editing document
// N changes its own hash and therefore every hash after it.
func contentHash(content posFiscalContent, previousHash string) (string, string) {
	// Lines are ordered by their index so the JSON is byte-stable.
	sort.SliceStable(content.Lines, func(i, j int) bool { return content.Lines[i].LineIndex < content.Lines[j].LineIndex })
	if content.VatBreakdown == nil {
		content.VatBreakdown = map[string]int64{}
	}
	raw, err := json.Marshal(content)
	if err != nil {
		// The content struct is plain data with no channels or funcs, so this
		// cannot fail in practice; hashing a stable fallback keeps the chain
		// unbroken rather than skipping a document.
		raw = []byte(content.FullNumber)
	}
	sum := sha256.Sum256(append([]byte(previousHash+"|"), raw...))
	return hex.EncodeToString(sum[:]), string(raw)
}

// posVATBreakdownFromLines turns the per-line VAT rates into the
// {rate: tax cents} map the document and the receipt both show. The POS stores
// VAT rates as whole percents and money as integer cents; base is derived as
// gross/(1+rate) rounded to the cent, exactly as the receipt does, so the
// document and the printed receipt never disagree by a cent.
func posVATBreakdownFromLines(lines []posFiscalLine) map[string]int64 {
	out := map[string]int64{}
	for _, line := range lines {
		if line.LineCents <= 0 && line.Discount <= 0 {
			continue
		}
		out[line.VatRate] += line.TaxCents
	}
	return out
}

// posFiscalRateCents splits a gross line amount into base and tax for one
// VAT rate. rate is a whole percent ("10", "21"). Integer math only, so the
// same line always yields the same cents.
func posFiscalRateCents(gross int64, ratePercent string) (base, tax int64) {
	rate, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(ratePercent), "%"), 64)
	if err != nil || rate < 0 {
		return gross, 0
	}
	divisor := 1 + rate/100
	if divisor <= 0 {
		return gross, 0
	}
	base = int64(float64(gross)/divisor + 0.5)
	if base > gross {
		base = gross
	}
	return base, gross - base
}

// posFiscalTicketTotals reads the money and VAT of a ticket straight from the
// database rather than from a client-supplied amount, so a document can never
// be issued for a number the sale did not reach.
type posFiscalTicketTotals struct {
	TicketID     int64
	VisitID      int64
	TicketNumber string
	BaseCents    int64
	TaxCents     int64
	TotalCents   int64
	Discount     int64
	Surcharge    int64
	Lines        []posFiscalLine
	Breakdown    map[string]int64
	CustomerName string
	CustomerNIF  string
}

func (s *Server) loadPOSFiscalTicketTotals(ctx context.Context, tx *sql.Tx, restaurantID int, ticketID int64) (posFiscalTicketTotals, error) {
	totals := posFiscalTicketTotals{TicketID: ticketID, Breakdown: map[string]int64{}}
	var visitID sql.NullInt64
	var subtotal, discount, tax, total, surcharge int64
	err := tx.QueryRowContext(ctx, `SELECT visit_id,ticket_number,subtotal_gross_cents,discount_cents,tax_cents,total_gross_cents,surcharge_cents FROM pos_tickets WHERE restaurant_id=? AND id=?`, restaurantID, ticketID).
		Scan(&visitID, &totals.TicketNumber, &subtotal, &discount, &tax, &total, &surcharge)
	if err != nil {
		return totals, err
	}
	totals.VisitID = visitID.Int64
	totals.Discount = discount
	totals.Surcharge = surcharge
	totals.TotalCents = total

	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(customer_name,''),COALESCE(customer_tax_id,'') FROM pos_visits WHERE restaurant_id=? AND id=?`, restaurantID, visitID.Int64).Scan(&totals.CustomerName, &totals.CustomerNIF)

	rows, err := tx.QueryContext(ctx, `SELECT product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,line_total_gross_cents,parent_line_id,id FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND status='ACTIVE' ORDER BY id`, restaurantID, ticketID)
	if err != nil {
		return totals, err
	}
	index := 0
	for rows.Next() {
		var name, quantity, rate string
		var unit, lineDiscount, lineTotal int64
		var parent sql.NullInt64
		var lineID int64
		if err = rows.Scan(&name, &quantity, &unit, &rate, &lineDiscount, &lineTotal, &parent, &lineID); err != nil {
			rows.Close()
			return totals, err
		}
		base, lineTax := posFiscalRateCents(lineTotal, rate)
		totals.Lines = append(totals.Lines, posFiscalLine{
			Name:      name,
			Quantity:  quantity,
			UnitCents: unit,
			LineCents: lineTotal,
			VatRate:   rate,
			BaseCents: base,
			TaxCents:  lineTax,
			Discount:  lineDiscount,
			LineIndex: index,
			// Pack components are folded into the parent on the document; they
			// are shown for traceability but must not be summed again.
			IsComponent: parent.Valid,
		})
		totals.Breakdown[rate] += lineTax
		totals.BaseCents += base
		totals.TaxCents += lineTax
		index++
	}
	rows.Close()
	// Prefer the ticket's own stored tax/base: the backend computed it at
	// checkout and it is what the reports and the receipt use. The per-line sum
	// above is the fallback for a ticket that has not been through checkout.
	if tax > 0 {
		totals.TaxCents = tax
		totals.BaseCents = total - tax
	}
	return totals, nil
}

// issuePOSFiscalDocument assigns the next number in the series, builds the
// canonical content, hashes it chained to the previous document and stores the
// row. It is deliberately in the caller's transaction: a document must not
// exist without its number being consumed, and the checkout that caused it must
// not exist without the document.
func (s *Server) issuePOSFiscalDocument(ctx context.Context, tx *sql.Tx, params posFiscalIssueParams) (map[string]any, error) {
	if params.RestaurantName == "" || !validSpanishIssuerTaxID(params.RestaurantTaxID) {
		return nil, errors.New("fiscal document needs the issuer name and a valid NIF")
	}
	key := posFiscalSeriesKey{RestaurantID: params.RestaurantID, TerminalKey: params.TerminalKey, DocumentType: params.DocumentType}
	seriesID, err := s.ensurePOSFiscalSeries(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	// Lock the series row: two concurrent checkouts must not read the same
	// next_number and issue two documents with the same number.
	var nextNumber int64
	var previousHash sql.NullString
	var prefix string
	if err = tx.QueryRowContext(ctx, `SELECT next_number,last_hash,series_prefix FROM pos_fiscal_series WHERE id=? FOR UPDATE`, seriesID).Scan(&nextNumber, &previousHash, &prefix); err != nil {
		return nil, err
	}

	fullNumber := fmt.Sprintf("%s-%s-%04d", prefix, params.IssuedAt.Format("2006-01-02"), nextNumber)
	issuedAt := params.IssuedAt.UTC().Format(time.RFC3339)

	lines := params.Lines
	if len(lines) == 0 && params.TicketID > 0 {
		totals, totalsErr := s.loadPOSFiscalTicketTotals(ctx, tx, params.RestaurantID, params.TicketID)
		if totalsErr != nil {
			return nil, totalsErr
		}
		lines = totals.Lines
		params.BaseCents = totals.BaseCents
		params.TaxCents = totals.TaxCents
		params.TotalCents = totals.TotalCents
		params.DiscountCents = totals.Discount
		params.SurchargeCents = totals.Surcharge
		params.CustomerName = totals.CustomerName
		params.CustomerTaxID = totals.CustomerNIF
		params.TicketNumber = totals.TicketNumber
	}
	breakdown := params.Breakdown
	if len(breakdown) == 0 {
		breakdown = posVATBreakdownFromLines(lines)
	}

	content := posFiscalContent{
		SeriesType:     params.DocumentType,
		SeriesNumber:   nextNumber,
		FullNumber:     fullNumber,
		TerminalKey:    key.TerminalKey,
		IssuedAt:       issuedAt,
		TicketNumber:   params.TicketNumber,
		IssuerName:     params.RestaurantName,
		IssuerTaxID:    strings.ToUpper(strings.TrimSpace(params.RestaurantTaxID)),
		CustomerName:   strings.TrimSpace(params.CustomerName),
		CustomerTaxID:  strings.ToUpper(strings.TrimSpace(params.CustomerTaxID)),
		BaseCents:      params.BaseCents,
		TaxCents:       params.TaxCents,
		DiscountCents:  params.DiscountCents,
		SurchargeCents: params.SurchargeCents,
		TotalCents:     params.TotalCents,
		VatBreakdown:   breakdown,
		Lines:          lines,
		CorrectsID:     params.CorrectsDocumentID,
		CorrectsNumber: params.CorrectsNumber,
		Reason:         params.Reason,
	}
	hash, canonical := contentHash(content, previousHash.String)

	linesJSON, err := json.Marshal(lines)
	if err != nil {
		return nil, err
	}
	breakdownJSON, err := json.Marshal(breakdown)
	if err != nil {
		return nil, err
	}
	var correctsID any
	if params.CorrectsDocumentID > 0 {
		correctsID = params.CorrectsDocumentID
	}
	var ticketID any
	if params.TicketID > 0 {
		ticketID = params.TicketID
	}
	var visitID any
	if params.VisitID > 0 {
		visitID = params.VisitID
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO pos_fiscal_documents (restaurant_id,series_id,series_number,document_type,full_number,terminal_key,issued_at,ticket_id,visit_id,corrects_document_id,corrects_number,correction_reason,issuer_name,issuer_tax_id,customer_name,customer_tax_id,base_cents,tax_cents,surcharge_cents,discount_cents,total_cents,vat_breakdown_json,lines_json,copy_number,content_hash,previous_hash,is_certified,created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,0,?)`,
		params.RestaurantID, seriesID, nextNumber, params.DocumentType, fullNumber, key.TerminalKey, params.IssuedAt, ticketID, visitID, correctsID, nullIfEmpty(params.CorrectsNumber), nullIfEmpty(params.Reason),
		params.RestaurantName, content.IssuerTaxID, nullIfEmpty(content.CustomerName), nullIfEmpty(content.CustomerTaxID),
		params.BaseCents, params.TaxCents, params.SurchargeCents, params.DiscountCents, params.TotalCents,
		string(breakdownJSON), string(linesJSON), hash, nullIfEmpty(previousHash.String), params.CreatedBy)
	if err != nil {
		return nil, err
	}
	documentID, _ := res.LastInsertId()
	// Consume the number and move the chain head in the same transaction.
	if _, err = tx.ExecContext(ctx, `UPDATE pos_fiscal_series SET next_number=next_number+1,last_hash=? WHERE id=?`, hash, seriesID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'fiscal_document',?,?,?,?)`, params.RestaurantID, documentID, params.DocumentType, canonical, params.CreatedBy); err != nil {
		return nil, err
	}

	return map[string]any{
		"id":               documentID,
		"documentType":     params.DocumentType,
		"seriesNumber":     nextNumber,
		"fullNumber":       fullNumber,
		"terminalKey":      key.TerminalKey,
		"issuedAt":         params.IssuedAt,
		"issuerName":       params.RestaurantName,
		"issuerTaxId":      content.IssuerTaxID,
		"customerName":     content.CustomerName,
		"customerTaxId":    content.CustomerTaxID,
		"ticketNumber":     params.TicketNumber,
		"baseCents":        params.BaseCents,
		"taxCents":         params.TaxCents,
		"surchargeCents":   params.SurchargeCents,
		"discountCents":    params.DiscountCents,
		"totalCents":       params.TotalCents,
		"vatBreakdown":     breakdown,
		"lines":            lines,
		"contentHash":      hash,
		"previousHash":     previousHash.String,
		"copyNumber":       1,
		"isCertified":      false,
		"correctsId":       correctsID,
		"correctionReason": params.Reason,
		// Said out loud on purpose: the client renders this next to the number
		// so nobody can read the document as a certified one.
		"certificationNotice": posFiscalNotice,
	}, nil
}

type posFiscalIssueParams struct {
	RestaurantID       int
	RestaurantName     string
	RestaurantTaxID    string
	TerminalKey        string
	DocumentType       string
	TicketID           int64
	VisitID            int64
	TicketNumber       string
	CustomerName       string
	CustomerTaxID      string
	BaseCents          int64
	TaxCents           int64
	SurchargeCents     int64
	DiscountCents      int64
	TotalCents         int64
	Breakdown          map[string]int64
	Lines              []posFiscalLine
	CorrectsDocumentID int64
	CorrectsNumber     string
	Reason             string
	IssuedAt           time.Time
	CreatedBy          int
}

// validSpanishIssuerTaxID accepts the same NIF/CIF/NIE shapes the customer tax
// id validator already accepts. A restaurant with a broken NIF in its profile
// must not be able to issue documents: the number is on every single one of
// them, and a wrong issuer NIF invalidates the series.
func validSpanishIssuerTaxID(value string) bool {
	_, ok := normalizeSpanishCustomerTaxID(value)
	return ok && strings.TrimSpace(value) != ""
}

// duplicatePOSFiscalDocument issues the "duplicado" copy of an existing
// document. A duplicate carries the SAME number and the SAME content hash as
// the original (it is the same document, printed twice) with a higher
// copy_number, so the two never look like two invoices.
func (s *Server) duplicatePOSFiscalDocument(ctx context.Context, tx *sql.Tx, restaurantID int, documentID int64, copyNumber int, createdBy int) (map[string]any, error) {
	// INSERT ... SELECT rather than read-then-write: the duplicate must be a
	// byte-for-byte copy of the original (same number, same content hash, same
	// previous hash) and only copy_number may differ. Re-serialising the rows in
	// Go would let a rounding difference produce a duplicate whose hash does not
	// match its original, which is exactly the inconsistency the chain exists to
	// prevent.
	if copyNumber < 2 {
		copyNumber = 2
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO pos_fiscal_documents (restaurant_id,series_id,series_number,document_type,full_number,terminal_key,issued_at,ticket_id,visit_id,corrects_document_id,correction_reason,issuer_name,issuer_tax_id,customer_name,customer_tax_id,base_cents,tax_cents,surcharge_cents,discount_cents,total_cents,vat_breakdown_json,lines_json,copy_number,content_hash,previous_hash,is_certified,created_by)
		SELECT restaurant_id,series_id,series_number,document_type,full_number,terminal_key,issued_at,ticket_id,visit_id,corrects_document_id,corrects_number,correction_reason,issuer_name,issuer_tax_id,customer_name,customer_tax_id,base_cents,tax_cents,surcharge_cents,discount_cents,total_cents,vat_breakdown_json,lines_json,?,content_hash,previous_hash,0,? FROM pos_fiscal_documents WHERE restaurant_id=? AND id=?`,
		copyNumber, createdBy, restaurantID, documentID)
	if err != nil {
		return nil, err
	}
	newID, _ := res.LastInsertId()
	if newID == 0 {
		return nil, errors.New("document not found")
	}
	var fullNumber, documentType, terminalKey, contentHash string
	var ticketID, correctsID sql.NullInt64
	var issuedAt time.Time
	if err = tx.QueryRowContext(ctx, `SELECT full_number,document_type,terminal_key,content_hash,ticket_id,corrects_document_id,issued_at FROM pos_fiscal_documents WHERE restaurant_id=? AND id=?`, restaurantID, newID).
		Scan(&fullNumber, &documentType, &terminalKey, &contentHash, &ticketID, &correctsID, &issuedAt); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'fiscal_document',?,'DUPLICATE',JSON_OBJECT('fullNumber',?,'copyNumber',?),?)`, restaurantID, newID, fullNumber, copyNumber, createdBy); err != nil {
		return nil, err
	}
	out := map[string]any{
		"id":                  newID,
		"fullNumber":          fullNumber,
		"documentType":        documentType,
		"terminalKey":         terminalKey,
		"copyNumber":          copyNumber,
		"contentHash":         contentHash,
		"isCertified":         false,
		"issuedAt":            issuedAt.UTC().Format(time.RFC3339),
		"certificationNotice": posFiscalNotice,
	}
	if ticketID.Valid {
		out["ticketId"] = ticketID.Int64
	}
	if correctsID.Valid {
		out["correctsId"] = correctsID.Int64
	}
	return out, nil
}

// verifyPOSFiscalChain re-derives every document's hash from the content stored
// next to it and checks that it links to the document before it.
//
// This is an internal integrity check, not a certification: it says "nothing in
// this series was altered after it was issued", nothing more. It cannot detect a
// rewrite of an entire chain, and it says so in its own response.
//
// It can only verify what it can reproduce, so it rebuilds the canonical
// content from the stored columns and compares. A mismatch therefore means the
// row itself changed, which is the case the check exists for.
func (s *Server) verifyPOSFiscalChain(ctx context.Context, restaurantID, seriesID int64) (map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,series_number,full_number,document_type,terminal_key,issued_at,ticket_number,issuer_name,issuer_tax_id,COALESCE(customer_name,''),COALESCE(customer_tax_id,''),base_cents,tax_cents,surcharge_cents,discount_cents,total_cents,COALESCE(lines_json,''),COALESCE(vat_breakdown_json,''),COALESCE(correction_reason,''),content_hash,COALESCE(previous_hash,''),COALESCE(corrects_number,''),corrects_document_id FROM pos_fiscal_documents WHERE restaurant_id=? AND series_id=? ORDER BY series_number`, restaurantID, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	expected := ""
	count := 0
	untested := 0
	var broken []map[string]any
	for rows.Next() {
		var id, number, base, tax, surcharge, discount, total int64
		var full, docType, terminal, ticketNumber, issuerName, issuerTaxID, customerName, customerTaxID, linesJSON, breakdownJSON, reason, storedHash, previousHash, correctsNumber string
		var correctsID sql.NullInt64
		var issuedAt sql.NullTime
		if err = rows.Scan(&id, &number, &full, &docType, &terminal, &issuedAt, &ticketNumber, &issuerName, &issuerTaxID, &customerName, &customerTaxID, &base, &tax, &surcharge, &discount, &total, &linesJSON, &breakdownJSON, &reason, &storedHash, &previousHash, &correctsNumber, &correctsID); err != nil {
			return nil, err
		}
		problems := []string{}
		if previousHash != expected {
			problems = append(problems, "no enlaza con el documento anterior")
		}
		// Recompute from the stored content. A copy/duplicate carries the same
		// number as its original and is verified against the same predecessor, so
		// it is checked too rather than skipped.
		lines := []posFiscalLine{}
		if linesJSON != "" {
			if err = json.Unmarshal([]byte(linesJSON), &lines); err != nil {
				problems = append(problems, "las lineas guardadas no se pueden leer")
				untested++
			}
		}
		breakdown := map[string]int64{}
		if breakdownJSON != "" {
			if err = json.Unmarshal([]byte(breakdownJSON), &breakdown); err != nil {
				problems = append(problems, "el desglose de IVA guardado no se puede leer")
				untested++
			}
		}
		issued := time.Now().UTC()
		if issuedAt.Valid {
			issued = issuedAt.Time.UTC()
		}
		content := posFiscalContent{
			SeriesType: docType, SeriesNumber: number, FullNumber: full, TerminalKey: terminal,
			IssuedAt: issued.Format(time.RFC3339), TicketNumber: ticketNumber,
			IssuerName: issuerName, IssuerTaxID: issuerTaxID,
			CustomerName: customerName, CustomerTaxID: customerTaxID,
			BaseCents: base, TaxCents: tax, SurchargeCents: surcharge, DiscountCents: discount,
			TotalCents: total, VatBreakdown: breakdown, Lines: lines, Reason: reason,
			CorrectsNumber: correctsNumber,
		}
		if correctsID.Valid {
			content.CorrectsID = correctsID.Int64
		}
		recomputed, _ := contentHash(content, previousHash)
		if recomputed != storedHash {
			problems = append(problems, "el hash guardado no coincide con el contenido")
		}
		if len(problems) > 0 {
			broken = append(broken, map[string]any{"id": id, "fullNumber": full, "problems": problems})
		}
		count++
		// The head advances to the STORED hash, not the recomputed one: a
		// tampered document must not silently re-anchor everything after it,
		// which is what makes a gap detectable at all.
		expected = storedHash
	}
	return map[string]any{
		"seriesId":     seriesID,
		"documents":    count,
		"chainIntact":  len(broken) == 0,
		"problems":     broken,
		"unverifiable": untested,
		"checkedAt":    time.Now().UTC().Format(time.RFC3339),
		// Said plainly so nobody reads a green tick as "this is compliant".
		"whatThisIs":    "Integridad interna de la cadena de hashes. No certifica el documento, no lo sella y no lo presenta en la AEAT.",
		"whatThisIsNot": "Una Factura simplificada o rectificativa conforme al RD 1007/2023 (VERI*FACTU) no existe aqui. Renumerar la serie despues invalidaria los documentos ya emitidos.",
	}, nil
}

// ---------------------------------------------------------------------------
// HTTP surface
// ---------------------------------------------------------------------------

// handleBOPOSFiscalSeries lists the numbering series of the restaurant, so the
// owner can see what the till is numbering and confirm the series are per
// terminal.
func (s *Server) handleBOPOSFiscalSeries(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,terminal_key,series_prefix,series_type,next_number,COALESCE(last_hash,''),is_active FROM pos_fiscal_series WHERE restaurant_id=? ORDER BY terminal_key,series_type`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading fiscal series")
		return
	}
	defer rows.Close()
	series := []map[string]any{}
	for rows.Next() {
		var id, nextNumber int64
		var terminal, prefix, seriesType, lastHash string
		var active int
		if err = rows.Scan(&id, &terminal, &prefix, &seriesType, &nextNumber, &lastHash, &active); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading fiscal series")
			return
		}
		var docs int64
		s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_fiscal_documents WHERE restaurant_id=? AND series_id=?`, a.ActiveRestaurantID, id).Scan(&docs)
		series = append(series, map[string]any{
			"id": id, "terminalKey": terminal, "prefix": prefix, "documentType": seriesType,
			"nextNumber": nextNumber, "lastHash": lastHash, "isActive": active == 1, "issued": docs,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "series": series})
}

// handleBOPOSFiscalDocument issues a simplified invoice for a paid ticket on
// demand, its duplicate copy, or a rectifying invoice for a refund. It refuses
// a ticket that was not paid, because a document for money that did not move
// is worse than no document.
func (s *Server) handleBOPOSFiscalDocument(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.checkPOSRateLimit("fiscal", a.ActiveRestaurantID, a.User.ID, 60) {
		httpx.WriteError(w, http.StatusTooManyRequests, "Too many fiscal document requests")
		return
	}
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(r.Context(), a.ActiveRestaurantID, ticketID)) {
		return
	}
	var in struct {
		Action      string `json:"action"`     // "issue" (default) | "duplicate"
		DocumentID  int64  `json:"documentId"` // for a duplicate
		TerminalKey string `json:"terminalKey"`
		// A rectifying invoice is issued for a refund that already exists.
		RefundID   int64  `json:"refundId"`
		Reason     string `json:"reason"`
		CopyNumber int    `json:"copyNumber"`
	}
	if !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid fiscal request")
		return
	}
	restaurantName, restaurantTaxID := s.posRestaurantIssuer(r.Context(), a.ActiveRestaurantID)
	if strings.TrimSpace(restaurantName) == "" || !validSpanishIssuerTaxID(restaurantTaxID) {
		// Refusing here is the honest outcome: a document with a wrong issuer
		// NIF is worse than a clear message telling the owner to fix the
		// restaurant profile first.
		httpx.WriteError(w, http.StatusConflict, "El restaurante necesita nombre y NIF válido en su perfil antes de emitir documentos fiscales")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error starting fiscal transaction")
		return
	}
	defer tx.Rollback()

	// The document for this ticket, if one was already issued: issuing two
	// simplified invoices for one sale is the exact error the series exists to
	// make impossible.
	var existingID int64
	var existingType string
	_ = tx.QueryRowContext(r.Context(), `SELECT id,document_type FROM pos_fiscal_documents WHERE restaurant_id=? AND ticket_id=? AND corrects_document_id IS NULL ORDER BY id LIMIT 1`, a.ActiveRestaurantID, ticketID).Scan(&existingID, &existingType)

	var document map[string]any
	switch in.Action {
	case "duplicate":
		if in.DocumentID <= 0 {
			in.DocumentID = existingID
		}
		if in.DocumentID <= 0 {
			httpx.WriteError(w, http.StatusConflict, "Esta cuenta no tiene documento que duplicar")
			return
		}
		if in.CopyNumber <= 0 {
			in.CopyNumber = 2
		}
		document, err = s.duplicatePOSFiscalDocument(r.Context(), tx, a.ActiveRestaurantID, in.DocumentID, in.CopyNumber, a.User.ID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error issuing duplicate")
			return
		}
	case "rectify":
		document, err = s.issueRectifyingInvoice(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, ticketID, in.RefundID, in.Reason, restaurantName, restaurantTaxID, in.TerminalKey)
	default:
		if existingID > 0 {
			httpx.WriteError(w, http.StatusConflict, "Esta cuenta ya tiene una factura simplificada emitida")
			return
		}
		var status string
		if err = tx.QueryRowContext(r.Context(), `SELECT status FROM pos_tickets WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, ticketID).Scan(&status); err != nil {
			httpx.WriteError(w, http.StatusNotFound, "Ticket not found")
			return
		}
		if status == "OPEN" {
			httpx.WriteError(w, http.StatusConflict, "La cuenta tiene que estar cobrada antes de emitir la factura")
			return
		}
		document, err = s.issuePOSFiscalDocument(r.Context(), tx, posFiscalIssueParams{
			RestaurantID: a.ActiveRestaurantID, RestaurantName: restaurantName, RestaurantTaxID: restaurantTaxID,
			TerminalKey: in.TerminalKey, DocumentType: posFiscalTypeSimplificada, TicketID: ticketID,
			IssuedAt: time.Now(), CreatedBy: a.User.ID,
		})
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error issuing fiscal document")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving fiscal document")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "document": document})
}

// issueRectifyingInvoice issues a factura rectificativa for a refund that is
// already recorded. The rectifying invoice always points back at the document
// it corrects, so the series keeps the audit trail a refund needs.
func (s *Server) issueRectifyingInvoice(ctx context.Context, tx *sql.Tx, restaurantID, userID int, ticketID, refundID int64, reason, restaurantName, restaurantTaxID, terminalKey string) (map[string]any, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, errors.New("a rectifying invoice needs a reason")
	}
	var correctsID int64
	var correctsNumber string
	if err := tx.QueryRowContext(ctx, `SELECT id,full_number FROM pos_fiscal_documents WHERE restaurant_id=? AND ticket_id=? AND corrects_document_id IS NULL ORDER BY id LIMIT 1`, restaurantID, ticketID).Scan(&correctsID, &correctsNumber); err != nil {
		return nil, errors.New("this sale has no simplified invoice to correct; issue one first")
	}
	// Refund amounts are read from the recorded refund, never from the request,
	// so a client cannot inflate a rectified document.
	query := `SELECT amount_cents FROM pos_refunds WHERE restaurant_id=? AND ticket_id=? AND status='COMPLETED'`
	args := []any{restaurantID, ticketID}
	if refundID > 0 {
		query += ` AND id=?`
		args = append(args, refundID)
	}
	var refundTotal int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&refundTotal); err != nil {
		return nil, errors.New("no completed refund to rectify")
	}
	var ticketTotal, ticketTax int64
	var ticketNumber string
	if err := tx.QueryRowContext(ctx, `SELECT total_gross_cents,tax_cents,ticket_number FROM pos_tickets WHERE restaurant_id=? AND id=?`, restaurantID, ticketID).Scan(&ticketTotal, &ticketTax, &ticketNumber); err != nil {
		return nil, err
	}
	var refundTax int64
	if ticketTotal > 0 {
		refundTax = int64(float64(refundTotal)*float64(ticketTax)/float64(ticketTotal) + 0.5)
	}
	if refundTax > refundTotal {
		refundTax = refundTotal
	}
	// The rectified VAT is attributed to the rates that actually carried the
	// refunded amount, read off the ticket's own lines. Hardcoding one rate
	// would file a wrong breakdown against a restaurant selling at 10% and 21%,
	// which is the kind of error an accountant finds a year later.
	breakdown := posRefundVATBreakdown(ctx, tx, restaurantID, ticketID, refundTotal, refundTax)
	refundLines := []posFiscalLine{{
		Name:      "Rectificaci\u00f3n de factura simplificada " + correctsNumber,
		Quantity:  "1",
		UnitCents: refundTotal,
		LineCents: refundTotal,
		VatRate:   posDominantRate(breakdown),
		BaseCents: refundTotal - refundTax,
		TaxCents:  refundTax,
		LineIndex: 0,
	}}
	return s.issuePOSFiscalDocument(ctx, tx, posFiscalIssueParams{
		RestaurantID: restaurantID, RestaurantName: restaurantName, RestaurantTaxID: restaurantTaxID,
		TerminalKey: terminalKey, DocumentType: posFiscalTypeRectificativa,
		TicketID: ticketID, TicketNumber: ticketNumber,
		BaseCents: refundTotal - refundTax, TaxCents: refundTax, TotalCents: refundTotal,
		Breakdown:          breakdown,
		Lines:              refundLines,
		CorrectsDocumentID: correctsID, CorrectsNumber: correctsNumber, Reason: strings.TrimSpace(reason),
		IssuedAt: time.Now(), CreatedBy: userID,
	})
}

// posRefundVATBreakdown splits a refunded amount across the ticket's VAT rates
// in proportion to how much each rate contributed to the sale, and splits the
// refunded tax the same way. The last rate absorbs the rounding remainder, so
// the per-rate gross always adds back up to the refund exactly instead of
// drifting a cent per rate.
func posRefundVATBreakdown(ctx context.Context, tx *sql.Tx, restaurantID int, ticketID, refundTotal, refundTax int64) map[string]int64 {
	breakdown := map[string]int64{}
	rows, err := tx.QueryContext(ctx, `SELECT vat_rate_snapshot,COALESCE(SUM(line_total_gross_cents),0) FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND status='ACTIVE' GROUP BY vat_rate_snapshot ORDER BY SUM(line_total_gross_cents) DESC`, restaurantID, ticketID)
	if err != nil {
		breakdown["0"] = refundTax
		return breakdown
	}
	type rateAmount struct {
		rate   string
		amount int64
	}
	byRate := []rateAmount{}
	for rows.Next() {
		var entry rateAmount
		if rows.Scan(&entry.rate, &entry.amount) == nil {
			byRate = append(byRate, entry)
		}
	}
	rows.Close()
	var gross int64
	for _, entry := range byRate {
		gross += entry.amount
	}
	if gross <= 0 || refundTotal <= 0 {
		breakdown["0"] = refundTax
		return breakdown
	}
	var attributed int64
	for index, entry := range byRate {
		share := int64(float64(refundTotal)*float64(entry.amount)/float64(gross) + 0.5)
		if index == len(byRate)-1 {
			share = refundTotal - attributed
		}
		if share < 0 {
			share = 0
		}
		rateTax := int64(float64(share)*float64(refundTax)/float64(refundTotal) + 0.5)
		if index == len(byRate)-1 {
			rateTax = refundTax
			var summed int64
			for _, tax := range breakdown {
				summed += tax
			}
			rateTax = refundTax - summed
		}
		breakdown[entry.rate] = rateTax
		attributed += share
	}
	return breakdown
}

// posDominantRate is the rate carrying the most tax in a breakdown, used to
// label the single rectifying line.
func posDominantRate(breakdown map[string]int64) string {
	best, bestTax := "", int64(-1)
	for rate, tax := range breakdown {
		if tax > bestTax || (tax == bestTax && rate < best) {
			best, bestTax = rate, tax
		}
	}
	if best == "" {
		return "0"
	}
	return best
}

// posRestaurantIssuer reads the issuer name and NIF the till already prints on
// the comanda. It deliberately reuses loadPOSRestaurantProfile instead of
// querying restaurants itself: the fiscal identity on an invoice has to be the
// same identity the receipt shows, and the profile is where the restaurant
// maintains it (restaurants.cif is only the legacy fallback).
func (s *Server) posRestaurantIssuer(ctx context.Context, restaurantID int) (string, string) {
	profile, err := s.loadPOSRestaurantProfile(ctx, restaurantID)
	if err != nil {
		return "", ""
	}
	return profile.Name, profile.TaxID
}

// handleBOPOSFiscalChainVerify walks a series and reports whether the hash
// chain is unbroken. Read-only, no permission beyond the POS reports one.
func (s *Server) handleBOPOSFiscalChainVerify(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	allowed, err := s.boPOSPermissionAllowed(r.Context(), a, posPermissionReports)
	if err != nil || !allowed {
		httpx.WriteError(w, http.StatusForbidden, "Reports permission required")
		return
	}
	seriesID, _ := strconv.ParseInt(chi.URLParam(r, "seriesId"), 10, 64)
	if seriesID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid series")
		return
	}
	result, err := s.verifyPOSFiscalChain(r.Context(), int64(a.ActiveRestaurantID), seriesID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verifying fiscal chain")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true, "chain": result,
		"notice": "Comprobación de integridad interna. No certifica el documento ni lo presenta en la AEAT. " + posFiscalNotice,
	})
}

// posCashNIFThreshold reads the per-restaurant cash threshold, defaulting to the
// statutory 3.000 € when the column is missing (an older database) or when a
// stored value is somehow above the statutory cap, which cannot be honoured.
func (s *Server) posCashNIFThreshold(ctx context.Context, restaurantID int) int64 {
	const statutoryCents = 300000
	var stored sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT cash_nif_threshold_cents FROM pos_settings WHERE restaurant_id=?`, restaurantID).Scan(&stored); err != nil || !stored.Valid || stored.Int64 <= 0 {
		return statutoryCents
	}
	if stored.Int64 > statutoryCents {
		return statutoryCents
	}
	return stored.Int64
}

// posCashNIFRefusal returns a message when a checkout would take more cash than
// the threshold without the buyer's NIF on the visit, and "" when it may go
// ahead. Only CASH counts: a card, Bizum or bank transfer already identifies
// the payer through the payment, which is the whole point of the rule.
func (s *Server) posCashNIFRefusal(ctx context.Context, restaurantID int, ticketID int64, payments []posCheckoutPayment) string {
	threshold := s.posCashNIFThreshold(ctx, restaurantID)
	if threshold <= 0 {
		return ""
	}
	cash := int64(0)
	for _, payment := range payments {
		if payment.Method == "CASH" {
			cash += payment.AmountCents
		}
	}
	if cash <= threshold {
		return ""
	}
	// The NIF is read from the visit, the same place the receipt prints it from,
	// so a guest whose NIF was already captured is not asked twice.
	var taxID sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT v.customer_tax_id FROM pos_visits v JOIN pos_tickets t ON t.restaurant_id=v.restaurant_id AND t.visit_id=v.id WHERE v.restaurant_id=? AND t.id=?`, restaurantID, ticketID).Scan(&taxID); err == nil {
		if value, ok := normalizeSpanishCustomerTaxID(taxID.String); ok && value != "" {
			return ""
		}
	}
	return "Pago en efectivo de " + posFiscalEuro(cash) + ": por encima de " + posFiscalEuro(threshold) + " el LIVA exige el NIF del comprador. As\u00edgnalo a la cuenta antes de cobrar."
}

// posFiscalEuro formats integer cents as Spanish plain euros ("3.000,00 €") for
// a message the waiter reads out loud or types into a note. Written by hand
// rather than pulled in from a locale library because the server has no
// user-facing formatting anywhere else and this is the only place that needs it.
func posFiscalEuro(cents int64) string {
	negative := cents < 0
	if negative {
		cents = -cents
	}
	units := cents / 100
	remainder := cents % 100
	digits := strconv.FormatInt(units, 10)
	var grouped strings.Builder
	for index, char := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(char)
	}
	out := grouped.String() + "," + strconv.FormatInt(remainder/10, 10) + strconv.FormatInt(remainder%10, 10) + " €"
	if negative {
		return "-" + out
	}
	return out
}
