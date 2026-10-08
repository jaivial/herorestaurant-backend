package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// =============================================================================
// Documents attached to a booking (menus, event dossiers, PDFs, sheets...).
// Coordination id: booking_documents_v1
//
// The upload flow is socket-only (backoffice_booking_documents_ws.go): the
// client sends the bytes over the WebSocket and gets the answer over the same
// socket, so a 25 MB file never has to survive a second HTTP hop.
//
// Drafts: /app/reservas/anadir attaches files BEFORE the booking row exists.
// An upload therefore lands as a DRAFT (booking_id NULL, owned by the uploader)
// and POST /api/admin/bookings claims it with `document_ids`. Uploading to an
// existing booking is the same call with `booking_id` set.
//
// Storage: the restaurant's own BunnyCDN zone (bunnyPut/bunnyPullURL/
// bunnyDelete, the same trio the receipt PDF and the booking QR use), because a
// booking document is a guest-facing artifact the WhatsApp gateway has to be
// able to fetch by URL. The object key carries a uuid, so the URL is
// unguessable. Images are stored as webp of at most bookingDocumentMaxImageBytes;
// every other file is stored byte for byte.
// =============================================================================

const (
	// bookingDocumentMaxUploadBytes is the single documented cap for ANY upload
	// (25 MB), whatever its type. Bigger files are rejected with a clear error.
	bookingDocumentMaxUploadBytes = 25 * 1024 * 1024
	// bookingDocumentMaxImageBytes is the STORED budget for an image. An image
	// above it (or already webp above it) is re-encoded down to it.
	bookingDocumentMaxImageBytes = 2 * 1024 * 1024
	// bookingDocumentMaxImageInputBytes is the cap on the INCOMING image, kept
	// at the encoder's own limit (specialmenuimage.MaxInputBytes) so a big photo
	// is refused with a clear message instead of an encoder-internal one. The
	// STORED image is still bounded by bookingDocumentMaxImageBytes.
	bookingDocumentMaxImageInputBytes = specialmenuimage.MaxInputBytes
	// bookingDocumentMaxDocuments caps how many documents one booking (or one
	// editor draft) can hold, so a stuck draft cannot fill the CDN.
	bookingDocumentMaxDocuments = 50
	// bookingDocumentsKey is the notif-map key carrying the documents to send.
	bookingDocumentsKey = "__booking_documents"

	// bookingDocumentWSFrameSlackBytes covers base64 overhead plus the JSON
	// envelope of an upload frame. base64 expands 3 bytes into 4, so the
	// largest ACCEPTED file (bookingDocumentMaxUploadBytes) is ~4/3 that big on
	// the wire; the socket read limit must sit above it or a legal upload is
	// killed with a bare 1009 instead of a `too_large` answer.
	bookingDocumentWSFrameSlackBytes = 2 * 1024 * 1024
	// bookingDocumentWSReadLimitBytes is the per-frame read cap.
	bookingDocumentWSReadLimitBytes = int64(bookingDocumentMaxUploadBytes)*4/3 + bookingDocumentWSFrameSlackBytes
)

// bookingDocumentURLUnsafe mirrors stockDocumentFilenameUnsafe: the original
// name is metadata, never object identity, so it is sanitized before it goes
// into a Content-Disposition header.
var bookingDocumentURLUnsafe = regexp.MustCompile(`[^[:alnum:]. _-]+`)

// bookingDocument is one stored file plus everything the UI needs to list it.
type bookingDocument struct {
	ID               int64  `json:"id"`
	RestaurantID     int    `json:"restaurant_id"`
	BookingID        *int64 `json:"booking_id"`
	UploadedBy       int    `json:"uploaded_by"`
	Title            string `json:"title"`
	OriginalFilename string `json:"original_filename"`
	ContentType      string `json:"content_type"`
	SizeBytes        int64  `json:"size_bytes"`
	StoragePath      string `json:"-"`
	CreatedAt        string `json:"created_at"`
	// URL is the same-origin backoffice read path (always present, so the UI
	// has one address to use). CDNURL is the public pull URL used only to hand
	// the file to a third party that must fetch it (the WhatsApp gateway).
	URL     string `json:"url"`
	CDNURL  string `json:"cdn_url"`
	IsImage bool   `json:"is_image"`
}

