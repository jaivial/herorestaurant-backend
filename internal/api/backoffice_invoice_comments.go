package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"

	"github.com/go-chi/chi/v5"
)

type boInvoiceComment struct {
	ID        int64      `json:"id"`
	InvoiceID int64      `json:"invoice_id"`
	Content   string     `json:"content"`
	UserID    int        `json:"user_id"`
	UserName  string     `json:"user_name"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
}

// GET /api/admin/invoices/{id}/comments
func (s *Server) handleBOInvoiceCommentsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice id")
		return
	}

	// Tenant ownership: only comments of invoices owned by the session
	// restaurant. A foreign or nonexistent invoice yields an empty list
	// (uniform outcome, no existence leak across tenants).
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT c.id, c.invoice_id, c.content, c.user_id, COALESCE(u.name, u.email, '') as user_name,
		       c.created_at, c.updated_at
		FROM bo_invoice_comments c
		JOIN invoices i ON i.id = c.invoice_id AND i.restaurant_id = ?
		LEFT JOIN bo_users u ON u.id = c.user_id
		WHERE c.invoice_id = ?
		ORDER BY c.created_at ASC
	`, a.ActiveRestaurantID, invoiceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching comments")
		return
	}
	defer rows.Close()

	var comments []boInvoiceComment
	for rows.Next() {
		var c boInvoiceComment
		var updatedAt sql.NullTime
		if err := rows.Scan(&c.ID, &c.InvoiceID, &c.Content, &c.UserID, &c.UserName, &c.CreatedAt, &updatedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading comment")
			return
		}
		if updatedAt.Valid {
			c.UpdatedAt = &updatedAt.Time
		}
		comments = append(comments, c)
	}

	if comments == nil {
		comments = []boInvoiceComment{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"comments": comments,
		"total":    len(comments),
	})
}

// POST /api/admin/invoices/{id}/comments
func (s *Server) handleBOInvoiceCommentCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice id")
		return
	}

	var input struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	content := strings.TrimSpace(input.Content)
	if content == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Content is required",
		})
		return
	}

	// Tenant ownership: refuse comments on invoices the session restaurant
	// does not own. Foreign and nonexistent invoices get the same 404 (no
	// existence leak across tenants).
	var owned int
	err = s.db.QueryRowContext(r.Context(), `
		SELECT 1 FROM invoices WHERE id = ? AND restaurant_id = ?
	`, invoiceID, a.ActiveRestaurantID).Scan(&owned)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking invoice")
		return
	}

	now := time.Now()
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO bo_invoice_comments (invoice_id, content, user_id, created_at)
		VALUES (?, ?, ?, ?)
	`, invoiceID, content, a.User.ID, now)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating comment")
		return
	}

	id, _ := res.LastInsertId()

	comment := boInvoiceComment{
		ID:        id,
		InvoiceID: invoiceID,
		Content:   content,
		UserID:    a.User.ID,
		UserName:  a.User.Name,
		CreatedAt: now,
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"comment": comment,
	})
}

// PUT /api/admin/invoices/{id}/comments/{commentId}
func (s *Server) handleBOInvoiceCommentUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice id")
		return
	}

	commentID, err := strconv.ParseInt(chi.URLParam(r, "commentId"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid comment id")
		return
	}

	var input struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	content := strings.TrimSpace(input.Content)
	if content == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Content is required",
		})
		return
	}

	// Tenant ownership: the invoice must belong to the session restaurant.
	// A foreign invoice yields the same "Comment not found" outcome as a
	// nonexistent one (uniform, no existence leak).
	result, err := s.db.ExecContext(r.Context(), `
		UPDATE bo_invoice_comments
		SET content = ?, updated_at = NOW()
		WHERE id = ? AND invoice_id = ?
		  AND invoice_id IN (SELECT id FROM invoices WHERE restaurant_id = ?)
	`, content, commentID, invoiceID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating comment")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Comment not found",
		})
		return
	}

	var c boInvoiceComment
	var updatedAt sql.NullTime
	err = s.db.QueryRowContext(r.Context(), `
		SELECT c.id, c.invoice_id, c.content, c.user_id, COALESCE(u.name, u.email, '') as user_name,
		       c.created_at, c.updated_at
		FROM bo_invoice_comments c
		LEFT JOIN bo_users u ON u.id = c.user_id
		WHERE c.id = ?
	`, commentID).Scan(&c.ID, &c.InvoiceID, &c.Content, &c.UserID, &c.UserName, &c.CreatedAt, &updatedAt)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching updated comment")
		return
	}
	if updatedAt.Valid {
		c.UpdatedAt = &updatedAt.Time
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"comment": c,
	})
}

// DELETE /api/admin/invoices/{id}/comments/{commentId}
func (s *Server) handleBOInvoiceCommentDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice id")
		return
	}

	commentID, err := strconv.ParseInt(chi.URLParam(r, "commentId"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid comment id")
		return
	}

	// Tenant ownership: same uniform outcome as Update.
	result, err := s.db.ExecContext(r.Context(), `
		DELETE FROM bo_invoice_comments
		WHERE id = ? AND invoice_id = ?
		  AND invoice_id IN (SELECT id FROM invoices WHERE restaurant_id = ?)
	`, commentID, invoiceID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting comment")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Comment not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}
