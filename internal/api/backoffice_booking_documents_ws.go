package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Socket transport for the booking document upload / list / delete flow.
// Coordination id: booking_documents_v1
//
// One socket handles the three concerns of /app/reservas/anadir and the
// reservas list. Messages are type-discriminated, and every answer comes back
// on the same socket, so a large upload never has to be relayed through a
// second HTTP hop that could time out.
//
//	-> {"type":"bookingDocumentUpload","filename":"menu.pdf","mimeType":"application/pdf","title":"Menú","bookingId":123|null,"dataBase64":"..."}
//	<- {"type":"bookingDocumentUploadOk","requestId":"r1","document":{...}}
//	<- {"type":"bookingDocumentUploadError","requestId":"r1","message":"...","code":"too_large"}
//
//	-> {"type":"bookingDocumentList","bookingId":123}       (bookingId 0 = my drafts)
//	<- {"type":"bookingDocumentListOk","requestId":"r1","documents":[...],"drafts":[...]}
//
//	-> {"type":"bookingDocumentDelete","id":42,"confirmed":true}
//	<- {"type":"bookingDocumentDeleteOk","requestId":"r1","id":42}
//	<- {"type":"bookingDocumentDeleteError","requestId":"r1","message":"..."}
//
// DELETE REQUIRES CONFIRMATION: the server only removes an object when the
// client sends `confirmed:true`. A delete message without it is answered with
// `bookingDocumentDeleteConfirm` so the UI can ask first, which is the whole
// point of the handshake.
//
// Tenant scoping: the restaurant comes from the session, never from the
// message, and every statement filters on restaurant_id.
// =============================================================================

// bookingDocumentWSClient is one backoffice connection. It is write-serialized
// so a ping can never interleave inside a JSON frame.
type bookingDocumentWSClient struct {
	conn         *websocket.Conn
	restaurantID int
	userID       int
	mu           sync.Mutex
}

func (c *bookingDocumentWSClient) send(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *bookingDocumentWSClient) ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second))
}

func (c *bookingDocumentWSClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

// bookingDocumentWSMessage is the union of what the client may send.
type bookingDocumentWSMessage struct {
	Type       string `json:"type"`
	RequestID  string `json:"requestId,omitempty"`
	ID         int64  `json:"id,omitempty"`
	BookingID  int64  `json:"bookingId,omitempty"`
	Filename   string `json:"filename,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	Title      string `json:"title,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
	Confirmed  bool   `json:"confirmed,omitempty"`
}

// handleBOBookingDocumentsWS upgrades GET /api/admin/bookings/documents/ws and
// serves upload, list and delete for the session's restaurant.
func (s *Server) handleBOBookingDocumentsWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}
	conn, err := boTablesWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &bookingDocumentWSClient{conn: conn, restaurantID: a.ActiveRestaurantID, userID: a.User.ID}

	// The cap is the file cap plus base64 overhead, so a peer cannot make the
	// server buffer an arbitrarily large frame.
	conn.SetReadLimit(int64(bookingDocumentMaxUploadBytes) * 2)

	go func() {
		defer func() { _ = client.close() }()

		// A dead peer otherwise holds the connection open indefinitely.
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		go func() {
			for range ticker.C {
				if err := client.ping(); err != nil {
					_ = client.close()
					return
				}
			}
		}()

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg bookingDocumentWSMessage
			if json.Unmarshal(raw, &msg) != nil {
				_ = client.send(map[string]any{
					"type":    "bookingDocumentError",
					"message": "Mensaje no válido",
				})
				continue
			}
			switch msg.Type {
			case "bookingDocumentUpload":
				s.bookingDocumentWSUpload(client, msg)
			case "bookingDocumentList":
				s.bookingDocumentWSList(client, msg)
			case "bookingDocumentDelete":
				s.bookingDocumentWSDelete(client, msg)
			case "ping":
				_ = client.send(map[string]any{"type": "pong"})
			default:
				_ = client.send(map[string]any{
					"type":        "bookingDocumentError",
					"requestId":   msg.RequestID,
					"message":     "Tipo de mensaje no soportado",
					"unsupported": true,
				})
			}
		}
	}()
}