// bookingDocumentFile is a document PLUS its bytes, the shape the email and the
// WhatsApp senders consume (the email as an attachment, WhatsApp as media).
type bookingDocumentFile struct {
	bookingDocument
	Data []byte
}

func (d bookingDocumentFile) emailAttachment() emailAttachment {
	return emailAttachment{Filename: d.downloadFilename(), ContentType: d.ContentType, Data: d.Data}
}

// downloadFilename is what the guest sees: the staff's title when set, the
// original name otherwise, always ending in the extension of what we stored.
func (d bookingDocumentFile) downloadFilename() string {
	base := strings.TrimSpace(d.Title)
	if base == "" {
		base = bookingDocumentFilename(d.OriginalFilename)
	}
	if ext := path.Ext(d.StoragePath); ext != "" && !strings.HasSuffix(strings.ToLower(base), strings.ToLower(ext)) {
		base += ext
	}
	if base == "" {
		base = "documento"
	}
	return bookingDocumentTruncate(base, 200)
}

// isImageContentType reports whether the payload is an image we must re-encode.
// A declared type can lie, so the bytes decide (http.DetectContentType).
func isBookingDocumentImage(payload []byte, declaredContentType string) bool {
	for _, ct := range []string{strings.ToLower(strings.TrimSpace(declaredContentType)), strings.ToLower(strings.TrimSpace(http.DetectContentType(payload)))} {
		if strings.HasPrefix(ct, "image/") {
			return true
		}
	}
	return false
}

// Sentinel errors behind the stable error codes. They exist because the code
// the UI branches on must NOT be derived from the human message: rewording a
// Spanish string would silently change the contract the client codes against.
var (
	errBookingDocumentTooLarge   = errors.New("booking document too large")
	errBookingDocumentEmpty      = errors.New("booking document empty")
	errBookingDocumentNoStorage  = errors.New("booking document storage not configured")
	errBookingDocumentInvalidReq = errors.New("booking document invalid request")
)

// bookingDocumentErrorCode maps a store error to the stable code the UI can
// branch on (the message stays Spanish for humans, the code is for the client).
func bookingDocumentErrorCode(err error) string {
	switch {
	case errors.Is(err, errBookingDocumentTooLarge):
		return "too_large"
	case errors.Is(err, errBookingDocumentEmpty):
		return "empty_file"
	case errors.Is(err, errBookingDocumentNoStorage):
		return "storage_not_configured"
	case errors.Is(err, errBookingDocumentInvalidReq):
		return "invalid_request"
	default:
		return "upload_failed"
	}
}

// bookingDocumentErrorMessage hides storage internals from the guest-facing UI.
// The human-facing text is fixed per sentinel so the internal English suffix
// that `%w` appends never reaches the browser.
func bookingDocumentErrorMessage(err error) string {
	switch {
	case errors.Is(err, errBookingDocumentNoStorage):
		return "Almacenamiento de documentos no configurado para este restaurante"
	case errors.Is(err, errBookingDocumentTooLarge):
		// Keep the Spanish explanation produced upstream, minus the sentinel.
		return bookingDocumentStripSentinel(err, errBookingDocumentTooLarge)
	case errors.Is(err, errBookingDocumentEmpty):
		return bookingDocumentStripSentinel(err, errBookingDocumentEmpty)
	case errors.Is(err, errBookingDocumentInvalidReq):
		return bookingDocumentStripSentinel(err, errBookingDocumentInvalidReq)
	case strings.Contains(strings.ToLower(err.Error()), "bunny"):
		return "No se pudo guardar el documento en el almacenamiento"
	default:
		return err.Error()
	}
}

// bookingDocumentStripSentinel removes the `...: <sentinel>` suffix that `%w`
// appends, leaving the Spanish message the error was built with.
func bookingDocumentStripSentinel(err error, sentinel error) string {
	msg := err.Error()
	if sentinel == nil {
		return msg
	}
	suffix := ": " + sentinel.Error()
	return strings.TrimSpace(strings.TrimSuffix(msg, suffix))
}

