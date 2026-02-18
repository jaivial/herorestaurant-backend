package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Table represents a restaurant table
type Table struct {
	ID           int     `json:"id"`
	RestaurantID int     `json:"restaurantId"`
	NumeroMesa   int     `json:"numeroMesa"`
	Name         string  `json:"name"`
	Shape        string  `json:"shape"`
	Capacity     int     `json:"capacity"`
	PositionX    float64 `json:"positionX"`
	PositionY    float64 `json:"positionY"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	Color        string  `json:"color"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

// CreateTableInput represents input for creating a table
type CreateTableInput struct {
	NumeroMesa int     `json:"numeroMesa"`
	Name       string  `json:"name"`
	Shape      string  `json:"shape"`
	Capacity   int     `json:"capacity"`
	PositionX  float64 `json:"positionX"`
	PositionY  float64 `json:"positionY"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	Color      string  `json:"color"`
}

// UpdateTableInput represents input for updating a table
type UpdateTableInput struct {
	NumeroMesa *int     `json:"numeroMesa"`
	Name       *string  `json:"name"`
	Shape      *string  `json:"shape"`
	Capacity   *int     `json:"capacity"`
	PositionX  *float64 `json:"positionX"`
	PositionY  *float64 `json:"positionY"`
	Width      *float64 `json:"width"`
	Height     *float64 `json:"height"`
	Color      *string  `json:"color"`
}

func (s *Server) handleBOTablesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurantID := a.ActiveRestaurantID

	q := `
		SELECT id, restaurant_id, numero_mesa, name, shape, capacity,
		       position_x, position_y, width, height, color, status,
		       created_at, updated_at
		FROM restaurant_tables
		WHERE restaurant_id = ?
		ORDER BY numero_mesa ASC
	`

	rows, err := s.db.QueryContext(r.Context(), q, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to fetch tables")
		return
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var t Table
		var name sql.NullString
		var color sql.NullString

		err := rows.Scan(
			&t.ID, &t.RestaurantID, &t.NumeroMesa, &name, &t.Shape, &t.Capacity,
			&t.PositionX, &t.PositionY, &t.Width, &t.Height, &color, &t.Status,
			&t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			continue
		}

		t.Name = name.String
		t.Color = color.String
		tables = append(tables, t)
	}

	if tables == nil {
		tables = []Table{}
	}

	httpx.WriteJSON(w, http.StatusOK, tables)
}

func (s *Server) handleBOTableGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	tableID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid table ID")
		return
	}

	restaurantID := a.ActiveRestaurantID

	q := `
		SELECT id, restaurant_id, numero_mesa, name, shape, capacity,
		       position_x, position_y, width, height, color, status,
		       created_at, updated_at
		FROM restaurant_tables
		WHERE id = ? AND restaurant_id = ?
	`

	var t Table
	var name sql.NullString
	var color sql.NullString

	err = s.db.QueryRowContext(r.Context(), q, tableID, restaurantID).Scan(
		&t.ID, &t.RestaurantID, &t.NumeroMesa, &name, &t.Shape, &t.Capacity,
		&t.PositionX, &t.PositionY, &t.Width, &t.Height, &color, &t.Status,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Table not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to fetch table")
		return
	}

	t.Name = name.String
	t.Color = color.String

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Server) handleBOTableCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var input CreateTableInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if input.NumeroMesa <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "numero_mesa is required")
		return
	}

	if input.Shape != "round" && input.Shape != "square" {
		input.Shape = "round"
	}

	if input.Capacity <= 0 {
		input.Capacity = 4
	}

	if input.Width <= 0 {
		input.Width = 80
	}

	if input.Height <= 0 {
		input.Height = 80
	}

	restaurantID := a.ActiveRestaurantID

	// Check if table number already exists
	var exists bool
	err := s.db.QueryRowContext(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM restaurant_tables WHERE restaurant_id = ? AND numero_mesa = ?)",
		restaurantID, input.NumeroMesa,
	).Scan(&exists)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to check table")
		return
	}

	if exists {
		httpx.WriteError(w, http.StatusConflict, "Table number already exists")
		return
	}

	q := `
		INSERT INTO restaurant_tables
		    (restaurant_id, numero_mesa, name, shape, capacity, position_x, position_y, width, height, color)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.ExecContext(r.Context(), q,
		restaurantID, input.NumeroMesa, input.Name, input.Shape, input.Capacity,
		input.PositionX, input.PositionY, input.Width, input.Height, input.Color,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to create table")
		return
	}

	id, _ := result.LastInsertId()

	// Return the created table
	s.handleBOTableGet(w, r.WithContext(context.WithValue(r.Context(), "tableID", int(id))))
}

func (s *Server) handleBOTableUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	tableID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid table ID")
		return
	}

	var input UpdateTableInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	restaurantID := a.ActiveRestaurantID

	// Check if table exists
	var exists bool
	err = s.db.QueryRowContext(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM restaurant_tables WHERE id = ? AND restaurant_id = ?)",
		tableID, restaurantID,
	).Scan(&exists)

	if err != nil || !exists {
		httpx.WriteError(w, http.StatusNotFound, "Table not found")
		return
	}

	// Build update query dynamically
	setClauses := []string{}
	args := []any{}

	if input.NumeroMesa != nil {
		setClauses = append(setClauses, "numero_mesa = ?")
		args = append(args, *input.NumeroMesa)
	}
	if input.Name != nil {
		setClauses = append(setClauses, "name = ?")
		args = append(args, *input.Name)
	}
	if input.Shape != nil {
		setClauses = append(setClauses, "shape = ?")
		args = append(args, *input.Shape)
	}
	if input.Capacity != nil {
		setClauses = append(setClauses, "capacity = ?")
		args = append(args, *input.Capacity)
	}
	if input.PositionX != nil {
		setClauses = append(setClauses, "position_x = ?")
		args = append(args, *input.PositionX)
	}
	if input.PositionY != nil {
		setClauses = append(setClauses, "position_y = ?")
		args = append(args, *input.PositionY)
	}
	if input.Width != nil {
		setClauses = append(setClauses, "width = ?")
		args = append(args, *input.Width)
	}
	if input.Height != nil {
		setClauses = append(setClauses, "height = ?")
		args = append(args, *input.Height)
	}
	if input.Color != nil {
		setClauses = append(setClauses, "color = ?")
		args = append(args, *input.Color)
	}

	if len(setClauses) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "No fields to update")
		return
	}

	args = append(args, tableID, restaurantID)

	q := "UPDATE restaurant_tables SET " + joinStrings(", ", setClauses...) + " WHERE id = ? AND restaurant_id = ?"

	_, err = s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to update table")
		return
	}

	// Return the updated table
	s.handleBOTableGet(w, r)
}

