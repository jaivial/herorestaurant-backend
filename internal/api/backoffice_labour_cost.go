package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

type boMemberCompensation struct {
	ID                  int64    `json:"id"`
	PayType             string   `json:"payType"`
	GrossAmount         float64  `json:"grossAmount"`
	MonthlyHours        *float64 `json:"monthlyHours"`
	EmployerCostPct     float64  `json:"employerCostPct"`
	EffectiveHourlyCost float64  `json:"effectiveHourlyCost"`
	EffectiveFrom       string   `json:"effectiveFrom"`
	EffectiveTo         *string  `json:"effectiveTo"`
	Notes               *string  `json:"notes"`
}

type boMemberCompensationInput struct {
	PayType         string   `json:"payType"`
	GrossAmount     float64  `json:"grossAmount"`
	MonthlyHours    *float64 `json:"monthlyHours"`
	EmployerCostPct float64  `json:"employerCostPct"`
	EffectiveFrom   string   `json:"effectiveFrom"`
	EffectiveTo     *string  `json:"effectiveTo"`
	Notes           *string  `json:"notes"`
}

func effectiveHourlyCost(payType string, grossAmount, monthlyHours, employerCostPct float64) (float64, error) {
	if grossAmount < 0 || employerCostPct < 0 || employerCostPct > 300 {
		return 0, errors.New("invalid compensation")
	}
	base := grossAmount
	switch strings.ToUpper(strings.TrimSpace(payType)) {
	case "MONTHLY":
		if monthlyHours <= 0 {
			return 0, errors.New("monthly hours required")
		}
		base /= monthlyHours
	case "HOURLY":
	default:
		return 0, errors.New("invalid pay type")
	}
	return math.Round(base*(1+employerCostPct/100)*10000) / 10000, nil
}

func compensationRangesOverlap(from time.Time, to *time.Time, otherFrom time.Time, otherTo *time.Time) bool {
	return (to == nil || !to.Before(otherFrom)) && (otherTo == nil || !otherTo.Before(from))
}

func parseCompensationInput(in boMemberCompensationInput) (boMemberCompensationInput, time.Time, *time.Time, error) {
	in.PayType = strings.ToUpper(strings.TrimSpace(in.PayType))
	in.EffectiveFrom = strings.TrimSpace(in.EffectiveFrom)
	from, err := parseBODate(in.EffectiveFrom)
	if err != nil {
		return in, time.Time{}, nil, errors.New("invalid effectiveFrom")
	}
	var to *time.Time
	if in.EffectiveTo != nil && strings.TrimSpace(*in.EffectiveTo) != "" {
		value, err := parseBODate(*in.EffectiveTo)
		if err != nil || value.Before(from) {
			return in, time.Time{}, nil, errors.New("invalid effectiveTo")
		}
		to = &value
	}
	monthlyHours := 0.0
	if in.MonthlyHours != nil {
		monthlyHours = *in.MonthlyHours
	}
	if _, err := effectiveHourlyCost(in.PayType, in.GrossAmount, monthlyHours, in.EmployerCostPct); err != nil {
		return in, time.Time{}, nil, err
	}
	return in, from, to, nil
}

func scanMemberCompensation(scanner boMemberScanner) (boMemberCompensation, error) {
	var item boMemberCompensation
	var monthly sql.NullFloat64
	var from time.Time
	var to sql.NullTime
	var notes sql.NullString
	if err := scanner.Scan(&item.ID, &item.PayType, &item.GrossAmount, &monthly, &item.EmployerCostPct, &from, &to, &notes); err != nil {
		return item, err
	}
	monthlyValue := 0.0
	if monthly.Valid {
		monthlyValue = monthly.Float64
		item.MonthlyHours = &monthlyValue
	}
	item.EffectiveHourlyCost, _ = effectiveHourlyCost(item.PayType, item.GrossAmount, monthlyValue, item.EmployerCostPct)
	item.EffectiveFrom = from.Format("2006-01-02")
	if to.Valid {
		value := to.Time.Format("2006-01-02")
		item.EffectiveTo = &value
	}
	item.Notes = nullStringPtr(notes)
	return item, nil
}

func (s *Server) handleBOMemberCompensationsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "id invalido")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,pay_type,gross_amount,monthly_hours,employer_cost_pct,effective_from,effective_to,notes FROM member_compensations WHERE restaurant_id=? AND restaurant_member_id=? AND deleted_at IS NULL ORDER BY effective_from DESC,id DESC`, a.ActiveRestaurantID, memberID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo salarios")
		return
	}
	defer rows.Close()
	items := []boMemberCompensation{}
	for rows.Next() {
		item, err := scanMemberCompensation(rows)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo salarios")
			return
		}
		items = append(items, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
}

func compensationOverlaps(ctx context.Context, q boMemberScannerQueryer, restaurantID, memberID int, excludeID int64, from time.Time, to *time.Time) (bool, error) {
	var exists int
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM member_compensations WHERE restaurant_id=? AND restaurant_member_id=? AND deleted_at IS NULL AND id<>? AND effective_from<=COALESCE(?, '9999-12-31') AND COALESCE(effective_to,'9999-12-31')>=?)`, restaurantID, memberID, excludeID, to, from).Scan(&exists)
	return exists != 0, err
}

type boMemberScannerQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func compensationSnapshot(item boMemberCompensationInput, memberID int) []byte {
	raw, _ := json.Marshal(map[string]any{"restaurantMemberId": memberID, "payType": item.PayType, "grossAmount": item.GrossAmount, "monthlyHours": item.MonthlyHours, "employerCostPct": item.EmployerCostPct, "effectiveFrom": item.EffectiveFrom, "effectiveTo": item.EffectiveTo, "notes": item.Notes})
	return raw
}

