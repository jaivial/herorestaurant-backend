package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetValidMenusForPartySize(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid request method. Only GET is allowed.",
		})
		return
	}

	rawPartySize := strings.TrimSpace(r.URL.Query().Get("party_size"))
	if rawPartySize == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "party_size parameter is required",
		})
		return
	}
	partySize, err := strconv.Atoi(rawPartySize)
	if err != nil || partySize < 1 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "party_size must be a positive integer",
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, price, included_coffee, menu_subtitle,
		       entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, created_at
		FROM menusDeGrupos
		WHERE restaurant_id = ? AND active = 1 AND min_party_size <= ?
		ORDER BY min_party_size ASC, price ASC
	`, restaurantID, partySize)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}
	defer rows.Close()

	type PrincipalesFallback struct {
		Titulo string `json:"titulo_principales"`
		Items  []any  `json:"items"`
	}
	type BeverageFallback struct {
		Type          string  `json:"type"`
		PricePerPerson *float64 `json:"price_per_person"`
	}

	type menuOut struct {
		ID                    int             `json:"id"`
		MenuTitle             string          `json:"menu_title"`
		Price                 float64         `json:"price"`
		MinPartySize          int             `json:"min_party_size"`
		MainDishesLimit       bool            `json:"main_dishes_limit"`
		MainDishesLimitNumber int             `json:"main_dishes_limit_number"`
		IncludedCoffee        bool            `json:"included_coffee"`
		MenuSubtitle          any             `json:"menu_subtitle"`
		Entrantes             any             `json:"entrantes"`
		Principales           any             `json:"principales"`
		Postre                any             `json:"postre"`
		Beverage              any             `json:"beverage"`
		Comments              any             `json:"comments"`
		CreatedAt             string          `json:"created_at"`
	}

	var menus []menuOut
	for rows.Next() {
		var (
			id                    int
			menuTitle             string
			price                 float64
			includedCoffeeInt     int
			menuSubtitleRaw       sql.NullString
			entrantesRaw          sql.NullString
			principalesRaw        sql.NullString
			postreRaw             sql.NullString
			beverageRaw           sql.NullString
			commentsRaw           sql.NullString
			minPartySize          int
			mainDishesLimitInt    int
			mainDishesLimitNumber int
			createdAt             time.Time
		)
		if err := rows.Scan(
			&id,
			&menuTitle,
			&price,
			&includedCoffeeInt,
			&menuSubtitleRaw,
			&entrantesRaw,
			&principalesRaw,
			&postreRaw,
			&beverageRaw,
			&commentsRaw,
			&minPartySize,
			&mainDishesLimitInt,
			&mainDishesLimitNumber,
			&createdAt,
		); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Server error: " + err.Error(),
			})
			return
		}

		decodeOr := func(raw sql.NullString, fallback any) any {
			if !raw.Valid || strings.TrimSpace(raw.String) == "" {
				return fallback
			}
			var v any
			if err := json.Unmarshal([]byte(raw.String), &v); err != nil {
				return fallback
			}
			if v == nil {
				return fallback
			}
			return v
		}

		principalesFallback := decodeOr(principalesRaw, PrincipalesFallback{
			Titulo: "Principal a elegir",
			Items:  []any{},
		})
		beverageFallback := decodeOr(beverageRaw, BeverageFallback{
			Type:           "no_incluida",
			PricePerPerson: nil,
		})

		menu := menuOut{
			ID:                    id,
			MenuTitle:             menuTitle,
			Price:                 price,
			MinPartySize:          minPartySize,
			MainDishesLimit:       mainDishesLimitInt != 0,
			MainDishesLimitNumber: mainDishesLimitNumber,
			IncludedCoffee:        includedCoffeeInt != 0,
			MenuSubtitle:          decodeOr(menuSubtitleRaw, []any{}),
			Entrantes:             decodeOr(entrantesRaw, []any{}),
			Principales:           principalesFallback,
			Postre:                decodeOr(postreRaw, []any{}),
			Beverage:              beverageFallback,
			Comments:              decodeOr(commentsRaw, []any{}),
			CreatedAt:             createdAt.Format("2006-01-02 15:04:05"),
		}
		menus = append(menus, menu)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"hasValidMenus": len(menus) > 0,
		"count":         len(menus),
		"menus":         menus,
	})
}