// bookingDocumentTruncate cuts a display name to at most max BYTES without
// splitting a UTF-8 rune in half: a broken final byte produces an invalid
// latin-1 filename in Content-Disposition and is rejected by some SMTP servers.
// Coordination id: booking_documents_v1
func bookingDocumentTruncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	cut := value[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func bookingDocumentFilename(value string) string {
	value = bookingDocumentURLUnsafe.ReplaceAllString(path.Base(strings.TrimSpace(value)), "_")
	if value == "" || value == "." {
		return "documento"
	}
	return bookingDocumentTruncate(value, 200)
}

// bookingDocumentObjectPath is the storage path format, tenant-scoped:
//   {restaurant_id}/booking-documents/{draft|bookings/{booking_id}}/{uuid}{ext}
// The uuid keeps the object unguessable and collision free; the original file
// name never becomes part of the path.
func bookingDocumentObjectPath(restaurantID int, bookingID *int64, ext string) string {
	scope := "draft"
	if bookingID != nil && *bookingID > 0 {
		scope = path.Join("bookings", strconv.FormatInt(*bookingID, 10))
	}
	if ext == "" {
		ext = ".bin"
	}
	return path.Join(strconv.Itoa(restaurantID), "booking-documents", scope, uuid.NewString()+ext)
}

// prepareBookingDocumentPayload applies the storage policy: an image is stored
// as webp of at most bookingDocumentMaxImageBytes, everything else is stored
// untouched. An image that already fits and is already webp is stored as-is -
// no encoder runs, so a small webp can never fail an upload.
//
// Returns the bytes to store, their content type and the extension.
func (s *Server) prepareBookingDocumentPayload(ctx context.Context, payload []byte, filename, declaredContentType string) (body []byte, contentType, ext string, err error) {
	if !isBookingDocumentImage(payload, declaredContentType) {
		ct := strings.ToLower(strings.TrimSpace(declaredContentType))
		if ct == "" || ct == "application/octet-stream" {
			ct = strings.ToLower(strings.TrimSpace(http.DetectContentType(payload)))
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		return payload, ct, bookingDocumentExtForContentType(ct), nil
	}

	if len(payload) > bookingDocumentMaxImageInputBytes {
		return nil, "", "", fmt.Errorf("la imagen supera el máximo de %d MB; comprímela antes de subirla: %w",
			bookingDocumentMaxImageInputBytes/(1024*1024), errBookingDocumentTooLarge)
	}
	detectedType, _, err := specialmenuimage.ImageContentTypeAndExt(payload, filename, declaredContentType)
	if err != nil {
		return nil, "", "", err
	}
	// Already webp and within budget: store the original bytes untouched.
	if detectedType == "image/webp" && len(payload) <= bookingDocumentMaxImageBytes {
		return payload, "image/webp", ".webp", nil
	}
	// Anything else, or a webp above the budget, is re-encoded down to it.
	normalized, err := specialmenuimage.NormalizeToWebPWithLimit(ctx, payload, filename, declaredContentType, bookingDocumentMaxImageBytes)
	if err != nil {
		return nil, "", "", err
	}
	if len(normalized) == 0 {
		return nil, "", "", errors.New("no se pudo comprimir la imagen")
	}
	return normalized, "image/webp", ".webp", nil
}

func bookingDocumentExtForContentType(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	case "text/markdown":
		return ".md"
	case "text/csv":
		return ".csv"
	}
	return ""
}

// -----------------------------------------------------------------------------
// Store
// -----------------------------------------------------------------------------

// saveBookingDocument stores one uploaded payload and its metadata row. A
// bookingID of nil leaves the document as a draft owned by userID.
func (s *Server) saveBookingDocument(ctx context.Context, restaurantID int, userID int, bookingID *int64, payload []byte, filename, declaredContentType, title string) (bookingDocument, error) {
	if restaurantID <= 0 || userID <= 0 {
		return bookingDocument{}, fmt.Errorf("invalid document owner: %w", errBookingDocumentInvalidReq)
	}
	if len(payload) == 0 {
		return bookingDocument{}, fmt.Errorf("el archivo está vacío: %w", errBookingDocumentEmpty)
	}
	if len(payload) > bookingDocumentMaxUploadBytes {
		return bookingDocument{}, fmt.Errorf("el archivo supera el máximo de %d MB: %w", bookingDocumentMaxUploadBytes/(1024*1024), errBookingDocumentTooLarge)
	}
	if !s.bunnyConfigured(ctx, restaurantID) {
		return bookingDocument{}, fmt.Errorf("almacenamiento de documentos no configurado para este restaurante: %w", errBookingDocumentNoStorage)
	}

	body, contentType, ext, err := s.prepareBookingDocumentPayload(ctx, payload, filename, declaredContentType)
	if err != nil {
		return bookingDocument{}, err
	}

	// The public zone (not the private one) because a booking document is a
	// guest-facing artifact: the WhatsApp gateway has to be able to fetch the
	// file by URL to forward it, exactly like the receipt PDF and the booking QR
	// already do. The path carries a uuid, so the URL is unguessable even
	// though the zone is readable.
	objectPath := bookingDocumentObjectPath(restaurantID, bookingID, ext)
	if err := s.bunnyPut(ctx, restaurantID, objectPath, body, contentType); err != nil {
		return bookingDocument{}, err
	}

	original := bookingDocumentFilename(filename)
	if original == "documento" {
		// No usable name came in (or it was sanitized away): keep the extension
		// so the download still opens.
		original = "documento" + ext
	}
	doc := bookingDocument{
		RestaurantID:     restaurantID,
		BookingID:        bookingID,
		UploadedBy:       userID,
		Title:            bookingDocumentTitle(title, original),
		OriginalFilename: original,
		ContentType:      contentType,
		SizeBytes:        int64(len(body)),
		StoragePath:      objectPath,
	}
	rowID, err := s.insertBookingDocument(ctx, doc)
	if err != nil {
		// A row we cannot write means an orphan object; drop it rather than
		// leaving storage nobody can ever reach again.
		if delErr := s.bunnyDelete(ctx, restaurantID, objectPath); delErr != nil {
			log.Printf("[booking_documents_v1] restaurant=%d orphan_delete_failed path=%s err=%v", restaurantID, objectPath, delErr)
		}
		return bookingDocument{}, err
	}
	doc.ID = rowID
	doc.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	doc.URL = bookingDocumentDownloadURL(doc.ID)
	doc.CDNURL = s.bunnyPullURL(ctx, restaurantID, objectPath)
	doc.IsImage = strings.HasPrefix(contentType, "image/")
	return doc, nil
}

// bookingDocumentTitle keeps the editable title, falling back to the original
// file name so the list never shows an empty row.
func bookingDocumentTitle(title, original string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = original
	}
	return bookingDocumentTruncate(title, 255)
}