// bookingDocumentWSUpload stores one payload and answers on the socket.
func (s *Server) bookingDocumentWSUpload(c *bookingDocumentWSClient, msg bookingDocumentWSMessage) {
	ctx := context.Background()

	payload, err := decodeBookingDocumentBase64(msg.DataBase64)
	if err != nil {
		s.bookingDocumentWSFail(c, msg, bookingDocumentErrorCode(err), err.Error())
		return
	}

	// A booking_id that does not exist for this restaurant must never be
	// writable through the socket: it would leak the object into another
	// tenant's booking (or into a booking that does not exist yet).
	var bookingID *int64
	if msg.BookingID > 0 {
		if !s.bookingBelongsToRestaurant(ctx, c.restaurantID, msg.BookingID) {
			s.bookingDocumentWSFail(c, msg, "not_found", "Reserva no encontrada")
			return
		}
		id := msg.BookingID
		bookingID = &id
	}

	total, err := s.countBookingDocuments(ctx, c.restaurantID, bookingID, c.userID)
	if err != nil {
		s.bookingDocumentWSFail(c, msg, "server_error", "No se pudo comprobar el número de documentos")
		return
	}
	if total >= bookingDocumentMaxDocuments {
		s.bookingDocumentWSFail(c, msg, "too_many", "Se alcanzó el máximo de documentos por reserva")
		return
	}

	doc, err := s.saveBookingDocument(ctx, c.restaurantID, c.userID, bookingID, payload, msg.Filename, msg.MimeType, msg.Title)
	if err != nil {
		log.Printf("[booking_documents_v1] restaurant=%d upload_failed err=%v", c.restaurantID, err)
		s.bookingDocumentWSFail(c, msg, bookingDocumentErrorCode(err), bookingDocumentErrorMessage(err))
		return
	}

	_ = c.send(map[string]any{
		"type":      "bookingDocumentUploadOk",
		"requestId": msg.RequestID,
		"document":  doc,
	})
}

// bookingDocumentWSList answers with the documents of a booking, and always
// with the caller's own drafts, so the editor keeps its pending attachments
// after a reload.
func (s *Server) bookingDocumentWSList(c *bookingDocumentWSClient, msg bookingDocumentWSMessage) {
	ctx := context.Background()

	documents := []bookingDocument{}
	if msg.BookingID > 0 {
		if !s.bookingBelongsToRestaurant(ctx, c.restaurantID, msg.BookingID) {
			s.bookingDocumentWSFail(c, msg, "not_found", "Reserva no encontrada")
			return
		}
		docs, err := s.listBookingDocuments(ctx, c.restaurantID, msg.BookingID)
		if err != nil {
			s.bookingDocumentWSFail(c, msg, "server_error", "No se pudieron cargar los documentos")
			return
		}
		documents = docs
	}

	drafts, err := s.listBookingDocumentDrafts(ctx, c.restaurantID, c.userID)
	if err != nil {
		s.bookingDocumentWSFail(c, msg, "server_error", "No se pudieron cargar los documentos")
		return
	}

	_ = c.send(map[string]any{
		"type":      "bookingDocumentListOk",
		"requestId": msg.RequestID,
		"documents": documents,
		"drafts":    drafts,
	})
}

// bookingDocumentWSDelete removes one document, but only after the client has
// confirmed. This mirrors handleBOStockDocumentOriginalDelete, which already
// gates its REST delete behind an explicit confirmation path.
func (s *Server) bookingDocumentWSDelete(c *bookingDocumentWSClient, msg bookingDocumentWSMessage) {
	ctx := context.Background()

	if msg.ID <= 0 {
		s.bookingDocumentWSFail(c, msg, "invalid_request", "Falta el id del documento")
		return
	}
	if !msg.Confirmed {
		// The server never deletes on an unconfirmed request.
		_ = c.send(map[string]any{
			"type":      "bookingDocumentDeleteConfirm",
			"requestId": msg.RequestID,
			"id":        msg.ID,
			"message":   "¿Eliminar definitivamente este documento?",
		})
		return
	}

	// A draft belongs to its uploader; a bound document belongs to the
	// restaurant. Both are checked with one owner-scoped read.
	doc, err := s.loadOwnedBookingDocument(ctx, c.restaurantID, c.userID, msg.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.bookingDocumentWSFail(c, msg, "not_found", "Documento no encontrado")
			return
		}
		s.bookingDocumentWSFail(c, msg, "server_error", "No se pudo eliminar el documento")
		return
	}
	if err := s.deleteBookingDocument(ctx, c.restaurantID, doc.ID); err != nil {
		s.bookingDocumentWSFail(c, msg, "server_error", "No se pudo eliminar el documento")
		return
	}

	_ = c.send(map[string]any{
		"type":      "bookingDocumentDeleteOk",
		"requestId": msg.RequestID,
		"id":        msg.ID,
	})
}

// bookingDocumentWSFail answers with "<action>Error", so the client can route
// the failure to the same handler that started the request (the same convention
// the technical-sheets socket uses with search / searchError).
func (s *Server) bookingDocumentWSFail(c *bookingDocumentWSClient, msg bookingDocumentWSMessage, code, message string) {
	action := strings.TrimPrefix(strings.TrimSpace(msg.Type), "bookingDocument")
	if action == "" || action == msg.Type {
		action = "Upload"
	}
	_ = c.send(map[string]any{
		"type":      "bookingDocument" + action + "Error",
		"requestId": msg.RequestID,
		"code":      code,
		"message":   message,
	})
}

// bookingBelongsToRestaurant is the tenant guard used before a socket message
// may name a booking.
func (s *Server) bookingBelongsToRestaurant(ctx context.Context, restaurantID int, bookingID int64) bool {
	var found int
	if err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM bookings WHERE restaurant_id = ? AND id = ? LIMIT 1`, restaurantID, bookingID).Scan(&found); err != nil {
		return false
	}
	return found == 1
}