func (s *Server) saveBOMemberCompensation(w http.ResponseWriter, r *http.Request, id int64) {
	a, _ := boAuthFromContext(r.Context())
	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "id invalido")
		return
	}
	var in boMemberCompensationInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	in, from, to, err := parseCompensationInput(in)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando salario")
		return
	}
	defer tx.Rollback()
	var lockedMemberID int
	if err = tx.QueryRowContext(r.Context(), `SELECT id FROM restaurant_members WHERE restaurant_id=? AND id=? AND is_active=1 FOR UPDATE`, a.ActiveRestaurantID, memberID).Scan(&lockedMemberID); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Miembro no encontrado")
		return
	}
	overlap, err := compensationOverlaps(r.Context(), tx, a.ActiveRestaurantID, memberID, id, from, to)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando salario")
		return
	}
	if overlap {
		httpx.WriteError(w, http.StatusConflict, "El periodo salarial se solapa con otro")
		return
	}
	monthly := any(nil)
	if in.MonthlyHours != nil {
		monthly = *in.MonthlyHours
	}
	toValue := any(nil)
	if to != nil {
		toValue = to.Format("2006-01-02")
	}
	action := "CREATE"
	if id == 0 {
		res, err := tx.ExecContext(r.Context(), `INSERT INTO member_compensations (restaurant_id,restaurant_member_id,pay_type,gross_amount,monthly_hours,employer_cost_pct,effective_from,effective_to,notes,created_by) VALUES (?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, memberID, in.PayType, in.GrossAmount, monthly, in.EmployerCostPct, from.Format("2006-01-02"), toValue, in.Notes, a.User.ID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "No se pudo guardar salario")
			return
		}
		id, _ = res.LastInsertId()
	} else {
		action = "UPDATE"
		res, err := tx.ExecContext(r.Context(), `UPDATE member_compensations SET pay_type=?,gross_amount=?,monthly_hours=?,employer_cost_pct=?,effective_from=?,effective_to=?,notes=? WHERE id=? AND restaurant_id=? AND restaurant_member_id=? AND deleted_at IS NULL`, in.PayType, in.GrossAmount, monthly, in.EmployerCostPct, from.Format("2006-01-02"), toValue, in.Notes, id, a.ActiveRestaurantID, memberID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "No se pudo guardar salario")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			httpx.WriteError(w, http.StatusNotFound, "Salario no encontrado")
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO member_compensation_audit (restaurant_id,compensation_id,action,snapshot,actor_user_id) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, id, action, compensationSnapshot(in, memberID), a.User.ID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error auditando salario")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando salario")
		return
	}
	status := http.StatusOK
	if action == "CREATE" {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"success": true, "id": id})
}
func (s *Server) handleBOMemberCompensationCreate(w http.ResponseWriter, r *http.Request) {
	s.saveBOMemberCompensation(w, r, 0)
}
func (s *Server) handleBOMemberCompensationPatch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "compensationId"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "id invalido")
		return
	}
	s.saveBOMemberCompensation(w, r, id)
}
func (s *Server) handleBOMemberCompensationDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	memberID, _ := parseBOIDParam(r, "id")
	id, _ := strconv.ParseInt(chi.URLParam(r, "compensationId"), 10, 64)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando salario")
		return
	}
	defer tx.Rollback()
	var snapshot []byte
	if err = tx.QueryRowContext(r.Context(), `SELECT JSON_OBJECT('restaurantMemberId',restaurant_member_id,'payType',pay_type,'grossAmount',gross_amount,'monthlyHours',monthly_hours,'employerCostPct',employer_cost_pct,'effectiveFrom',effective_from,'effectiveTo',effective_to,'notes',notes) FROM member_compensations WHERE restaurant_id=? AND restaurant_member_id=? AND id=? AND deleted_at IS NULL FOR UPDATE`, a.ActiveRestaurantID, memberID, id).Scan(&snapshot); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Salario no encontrado")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE member_compensations SET deleted_at=NOW() WHERE restaurant_id=? AND restaurant_member_id=? AND id=?`, a.ActiveRestaurantID, memberID, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando salario")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO member_compensation_audit (restaurant_id,compensation_id,action,snapshot,actor_user_id) VALUES (?,?,'DELETE',?,?)`, a.ActiveRestaurantID, id, snapshot, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error auditando salario")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando salario")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOFichajeLabourCost(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from, err := parseBODate(r.URL.Query().Get("from"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "from invalido")
		return
	}
	to, err := parseBODate(r.URL.Query().Get("to"))
	if err != nil || to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		httpx.WriteError(w, http.StatusBadRequest, "to invalido")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT e.restaurant_member_id,CONCAT(m.first_name,' ',m.last_name),SUM(e.minutes_worked),SUM(CASE WHEN c.id IS NULL THEN 0 ELSE e.minutes_worked/60*(CASE c.pay_type WHEN 'MONTHLY' THEN c.gross_amount/c.monthly_hours ELSE c.gross_amount END)*(1+c.employer_cost_pct/100) END),MAX(c.id IS NULL) FROM member_time_entries e JOIN restaurant_members m ON m.restaurant_id=e.restaurant_id AND m.id=e.restaurant_member_id LEFT JOIN member_compensations c ON c.restaurant_id=e.restaurant_id AND c.restaurant_member_id=e.restaurant_member_id AND c.deleted_at IS NULL AND e.work_date BETWEEN c.effective_from AND COALESCE(c.effective_to,'9999-12-31') WHERE e.restaurant_id=? AND e.work_date BETWEEN ? AND ? GROUP BY e.restaurant_member_id,m.first_name,m.last_name ORDER BY m.last_name,m.first_name`, a.ActiveRestaurantID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando coste laboral")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	missing := []string{}
	totalMinutes := 0
	totalCost := 0.0
	for rows.Next() {
		var id, minutes, missingFlag int
		var name string
		var cost float64
		if err = rows.Scan(&id, &name, &minutes, &cost, &missingFlag); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo coste laboral")
			return
		}
		totalMinutes += minutes
		totalCost += cost
		if missingFlag != 0 {
			missing = append(missing, name)
		}
		items = append(items, map[string]any{"memberId": id, "name": name, "minutesWorked": minutes, "cost": round2(cost), "missingCompensation": missingFlag != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "totalMinutes": totalMinutes, "totalCost": round2(totalCost), "missingCompensationMembers": missing, "members": items})
}

func loadStockRecipeLabourCosts(ctx context.Context, tx *sql.Tx, restaurantID int, effectiveDate time.Time) (map[int64]float64, map[int64][]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT l.recipe_id,l.minutes_per_batch,CONCAT(m.first_name,' ',m.last_name),c.pay_type,c.gross_amount,c.monthly_hours,c.employer_cost_pct FROM stock_recipe_labour l JOIN restaurant_members m ON m.restaurant_id=l.restaurant_id AND m.id=l.restaurant_member_id LEFT JOIN member_compensations c ON c.restaurant_id=l.restaurant_id AND c.restaurant_member_id=l.restaurant_member_id AND c.deleted_at IS NULL AND ? BETWEEN c.effective_from AND COALESCE(c.effective_to,'9999-12-31') WHERE l.restaurant_id=?`, effectiveDate.Format("2006-01-02"), restaurantID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	direct := map[int64]float64{}
	missing := map[int64][]string{}
	for rows.Next() {
		var recipeID int64
		var minutes float64
		var name string
		var payType sql.NullString
		var gross, monthly, burden sql.NullFloat64
		if err := rows.Scan(&recipeID, &minutes, &name, &payType, &gross, &monthly, &burden); err != nil {
			return nil, nil, err
		}
		if !payType.Valid {
			missing[recipeID] = append(missing[recipeID], name)
			continue
		}
		rate, err := effectiveHourlyCost(payType.String, gross.Float64, monthly.Float64, burden.Float64)
		if err != nil {
			missing[recipeID] = append(missing[recipeID], name)
			continue
		}
		direct[recipeID] += minutes / 60 * rate
	}
	return direct, missing, rows.Err()
}