func (s *Server) insertBookingDocument(ctx context.Context, doc bookingDocument) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO booking_documents
			(restaurant_id, booking_id, uploaded_by, title, original_filename,
			 content_type, size_bytes, storage_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		doc.RestaurantID, doc.BookingID, doc.UploadedBy, doc.Title,
		doc.OriginalFilename, doc.ContentType, doc.SizeBytes, doc.StoragePath)
	if err != nil {
		log.Printf("[booking_documents_v1] insert_failed restaurant=%d err=%v", doc.RestaurantID, err)
		return 0, err
	}
	return res.LastInsertId()
}

// listBookingDocuments returns the live documents of a booking, oldest first so
// the UI keeps the order the staff attached them in.
func (s *Server) listBookingDocuments(ctx context.Context, restaurantID int, bookingID int64) ([]bookingDocument, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, restaurant_id, booking_id, uploaded_by, title, original_filename,
		       content_type, size_bytes, storage_path, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s')
		FROM booking_documents
		WHERE restaurant_id = ? AND booking_id = ? AND deleted_at IS NULL
		ORDER BY id ASC`, restaurantID, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []bookingDocument{}
	for rows.Next() {
		doc, err := scanBookingDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s.withCDNURL(ctx, restaurantID, doc))
	}
	return out, rows.Err()
}

type bookingDocumentScanner interface {
	Scan(dest ...any) error
}

func scanBookingDocument(row bookingDocumentScanner) (bookingDocument, error) {
	var doc bookingDocument
	var bookingID sql.NullInt64
	if err := row.Scan(&doc.ID, &doc.RestaurantID, &bookingID, &doc.UploadedBy, &doc.Title,
		&doc.OriginalFilename, &doc.ContentType, &doc.SizeBytes, &doc.StoragePath, &doc.CreatedAt); err != nil {
		return bookingDocument{}, err
	}
	if bookingID.Valid {
		id := bookingID.Int64
		doc.BookingID = &id
	}
	doc.URL = bookingDocumentDownloadURL(doc.ID)
	doc.IsImage = strings.HasPrefix(strings.ToLower(doc.ContentType), "image/")
	return doc, nil
}

// withCDNURL fills the public pull URL of a stored document, derived from the
// tenant's own pull zone (never from a value stored in the row, so a database
// edit cannot turn this into an open redirect).
func (s *Server) withCDNURL(ctx context.Context, restaurantID int, doc bookingDocument) bookingDocument {
	if doc.StoragePath != "" && s.bunnyConfigured(ctx, restaurantID) {
		doc.CDNURL = s.bunnyPullURL(ctx, restaurantID, doc.StoragePath)
	}
	return doc
}

// loadBookingDocument fetches one live document owned by the tenant.
func (s *Server) loadBookingDocument(ctx context.Context, restaurantID int, id int64) (bookingDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, restaurant_id, booking_id, uploaded_by, title, original_filename,
		       content_type, size_bytes, storage_path, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s')
		FROM booking_documents
		WHERE restaurant_id = ? AND id = ? AND deleted_at IS NULL
		LIMIT 1`, restaurantID, id)
	return scanBookingDocument(row)
}

