package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: invoice_numbering_v1
//
// Every invoice is identified by a number that is printed in the header of
// its PDF: the custom one the operator typed, or the automatic one of the
// restaurant's series for the current year. The series lives in
// restaurant_invoice_settings (one row per restaurant and year) and is
// configured from the BackOffice ("Numeracion de facturas").

const (
	defaultInvoiceNumberPrefix       = "F-"
	defaultInvoiceNumberFormat       = "F-{YYYY}-{0001}"
	defaultInvoiceNumberPaddingZeros = 4
	defaultInvoiceStartingNumber     = 1
	defaultInvoicePdfTemplate        = "basic"
	defaultInvoicePaymentTerms       = 30
)

// invoiceNumberFormat is the operator-facing shape of the series.
type invoiceNumberFormat struct {
	Prefix         string `json:"prefix"`
	Suffix         string `json:"suffix"`
	Format         string `json:"format"`
	StartingNumber int    `json:"startingNumber"`
	PaddingZeros   int    `json:"paddingZeros"`
}

// invoiceNumberSettings is the payload of the invoice settings endpoints and
// the row of restaurant_invoice_settings.
type invoiceNumberSettings struct {
	Format              invoiceNumberFormat `json:"format"`
	NextNumber          int                 `json:"nextNumber"`
	DefaultPdfTemplate  string              `json:"defaultPdfTemplate"`
	DefaultPaymentTerms int                 `json:"default_payment_terms"`
}

// defaultInvoiceNumberSettings returns the settings a restaurant starts with.
func defaultInvoiceNumberSettings() invoiceNumberSettings {
	return invoiceNumberSettings{
		Format: invoiceNumberFormat{
			Prefix:         defaultInvoiceNumberPrefix,
			Format:         defaultInvoiceNumberFormat,
			StartingNumber: defaultInvoiceStartingNumber,
			PaddingZeros:   defaultInvoiceNumberPaddingZeros,
		},
		NextNumber:          defaultInvoiceStartingNumber,
		DefaultPdfTemplate:  defaultInvoicePdfTemplate,
		DefaultPaymentTerms: defaultInvoicePaymentTerms,
	}
}

// normalizeInvoiceNumberSettings clamps what the operator typed to sane
// values, so a broken setting can never break the numbering.
func normalizeInvoiceNumberSettings(in invoiceNumberSettings) invoiceNumberSettings {
	out := defaultInvoiceNumberSettings()
	out.Format.Prefix = strings.TrimSpace(in.Format.Prefix)
	out.Format.Suffix = strings.TrimSpace(in.Format.Suffix)
	if f := strings.TrimSpace(in.Format.Format); f != "" {
		out.Format.Format = f
	}
	if in.Format.StartingNumber > 0 {
		out.Format.StartingNumber = in.Format.StartingNumber
	}
	if in.Format.PaddingZeros > 0 && in.Format.PaddingZeros <= 10 {
		out.Format.PaddingZeros = in.Format.PaddingZeros
	}
	if in.NextNumber > 0 {
		out.NextNumber = in.NextNumber
	}
	if t := strings.TrimSpace(in.DefaultPdfTemplate); validPdfTemplates[t] {
		out.DefaultPdfTemplate = t
	}
	if in.DefaultPaymentTerms > 0 {
		out.DefaultPaymentTerms = in.DefaultPaymentTerms
	}
	// The prefix only shows up when the format uses its token.
	if out.Format.Prefix == "" && strings.Contains(out.Format.Format, "{prefix}") {
		out.Format.Prefix = defaultInvoiceNumberPrefix
	}
	return out
}

// formatInvoiceNumber renders the number of the series with the configured
// tokens: {YYYY} {YY} {0001} {000} {00} {0} {N} {prefix} {suffix}.
func formatInvoiceNumber(f invoiceNumberFormat, seq, year int) string {
	padded := strconv.Itoa(seq)
	if f.PaddingZeros > len(padded) {
		padded = strings.Repeat("0", f.PaddingZeros-len(padded)) + padded
	}
	format := strings.TrimSpace(f.Format)
	if format == "" {
		format = defaultInvoiceNumberFormat
	}
	return strings.NewReplacer(
		"{YYYY}", strconv.Itoa(year),
		"{YY}", fmt.Sprintf("%02d", year%100),
		"{0001}", padded,
		"{000}", lastChars(padded, 3),
		"{00}", lastChars(padded, 2),
		"{0}", lastChars(padded, 1),
		"{N}", strconv.Itoa(seq),
		"{prefix}", f.Prefix,
		"{suffix}", f.Suffix,
	).Replace(format)
}

// lastChars returns the last n characters of s (all of it when shorter).
func lastChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// invoiceNumberSequence extracts the trailing sequence of an invoice number
// ("F-2026-0049" -> 49).
func invoiceNumberSequence(number string) int {
	i := len(number)
	for i > 0 && number[i-1] >= '0' && number[i-1] <= '9' {
		i--
	}
	seq, _ := strconv.Atoi(number[i:])
	return seq
}