func (s *Server) handleBOStockLabourMembers(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	today := boTodayDate()
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.id,CONCAT(m.first_name,' ',m.last_name),c.pay_type,c.gross_amount,c.monthly_hours,c.employer_cost_pct FROM restaurant_members m LEFT JOIN member_compensations c ON c.restaurant_id=m.restaurant_id AND c.restaurant_member_id=m.id AND c.deleted_at IS NULL AND ? BETWEEN c.effective_from AND COALESCE(c.effective_to,'9999-12-31') WHERE m.restaurant_id=? AND m.is_active=1 ORDER BY m.last_name,m.first_name`, today.Format("2006-01-02"), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading labour members")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int
		var name string
		var payType sql.NullString
		var gross, monthly, burden sql.NullFloat64
		if err = rows.Scan(&id, &name, &payType, &gross, &monthly, &burden); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading labour members")
			return
		}
		available := payType.Valid
		if available {
			_, err = effectiveHourlyCost(payType.String, gross.Float64, monthly.Float64, burden.Float64)
			available = err == nil
		}
		items = append(items, map[string]any{"id": id, "name": name, "costAvailable": available})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
}

func stockRecipeLabourCost(recipeID int64, recipes map[int64]stockBOMRecipe, directCosts map[int64]float64, missingByRecipe map[int64][]string) (float64, []string, error) {
	missingSet := map[string]bool{}
	visiting := map[int64]bool{}
	var cost func(int64, float64) (float64, error)
	cost = func(id int64, multiplier float64) (float64, error) {
		recipe, ok := recipes[id]
		if !ok || recipe.OutputQty <= 0 {
			return 0, errors.New("recipe not found")
		}
		if visiting[id] {
			return 0, errors.New("recipe cycle detected")
		}
		visiting[id] = true
		defer delete(visiting, id)
		total := directCosts[id] * multiplier
		for _, name := range missingByRecipe[id] {
			missingSet[name] = true
		}
		for _, component := range recipe.Components {
			if component.SubRecipeID == nil {
				continue
			}
			sub := recipes[*component.SubRecipeID]
			needed, err := stockProductionRequirement(component.QtyBase, multiplier, component.WastePct)
			if err != nil {
				return 0, err
			}
			nested, err := cost(*component.SubRecipeID, needed/sub.OutputQty)
			if err != nil {
				return 0, err
			}
			total += nested
		}
		return total, nil
	}
	total, err := cost(recipeID, 1)
	if err != nil {
		return 0, nil, err
	}
	recipe := recipes[recipeID]
	names := make([]string, 0, len(missingSet))
	for name := range missingSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return total / recipe.OutputQty, names, nil
}