// listBookingDocumentDrafts returns the caller's own unbound uploads, the
// pending attachments of an editor whose booking does not exist yet.
func (s *Server) listBookingDocumentDrafts(ctx context.Context, restaurantID int, userID int) ([]bookingDocument, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, restaurant_id, booking_id, uploaded_by, title, original_filename,
		       content_type, size_bytes, storage_path, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s')
		FROM booking_documents
		WHERE restaurant_id = ? AND uploaded_by = ? AND booking_id IS NULL AND deleted_at IS NULL
		ORDER BY id ASC`, restaurantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []bookingDocument{}
	for rows.Next() {
		doc, err := scanBookingDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s.withCDNURL(ctx, restaurantID, doc))
	}
	return out, rows.Err()
}

// loadOwnedBookingDocument fetches a document the caller is allowed to delete:
// its own draft, or any document of a booking of the same restaurant.
func (s *Server) loadOwnedBookingDocument(ctx context.Context, restaurantID int, userID int, id int64) (bookingDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, restaurant_id, booking_id, uploaded_by, title, original_filename,
		       content_type, size_bytes, storage_path, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s')
		FROM booking_documents
		WHERE restaurant_id = ? AND id = ? AND deleted_at IS NULL
		  AND (booking_id IS NOT NULL OR uploaded_by = ?)
		LIMIT 1`, restaurantID, id, userID)
	return scanBookingDocument(row)
}