// invoiceNumberFormatHasYear reports whether the format prints the year, and
// therefore whether the series restarts every year.
func invoiceNumberFormatHasYear(f invoiceNumberFormat) bool {
	return strings.Contains(f.Format, "{YYYY}") || strings.Contains(f.Format, "{YY}")
}

// maxIssuedInvoiceSequence returns the highest sequence already used by the
// restaurant, so a new series never repeats a number that was issued before.
func maxIssuedInvoiceSequence(ctx context.Context, tx *sql.Tx, restaurantID, year int, byYear bool) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT invoice_number FROM invoices
		WHERE restaurant_id = ? AND invoice_number IS NOT NULL AND invoice_number <> ''
	`, restaurantID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	yearToken := strconv.Itoa(year)
	max := 0
	for rows.Next() {
		var number string
		if err := rows.Scan(&number); err != nil {
			return 0, err
		}
		if byYear && !strings.Contains(number, yearToken) {
			continue
		}
		if seq := invoiceNumberSequence(number); seq > max {
			max = seq
		}
	}
	return max, rows.Err()
}

// ensureInvoiceNumberSettingsRow returns the numbering settings of the
// restaurant for the given year, creating the row when it does not exist yet.
// The row is locked for the rest of the transaction, so the sequence is
// reserved atomically with the invoice that uses it.
func (s *Server) ensureInvoiceNumberSettingsRow(ctx context.Context, tx *sql.Tx, restaurantID, year int) (invoiceNumberSettings, error) {
	settings, err := scanInvoiceNumberSettings(tx.QueryRowContext(ctx, `
		SELECT prefix, suffix, format, starting_number, padding_zeros, next_number, default_pdf_template, default_payment_terms
		FROM restaurant_invoice_settings
		WHERE restaurant_id = ? AND year = ?
		FOR UPDATE
	`, restaurantID, year))
	if err == nil {
		return settings, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return settings, err
	}

	// First use of the series for this year: inherit the format the operator
	// configured and continue after the numbers already issued.
	base, err := s.loadLatestInvoiceNumberSettings(ctx, tx, restaurantID)
	if err != nil {
		return base, err
	}
	maxSeq, err := maxIssuedInvoiceSequence(ctx, tx, restaurantID, year, invoiceNumberFormatHasYear(base.Format))
	if err != nil {
		return base, err
	}
	next := base.Format.StartingNumber
	if maxSeq+1 > next {
		next = maxSeq + 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO restaurant_invoice_settings
			(restaurant_id, year, prefix, suffix, format, starting_number, padding_zeros, next_number, default_pdf_template, default_payment_terms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE next_number = GREATEST(next_number, ?)
	`, restaurantID, year, base.Format.Prefix, base.Format.Suffix, base.Format.Format,
		base.Format.StartingNumber, base.Format.PaddingZeros, next, base.DefaultPdfTemplate, base.DefaultPaymentTerms,
		next); err != nil {
		return base, err
	}
	return scanInvoiceNumberSettings(tx.QueryRowContext(ctx, `
		SELECT prefix, suffix, format, starting_number, padding_zeros, next_number, default_pdf_template, default_payment_terms
		FROM restaurant_invoice_settings
		WHERE restaurant_id = ? AND year = ?
		FOR UPDATE
	`, restaurantID, year))
}

// loadLatestInvoiceNumberSettings returns the settings of the most recent
// series of the restaurant, so a new year inherits the format the operator
// configured instead of falling back to the defaults.
func (s *Server) loadLatestInvoiceNumberSettings(ctx context.Context, tx *sql.Tx, restaurantID int) (invoiceNumberSettings, error) {
	settings, err := scanInvoiceNumberSettings(tx.QueryRowContext(ctx, `
		SELECT prefix, suffix, format, starting_number, padding_zeros, next_number, default_pdf_template, default_payment_terms
		FROM restaurant_invoice_settings
		WHERE restaurant_id = ?
		ORDER BY year DESC
		LIMIT 1
	`, restaurantID))
	if errors.Is(err, sql.ErrNoRows) {
		return defaultInvoiceNumberSettings(), nil
	}
	return settings, err
}