func (s *Server) handleBOTableDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	tableID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid table ID")
		return
	}

	restaurantID := a.ActiveRestaurantID

	result, err := s.db.ExecContext(r.Context(),
		"DELETE FROM restaurant_tables WHERE id = ? AND restaurant_id = ?",
		tableID, restaurantID,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to delete table")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Table not found")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// handleBOTableAutoAssign positions tables based on their numero_mesa value
// in a grid pattern
func (s *Server) handleBOTableAutoAssign(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurantID := a.ActiveRestaurantID

	// Get all tables ordered by numero_mesa
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, numero_mesa FROM restaurant_tables WHERE restaurant_id = ? ORDER BY numero_mesa ASC`,
		restaurantID,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to fetch tables")
		return
	}
	defer rows.Close()

	type tablePos struct {
		id         int
		numeroMesa int
	}

	var tables []tablePos
	for rows.Next() {
		var tp tablePos
		if err := rows.Scan(&tp.id, &tp.numeroMesa); err == nil {
			tables = append(tables, tp)
		}
	}

	if len(tables) == 0 {
		httpx.WriteJSON(w, http.StatusOK, []Table{})
		return
	}

	// Grid layout parameters
	const (
		cellWidth  = 150
		cellHeight = 150
		startX     = 50
		startY     = 50
		cols       = 6 // tables per row
	)

	// Update positions
	for i, tp := range tables {
		row := i / cols
		col := i % cols

		x := startX + col*cellWidth
		y := startY + row*cellHeight

		_, err := s.db.ExecContext(r.Context(),
			`UPDATE restaurant_tables SET position_x = ?, position_y = ? WHERE id = ?`,
			x, y, tp.id,
		)
		if err != nil {
			continue
		}
	}

	// Return updated tables
	s.handleBOTablesList(w, r)
}

func joinStrings(sep string, parts ...string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