// deleteBookingDocument removes the object from BunnyCDN and soft-deletes the
// row. The row is kept so a booking's attachment history is not rewritten by a
// later edit, and so a failed Bunny delete never orphans the metadata.
func (s *Server) deleteBookingDocument(ctx context.Context, restaurantID int, id int64) error {
	doc, err := s.loadBookingDocument(ctx, restaurantID, id)
	if err != nil {
		return err
	}
	if doc.StoragePath != "" {
		if err := s.bunnyDelete(ctx, restaurantID, doc.StoragePath); err != nil {
			log.Printf("[booking_documents_v1] restaurant=%d document=%d delete_failed err=%v", restaurantID, id, err)
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE booking_documents SET deleted_at = NOW(), storage_path = ''
		WHERE restaurant_id = ? AND id = ? AND deleted_at IS NULL`, restaurantID, id); err != nil {
		return err
	}
	return nil
}

// purgeBookingDocuments releases every document attached to a booking that is
// being cancelled or deleted.
//
// The table has no FK to bookings (a booking row can also be removed by paths
// this feature does not own), so without this the rows would stay live and
// downloadable by id forever, would keep counting against
// bookingDocumentMaxDocuments, and would leave the Bunny objects unreleased.
// Called AFTER the booking delete has committed: a failure here leaves rows
// that purgeOrphanedBookingDocuments can still collect, whereas running it
// inside the delete transaction would release objects for a booking that may
// still roll back.
//
// Coordination id: booking_documents_v1
func (s *Server) purgeBookingDocuments(ctx context.Context, restaurantID int, bookingID int64) {
	if restaurantID <= 0 || bookingID <= 0 {
		return
	}
	docs, err := s.listBookingDocuments(ctx, restaurantID, bookingID)
	if err != nil {
		log.Printf("[booking_documents_v1] restaurant=%d booking=%d purge_list_failed err=%v", restaurantID, bookingID, err)
		return
	}
	for _, doc := range docs {
		if err := s.deleteBookingDocument(ctx, restaurantID, doc.ID); err != nil {
			log.Printf("[booking_documents_v1] restaurant=%d booking=%d purge_document_failed document=%d err=%v", restaurantID, bookingID, doc.ID, err)
		}
	}
}

// purgeOrphanedBookingDocuments soft-deletes documents whose booking no longer
// exists. It is the safety net for a booking row removed by a path that does
// not call purgeBookingDocuments, so a dangling row can never stay served.
func (s *Server) purgeOrphanedBookingDocuments(ctx context.Context, restaurantID int) {
	if restaurantID <= 0 {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM booking_documents
		WHERE restaurant_id = ? AND deleted_at IS NULL AND booking_id IS NOT NULL
		  AND booking_id NOT IN (SELECT id FROM bookings WHERE restaurant_id = ?)
		LIMIT 200`, restaurantID, restaurantID)
	if err != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.deleteBookingDocument(ctx, restaurantID, id); err != nil {
			log.Printf("[booking_documents_v1] restaurant=%d orphan_document=%d purge_failed err=%v", restaurantID, id, err)
			continue
		}
		log.Printf("[booking_documents_v1] restaurant=%d orphan_document=%d purged (booking no longer exists)", restaurantID, id)
	}
}

// bindDraftDocumentsToBooking claims the draft ids a booking-create sent and
// attaches them to the new booking. Ids that do not belong to this restaurant
// or this uploader, are already bound, are deleted or are not drafts are
// ignored rather than failing the whole booking: the reservation itself must
// not be lost because of a stale attachment chip.
func (s *Server) bindDraftDocumentsToBooking(ctx context.Context, restaurantID int, userID int, bookingID int64, ids []int64) int {
	if len(ids) == 0 || bookingID <= 0 {
		return 0
	}
	claimed := 0
	for _, id := range ids {
		res, err := s.db.ExecContext(ctx, `
			UPDATE booking_documents SET booking_id = ?
			WHERE restaurant_id = ? AND id = ? AND uploaded_by = ?
			  AND booking_id IS NULL AND deleted_at IS NULL`,
			bookingID, restaurantID, id, userID)
		if err != nil {
			log.Printf("[booking_documents_v1] restaurant=%d bind_failed document=%d err=%v", restaurantID, id, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			claimed++
		}
	}
	return claimed
}

// countBookingDocuments caps how many documents one booking or one editor
// draft can accumulate.
func (s *Server) countBookingDocuments(ctx context.Context, restaurantID int, bookingID *int64, userID int) (int, error) {
	var total int
	var err error
	if bookingID != nil && *bookingID > 0 {
		err = s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM booking_documents
			WHERE restaurant_id = ? AND booking_id = ? AND deleted_at IS NULL`, restaurantID, *bookingID).Scan(&total)
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM booking_documents
			WHERE restaurant_id = ? AND uploaded_by = ? AND booking_id IS NULL AND deleted_at IS NULL`, restaurantID, userID).Scan(&total)
	}
	return total, err
}