// scanInvoiceNumberSettings reads one restaurant_invoice_settings row.
func scanInvoiceNumberSettings(row *sql.Row) (invoiceNumberSettings, error) {
	var (
		prefix, suffix, format, pdfTemplate            sql.NullString
		startingNumber, paddingZeros, nextNumber, terms sql.NullInt64
	)
	if err := row.Scan(&prefix, &suffix, &format, &startingNumber, &paddingZeros, &nextNumber, &pdfTemplate, &terms); err != nil {
		return invoiceNumberSettings{}, err
	}
	out := defaultInvoiceNumberSettings()
	out.Format.Prefix = strings.TrimSpace(prefix.String)
	out.Format.Suffix = strings.TrimSpace(suffix.String)
	out.Format.Format = strings.TrimSpace(format.String)
	if startingNumber.Int64 > 0 {
		out.Format.StartingNumber = int(startingNumber.Int64)
	}
	if paddingZeros.Int64 > 0 {
		out.Format.PaddingZeros = int(paddingZeros.Int64)
	}
	if nextNumber.Int64 > 0 {
		out.NextNumber = int(nextNumber.Int64)
	}
	if t := strings.TrimSpace(pdfTemplate.String); validPdfTemplates[t] {
		out.DefaultPdfTemplate = t
	}
	if terms.Int64 > 0 {
		out.DefaultPaymentTerms = int(terms.Int64)
	}
	return out, nil
}

// reserveInvoiceNumber takes the next number of the restaurant's series. It
// runs inside the transaction that stores the invoice, so the number is
// reserved and the invoice land together.
func (s *Server) reserveInvoiceNumber(ctx context.Context, tx *sql.Tx, restaurantID int) (string, error) {
	year := time.Now().Year()
	settings, err := s.ensureInvoiceNumberSettingsRow(ctx, tx, restaurantID, year)
	if err != nil {
		return "", err
	}
	number := formatInvoiceNumber(settings.Format, settings.NextNumber, year)
	if _, err := tx.ExecContext(ctx, `
		UPDATE restaurant_invoice_settings SET next_number = ? WHERE restaurant_id = ? AND year = ?
	`, settings.NextNumber+1, restaurantID, year); err != nil {
		return "", err
	}
	return number, nil
}

// resolveInvoiceNumber returns the number the invoice must carry: the custom
// one when the operator provided it, the stored one when a save does not
// mention it (a save never wipes the number), and the automatic one when the
// invoice has none yet.
func (s *Server) resolveInvoiceNumber(ctx context.Context, tx *sql.Tx, restaurantID int, requested *string, stored string) (string, error) {
	if custom := strings.TrimSpace(deref(requested)); custom != "" {
		return custom, nil
	}
	if kept := strings.TrimSpace(stored); kept != "" {
		return kept, nil
	}
	return s.reserveInvoiceNumber(ctx, tx, restaurantID)
}

// loadInvoiceNumberSettings returns the numbering settings of the restaurant
// for the given year, creating the row when it does not exist yet.
func (s *Server) loadInvoiceNumberSettings(ctx context.Context, restaurantID, year int) (invoiceNumberSettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return invoiceNumberSettings{}, err
	}
	defer tx.Rollback()
	settings, err := s.ensureInvoiceNumberSettingsRow(ctx, tx, restaurantID, year)
	if err != nil {
		return settings, err
	}
	if err := tx.Commit(); err != nil {
		return settings, err
	}
	return settings, nil
}

// saveInvoiceNumberSettings stores the numbering settings of the restaurant
// for the given year.
func (s *Server) saveInvoiceNumberSettings(ctx context.Context, restaurantID, year int, input invoiceNumberSettings) (invoiceNumberSettings, error) {
	settings := normalizeInvoiceNumberSettings(input)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_invoice_settings
			(restaurant_id, year, prefix, suffix, format, starting_number, padding_zeros, next_number, default_pdf_template, default_payment_terms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			prefix = ?, suffix = ?, format = ?, starting_number = ?, padding_zeros = ?,
			next_number = ?, default_pdf_template = ?, default_payment_terms = ?
	`, restaurantID, year, settings.Format.Prefix, settings.Format.Suffix, settings.Format.Format,
		settings.Format.StartingNumber, settings.Format.PaddingZeros, settings.NextNumber,
		settings.DefaultPdfTemplate, settings.DefaultPaymentTerms,
		settings.Format.Prefix, settings.Format.Suffix, settings.Format.Format,
		settings.Format.StartingNumber, settings.Format.PaddingZeros, settings.NextNumber,
		settings.DefaultPdfTemplate, settings.DefaultPaymentTerms); err != nil {
		return settings, err
	}
	return settings, nil
}

// handleBOInvoiceSettingsGet returns the numbering settings of the restaurant
// for the current year.
func (s *Server) handleBOInvoiceSettingsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	settings, err := s.loadInvoiceNumberSettings(r.Context(), a.ActiveRestaurantID, time.Now().Year())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando la numeracion de facturas: "+err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"settings": settings,
	})
}

// handleBOInvoiceSettingsPost stores the numbering settings of the restaurant
// for the current year.
func (s *Server) handleBOInvoiceSettingsPost(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var input invoiceNumberSettings
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	settings, err := s.saveInvoiceNumberSettings(r.Context(), a.ActiveRestaurantID, time.Now().Year(), input)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando la numeracion de facturas: "+err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"settings": settings,
	})
}
