package api

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	qrcode "github.com/skip2/go-qrcode"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Special-date booking QR. The PNG lives on the restaurant BunnyCDN and its
// URL is stored in bookings.qr_url; email, WhatsApp and the payment receipt
// all reference it. Scanning opens the backoffice booking page
// /app/reservas/especial/reserva?restaurant_id=&booking_id=&date=.
// Coordination id: special_booking_qr_v1
// =============================================================================

const bookingQRKey = "__booking_qr"

type bookingQR struct {
	URL string
	PNG []byte
}

// specialBookingQRTarget is the backoffice URL encoded inside the QR.
func (s *Server) specialBookingQRTarget(restaurantID int, bookingID int64, date string) string {
	q := url.Values{}
	q.Set("restaurant_id", strconv.Itoa(restaurantID))
	q.Set("booking_id", strconv.FormatInt(bookingID, 10))
	q.Set("date", date)
	return s.backofficePublicBaseURL() + "/app/reservas/especial/reserva?" + q.Encode()
}

// ensureBookingQR renders the QR, uploads it (when Bunny is configured) and
// stores its CDN URL on the booking. The PNG is returned for PDF embedding.
func (s *Server) ensureBookingQR(ctx context.Context, restaurantID int, bookingID int64, date string) *bookingQR {
	png, err := qrcode.Encode(s.specialBookingQRTarget(restaurantID, bookingID, date), qrcode.Medium, 512)
	if err != nil {
		log.Printf("[special_booking_qr_v1] encode failed booking=%d: %v", bookingID, err)
		return nil
	}
	out := &bookingQR{PNG: png}
	if !s.bunnyConfigured(ctx, restaurantID) {
		return out
	}
	objectPath := fmt.Sprintf("%d/qr/bookings/%d.png", restaurantID, bookingID)
	if err := s.bunnyPut(ctx, restaurantID, objectPath, png, "image/png"); err != nil {
		log.Printf("[special_booking_qr_v1] upload failed booking=%d: %v", bookingID, err)
		return out
	}
	out.URL = s.bunnyPullURL(ctx, restaurantID, objectPath)
	_, _ = s.db.ExecContext(ctx, `UPDATE bookings SET qr_url = ? WHERE id = ? AND restaurant_id = ?`, out.URL, bookingID, restaurantID)
	log.Printf("[special_booking_qr_v1] stored booking=%d url=%s", bookingID, out.URL)
	return out
}

// setBookingReceiptURL records the uploaded receipt PDF on the booking row.
func (s *Server) setBookingReceiptURL(ctx context.Context, restaurantID int, bookingID int64, receiptURL string) {
	if receiptURL == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE bookings SET receipt_url = ? WHERE id = ? AND restaurant_id = ?`, receiptURL, bookingID, restaurantID)
}

// handleBOBookingQR returns (creating on demand) the QR + receipt URLs of a
// special booking. Backfills bookings created before special_booking_qr_v1.
// GET /api/admin/bookings/{id}/qr
func (s *Server) handleBOBookingQR(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid booking id"})
		return
	}
	rid := a.ActiveRestaurantID
	var date string
	var qrURL, receiptURL sql.NullString
	var isSpecial int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d'), qr_url, receipt_url, COALESCE(is_special_booking, 0)
		FROM bookings WHERE id = ? AND restaurant_id = ? LIMIT 1`, id, rid).Scan(&date, &qrURL, &receiptURL, &isSpecial); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Booking not found"})
		return
	}
	if isSpecial == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "La reserva no es de fecha especial"})
		return
	}
	if strings.TrimSpace(qrURL.String) == "" {
		if qr := s.ensureBookingQR(r.Context(), rid, id, date); qr != nil {
			qrURL.String = qr.URL
		}
	}
	if strings.TrimSpace(receiptURL.String) == "" {
		var fromCheckout sql.NullString
		if s.db.QueryRowContext(r.Context(), `SELECT receipt_url FROM booking_checkouts WHERE booking_id = ? AND restaurant_id = ? AND receipt_url IS NOT NULL ORDER BY id DESC LIMIT 1`, id, rid).Scan(&fromCheckout) == nil && fromCheckout.String != "" {
			receiptURL.String = fromCheckout.String
			s.setBookingReceiptURL(r.Context(), rid, id, fromCheckout.String)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"booking_id":  id,
		"qr_url":      qrURL.String,
		"receipt_url": receiptURL.String,
		"target_url":  s.specialBookingQRTarget(rid, id, date),
	})
}

// broadcastBookingChanged tells open backoffice pages (booking QR detail) that
// a booking changed so they refetch it. Coordination id: special_booking_qr_v1
func (s *Server) broadcastBookingChanged(restaurantID int, bookingID int64, action string) {
	s.broadcastBOGlobal(restaurantID, "booking", map[string]any{
		"type":          action, // booking_created | booking_updated | booking_cancelled
		"restaurant_id": restaurantID,
		"booking_id":    bookingID,
	})
}

// handleBOBookingReceiptPDF streams the booking's receipt PDF same-origin.
// BunnyCDN answers PDFs without Access-Control-Allow-Origin, so pdf.js in the
// backoffice cannot fetch the CDN URL directly. Tenant-scoped: only the
// receipt stored on this restaurant's booking row is fetched, and only from the
// restaurant's own pull zone (no open proxy).
// GET /api/admin/bookings/{id}/receipt.pdf
// Coordination id: special_booking_receipt_proxy_v1
func (s *Server) handleBOBookingReceiptPDF(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid booking id")
		return
	}
	rid := a.ActiveRestaurantID
	var receiptURL sql.NullString
	if err := s.db.QueryRowContext(r.Context(), `SELECT receipt_url FROM bookings WHERE id = ? AND restaurant_id = ? LIMIT 1`, id, rid).Scan(&receiptURL); err != nil || strings.TrimSpace(receiptURL.String) == "" {
		httpx.WriteError(w, http.StatusNotFound, "Comprobante no disponible")
		return
	}
	pullBase := strings.TrimRight(s.bunnyCreds(r.Context(), rid).PullBaseURL, "/")
	src := strings.TrimSpace(receiptURL.String)
	if pullBase == "" || !strings.HasPrefix(src, pullBase+"/") {
		log.Printf("[special_booking_receipt_proxy_v1] refused non-tenant url booking=%d", id)
		httpx.WriteError(w, http.StatusNotFound, "Comprobante no disponible")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, src, nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error preparando comprobante")
		return
	}
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "No se pudo obtener el comprobante")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		httpx.WriteError(w, http.StatusBadGateway, "No se pudo obtener el comprobante")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="comprobante-reserva-%d.pdf"`, id))
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.Copy(w, io.LimitReader(res.Body, 20<<20))
}