// purgeOrphanedBookingDocumentsAsync runs the orphan sweep off the request path:
// the list/send/read handlers must never pay for it synchronously.
func (s *Server) purgeOrphanedBookingDocumentsAsync(restaurantID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go s.purgeOrphanedBookingDocuments(ctx, restaurantID)
}

// bookingDocumentPayloadsForSend downloads every live document of a booking, so
// the confirmation email can attach them and WhatsApp can forward them.
// It returns what it could fetch: a storage hiccup must not cost the guest the
// reservation confirmation itself.
func (s *Server) bookingDocumentPayloadsForSend(ctx context.Context, restaurantID int, bookingID int64) []bookingDocumentFile {
	if restaurantID <= 0 || bookingID <= 0 || !s.bunnyConfigured(ctx, restaurantID) {
		return nil
	}
	docs, err := s.listBookingDocuments(ctx, restaurantID, bookingID)
	if err != nil {
		log.Printf("[booking_documents_v1] restaurant=%d booking=%d list_failed err=%v", restaurantID, bookingID, err)
		return nil
	}
	out := make([]bookingDocumentFile, 0, len(docs))
	for _, doc := range docs {
		payload, storedType, err := s.bookingDocumentFetch(ctx, restaurantID, doc)
		if err != nil || len(payload) == 0 {
			log.Printf("[booking_documents_v1] restaurant=%d booking=%d document=%d fetch_failed err=%v", restaurantID, bookingID, doc.ID, err)
			continue
		}
		if strings.TrimSpace(storedType) != "" {
			doc.ContentType = storedType
		}
		out = append(out, bookingDocumentFile{bookingDocument: doc, Data: payload})
	}
	return out
}

// bookingDocumentHTTPClient is shared by every document fetch so a booking with
// 50 documents reuses one connection pool against Bunny instead of opening a
// new TLS connection per file.
var bookingDocumentHTTPClient = &http.Client{Timeout: 20 * time.Second}

// bookingDocumentFetch reads a stored document back. It uses the private-zone
// helper when the object lives there and a plain pull-zone GET otherwise, so
// the same code works for objects stored before or after the zone moved.
func (s *Server) bookingDocumentFetch(ctx context.Context, restaurantID int, doc bookingDocument) ([]byte, string, error) {
	pullBase := strings.TrimRight(s.bunnyCreds(ctx, restaurantID).PullBaseURL, "/")
	src := s.bunnyPullURL(ctx, restaurantID, doc.StoragePath)
	if pullBase != "" && strings.HasPrefix(src, pullBase+"/") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, "", err
		}
		res, err := bookingDocumentHTTPClient.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("document fetch failed (%d)", res.StatusCode)
		}
		payload, err := io.ReadAll(io.LimitReader(res.Body, bookingDocumentMaxUploadBytes+1))
		if err != nil || len(payload) == 0 || len(payload) > bookingDocumentMaxUploadBytes {
			return nil, "", errors.New("invalid document payload")
		}
		return payload, res.Header.Get("Content-Type"), nil
	}
	return s.bunnyPrivateGet(ctx, restaurantID, doc.StoragePath)
}

// bookingDocumentDownloadURL is the same-origin, session-authenticated URL the
// backoffice uses to preview or download a document.
func bookingDocumentDownloadURL(id int64) string {
	return "/api/admin/bookings/documents/" + strconv.FormatInt(id, 10) + "/file"
}

// decodeBookingDocumentBase64 accepts a browser File read as a data URL or as
// raw base64. The client sends the bytes over the socket as base64 because a
// WebSocket text frame cannot carry arbitrary bytes; a binary frame would force
// every consumer to handle two encodings.
func decodeBookingDocumentBase64(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("el archivo está vacío: %w", errBookingDocumentEmpty)
	}
	if i := strings.Index(encoded, ","); strings.HasPrefix(encoded, "data:") && i > 0 {
		encoded = encoded[i+1:]
	}
	if decodedLen := base64.StdEncoding.DecodedLen(len(encoded)); decodedLen > bookingDocumentMaxUploadBytes+1024 {
		return nil, fmt.Errorf("el archivo supera el máximo de %d MB: %w", bookingDocumentMaxUploadBytes/(1024*1024), errBookingDocumentTooLarge)
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Browsers sometimes emit base64 without padding.
		payload, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer el archivo (base64 inválido): %w", errBookingDocumentInvalidReq)
		}
	}
	return payload, nil
}

// -----------------------------------------------------------------------------
// REST surface
// -----------------------------------------------------------------------------

// handleBOBookingDocumentsList answers the documents of one booking.
// GET /api/admin/bookings/{id}/documents
func (s *Server) handleBOBookingDocumentsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}
	bookingID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || bookingID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid booking id"})
		return
	}
	if !s.bookingBelongsToRestaurant(r.Context(), a.ActiveRestaurantID, bookingID) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Booking not found"})
		return
	}
	docs, err := s.listBookingDocuments(r.Context(), a.ActiveRestaurantID, bookingID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudieron cargar los documentos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"documents": docs,
	})
}

// handleBOBookingDocumentFile streams a stored document same-origin. BunnyCDN
// does not answer the pull zone with CORS headers, so the backoffice cannot
// fetch the object directly; this is the authenticated, tenant-scoped read
// path (no open proxy: the row decides which object is served, and the URL is
// only rebuilt from the restaurant's own pull base).
// GET /api/admin/bookings/documents/{docId}/file
func (s *Server) handleBOBookingDocumentFile(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}
	docID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "docId")), 10, 64)
	if err != nil || docID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid document id")
		return
	}
	ctx := r.Context()
	doc, err := s.loadBookingDocument(ctx, a.ActiveRestaurantID, docID)
	if err != nil {
		// A DB/storage outage must not be reported as "not found": to the staff
		// those two are indistinguishable and a transient blip then looks like
		// data loss.
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "Documento no encontrado")
		} else {
			log.Printf("[booking_documents_v1] restaurant=%d document=%d read_failed err=%v", a.ActiveRestaurantID, docID, err)
			httpx.WriteError(w, http.StatusInternalServerError, "No se pudo leer el documento")
		}
		return
	}
	payload, _, err := s.bookingDocumentFetch(ctx, a.ActiveRestaurantID, doc)
	if err != nil || len(payload) == 0 {
		httpx.WriteError(w, http.StatusBadGateway, "Documento no disponible")
		return
	}
	filename := strings.ReplaceAll(bookingDocumentFile{bookingDocument: doc}.downloadFilename(), `"`, ``)
	contentType, disposition := bookingDocumentServeHeaders(payload)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition+`; filename="`+filename+`"`)
	// The bytes are user supplied and the route is same-origin: without nosniff
	// a browser may sniff an `attachment` response back into HTML and execute it
	// in the authenticated backoffice origin. There is no CSP middleware in this
	// repo, so the header is the only barrier.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// bookingDocumentServeHeaders decides how the raw bytes may be served.
//
// SECURITY: the payload and the declared mime both come from the uploader, so
// they are NOT trusted for a decision that controls script execution. The bytes
// are sniffed with http.DetectContentType and only a closed allowlist of types
// a browser renders without a script context is served `inline`; everything
// else (HTML, SVG, XML, unknown) is forced to `application/octet-stream` and
// served as `attachment`, which is what handleBOStockDocumentOriginalGet
// already does for stored uploads.
func bookingDocumentServeHeaders(payload []byte) (contentType, disposition string) {
	// Images were normalised to webp on upload, but detect on the BYTES: a
	// declared `image/webp` on an HTML body must not be served inline.
	sniffed := strings.ToLower(strings.TrimSpace(http.DetectContentType(payload)))
	sniffed, _, _ = strings.Cut(sniffed, ";")

	switch {
	case strings.HasPrefix(sniffed, "image/webp"),
		strings.HasPrefix(sniffed, "image/png"),
		strings.HasPrefix(sniffed, "image/gif"),
		sniffed == "image/jpeg",
		sniffed == "application/pdf",
		sniffed == "text/plain":
		// A renderer with no script context. text/plain is only inert while
		// nosniff holds, which the caller sets.
		return sniffed, "inline"
	default:
		// Everything else, including anything whose sniffed type does not match
		// what was stored (a declared image on non-image bytes), is opaque.
		return "application/octet-stream", "attachment"
	}
}
