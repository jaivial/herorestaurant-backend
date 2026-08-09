package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

const (
	posPermissionView         = "pos.view"
	posPermissionSell         = "pos.sell"
	posPermissionVisitManage  = "pos.visit.manage"
	posPermissionLineVoid     = "pos.line.void"
	posPermissionDiscount     = "pos.discount"
	posPermissionCheckout     = "pos.checkout"
	posPermissionRefund       = "pos.refund"
	posPermissionRestock      = "pos.restock"
	posPermissionShiftManage  = "pos.shift.manage"
	posPermissionCatalog      = "pos.catalog.manage"
	posPermissionStockMapping = "pos.stock_mapping.manage"
	posPermissionCoversAdjust = "pos.covers.adjust"
	posPermissionReports      = "pos.reports.view"
	posPermissionSettings     = "pos.settings.manage"
	posPermissionKitchen      = "pos.kitchen.manage"
	posFeatureKey             = "pos_pack"
)

func (s *Server) checkPOSRateLimit(scope string, restaurantID, userID, limit int) bool {
	key := "pos:" + scope + ":" + strconv.Itoa(restaurantID) + ":" + strconv.Itoa(userID)
	now := time.Now().Unix()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	entry, ok := s.rateLimit[key]
	if !ok || now >= entry.windowEnd {
		s.rateLimit[key] = &rateLimitState{windowEnd: now + 60, tokens: limit - 1}
		return true
	}
	if entry.tokens <= 0 {
		return false
	}
	entry.tokens--
	return true
}

func withBOPOSTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) boPOSPermissionAllowed(ctx context.Context, a boAuth, permission string) (bool, error) {
	var allowed int
	err := s.db.QueryRowContext(ctx, `SELECT is_allowed FROM pos_role_permissions WHERE restaurant_id=? AND role_slug=? AND permission_key=? LIMIT 1`, a.ActiveRestaurantID, a.Role, permission).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return a.Role == "root" || a.Role == "admin", nil
	}
	return allowed != 0, err
}

func (s *Server) requireBOPOSPermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := boAuthFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			allowed, err := s.boPOSPermissionAllowed(r.Context(), a, permission)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error validating POS permission")
				return
			}
			if !allowed {
				httpx.WriteError(w, http.StatusForbidden, "Forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) requireBOPOSFeature(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := boAuthFromContext(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		active, err := s.hasActiveRecurringFeature(r.Context(), a.ActiveRestaurantID, posFeatureKey)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating POS plan")
			return
		}
		if !active && a.Role != "root" {
			httpx.WriteJSON(w, http.StatusPaymentRequired, map[string]any{"success": false, "message": "POS plan required", "code": "POS_PLAN_REQUIRED"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type posTotalLine struct {
	Quantity       float64
	UnitPriceCents int64
	DiscountCents  int64
	VATRate        float64
}

type posTotals struct {
	SubtotalGrossCents int64
	DiscountCents      int64
	SurchargeCents     int64
	TaxCents           int64
	TotalGrossCents    int64
}

// calculatePOSTotals keeps the discount-only signature used by existing callers.
func calculatePOSTotals(lines []posTotalLine, ticketDiscount int64) (posTotals, error) {
	return calculatePOSTotalsWithAdjustments(lines, ticketDiscount, 0)
}

// calculatePOSTotalsWithAdjustments applies the ticket discount first, then the
// surcharge, and recomputes VAT on the resulting gross so tax always matches
// what the customer actually pays.
func calculatePOSTotalsWithAdjustments(lines []posTotalLine, ticketDiscount, ticketSurcharge int64) (posTotals, error) {
	var out posTotals
	if ticketDiscount < 0 {
		return out, errors.New("invalid discount")
	}
	if ticketSurcharge < 0 {
		return out, errors.New("invalid surcharge")
	}
	lineNets := make([]int64, len(lines))
	for i, line := range lines {
		if line.Quantity <= 0 || line.UnitPriceCents < 0 || line.DiscountCents < 0 || line.VATRate < 0 || line.VATRate > 100 {
			return out, errors.New("invalid line")
		}
		gross := int64(math.Round(line.Quantity * float64(line.UnitPriceCents)))
		if line.DiscountCents > gross {
			return out, errors.New("line discount exceeds gross")
		}
		out.SubtotalGrossCents += gross
		out.DiscountCents += line.DiscountCents
		lineNets[i] = gross - line.DiscountCents
	}
	baseAfterLines := out.SubtotalGrossCents - out.DiscountCents
	if ticketDiscount > baseAfterLines {
		return out, errors.New("ticket discount exceeds gross")
	}
	out.DiscountCents += ticketDiscount
	out.SurchargeCents = ticketSurcharge
	out.TotalGrossCents = out.SubtotalGrossCents - out.DiscountCents + ticketSurcharge
	remainingDiscount := ticketDiscount
	remainingSurcharge := ticketSurcharge
	for i, line := range lines {
		allocated := int64(0)
		if ticketDiscount > 0 && baseAfterLines > 0 {
			if i == len(lines)-1 {
				allocated = remainingDiscount
			} else {
				allocated = int64(math.Round(float64(ticketDiscount) * float64(lineNets[i]) / float64(baseAfterLines)))
				if allocated > remainingDiscount {
					allocated = remainingDiscount
				}
			}
		}
		remainingDiscount -= allocated
		grossAfterDiscount := lineNets[i] - allocated
		surchargeShare := int64(0)
		if ticketSurcharge > 0 && baseAfterLines > 0 {
			if i == len(lines)-1 {
				surchargeShare = remainingSurcharge
			} else {
				surchargeShare = int64(math.Round(float64(ticketSurcharge) * float64(lineNets[i]) / float64(baseAfterLines)))
				if surchargeShare > remainingSurcharge {
					surchargeShare = remainingSurcharge
				}
			}
		}
		remainingSurcharge -= surchargeShare
		lineGross := grossAfterDiscount + surchargeShare
		net := int64(math.Round(float64(lineGross) / (1 + line.VATRate/100)))
		out.TaxCents += lineGross - net
	}
	return out, nil
}

type posServicePeriod struct {
	ServiceType string
	Start       string
	End         string
}

type posBusinessMoment struct {
	ServiceDate string
	ServiceType string
}

func normalizePOSDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

func posClockMinutes(value string) (int, error) {
	value = strings.TrimSpace(value)
	if len(value) >= 5 {
		value = value[:5]
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, err
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func resolvePOSBusinessMoment(at time.Time, timezone, cutoff string, periods []posServicePeriod) (posBusinessMoment, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return posBusinessMoment{}, err
	}
	local := at.In(location)
	cutoffMinutes, err := posClockMinutes(cutoff)
	if err != nil {
		return posBusinessMoment{}, err
	}
	minute := local.Hour()*60 + local.Minute()
	serviceDate := local
	if minute < cutoffMinutes {
		serviceDate = serviceDate.AddDate(0, 0, -1)
	}
	serviceType := "OTHER"
	for _, period := range periods {
		start, startErr := posClockMinutes(period.Start)
		end, endErr := posClockMinutes(period.End)
		if startErr != nil || endErr != nil {
			continue
		}
		matches := start <= end && minute >= start && minute < end || start > end && (minute >= start || minute < end)
		if matches {
			serviceType = period.ServiceType
			break
		}
	}
	return posBusinessMoment{ServiceDate: serviceDate.Format("2006-01-02"), ServiceType: serviceType}, nil
}

func posStockPlannedQuantity(soldQuantity, qtyBasePerSale float64) (float64, error) {
	if soldQuantity <= 0 || qtyBasePerSale <= 0 {
		return 0, errors.New("invalid stock quantity")
	}
	return soldQuantity * qtyBasePerSale, nil
}

type posCoverVisit struct {
	Covers      int
	Status      string
	Channel     string
	PaidTickets int
}

func aggregatePOSCovers(visits []posCoverVisit, adjustments int) int {
	total := adjustments
	for _, visit := range visits {
		if visit.Status == "CLOSED" && visit.Channel == "DINE_IN" {
			total += visit.Covers
		}
	}
	if total < 0 {
		return 0
	}
	return total
}

type posSettings struct {
	IsEnabled         bool   `json:"isEnabled"`
	StockMode         string `json:"stockMode"`
	CoversMode        string `json:"coversMode"`
	Timezone          string `json:"timezone"`
	BusinessDayCutoff string `json:"businessDayCutoff"`
	AutoCloseVisit    bool   `json:"autoCloseVisit"`
	RequireOpenShift  bool   `json:"requireOpenShift"`
	ReceiptPrefix     string `json:"receiptPrefix"`
}

// posRestaurantProfile is the issuer identity printed on POS documents such as
// the comanda ticket: branded name, fiscal ID, contact data and logo.
type posRestaurantProfile struct {
	Name    string `json:"name"`
	TaxID   string `json:"taxId"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
	Email   string `json:"email"`
	LogoURL string `json:"logoUrl"`
}

func (s *Server) loadPOSRestaurantProfile(ctx context.Context, restaurantID int) (posRestaurantProfile, error) {
	branding, err := s.loadRestaurantBranding(ctx, restaurantID)
	if err != nil {
		return posRestaurantProfile{}, err
	}
	info, err := s.loadRestaurantInfo(ctx, restaurantID)
	if err != nil {
		return posRestaurantProfile{}, err
	}
	out := posRestaurantProfile{
		Name:    branding.BrandName,
		TaxID:   info.CIF,
		Address: info.Direccion,
		Phone:   info.Telefono,
		Email:   info.Email,
		LogoURL: s.resolveInvoiceLogoURL(ctx, restaurantID, branding),
	}
	if out.Name == "" {
		if err = s.db.QueryRowContext(ctx, `SELECT name FROM restaurants WHERE id=?`, restaurantID).Scan(&out.Name); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	return out, nil
}

func defaultPOSSettings() posSettings {
	return posSettings{StockMode: "OFF", CoversMode: "MANUAL", Timezone: "Europe/Madrid", BusinessDayCutoff: "05:00", AutoCloseVisit: true, ReceiptPrefix: "TPV"}
}

func (s *Server) loadPOSSettings(ctx context.Context, restaurantID int) (posSettings, error) {
	out := defaultPOSSettings()
	var enabled, autoClose, requireShift int
	err := s.db.QueryRowContext(ctx, `SELECT is_enabled,stock_mode,covers_mode,timezone,TIME_FORMAT(business_day_cutoff,'%H:%i'),auto_close_visit,require_open_shift,receipt_prefix FROM pos_settings WHERE restaurant_id=?`, restaurantID).Scan(&enabled, &out.StockMode, &out.CoversMode, &out.Timezone, &out.BusinessDayCutoff, &autoClose, &requireShift, &out.ReceiptPrefix)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	out.IsEnabled, out.AutoCloseVisit, out.RequireOpenShift = enabled != 0, autoClose != 0, requireShift != 0
	return out, err
}

func validPOSMode(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (s *Server) handleBOPOSSettingsGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	settings, err := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS settings")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "settings": settings})
}

func (s *Server) handleBOPOSSettingsPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in posSettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid POS settings")
		return
	}
	in.StockMode, in.CoversMode = strings.ToUpper(strings.TrimSpace(in.StockMode)), strings.ToUpper(strings.TrimSpace(in.CoversMode))
	if !validPOSMode(in.StockMode, "OFF", "SHADOW", "LIVE") || !validPOSMode(in.CoversMode, "MANUAL", "SHADOW", "LIVE") || strings.TrimSpace(in.ReceiptPrefix) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid POS settings")
		return
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid timezone")
		return
	}
	if _, err := posClockMinutes(in.BusinessDayCutoff); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid business day cutoff")
		return
	}
	current, err := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading POS settings")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error saving POS settings")
		return
	}
	defer tx.Rollback()
	acceptances := []struct {
		needed              bool
		kind, code, message string
	}{{requiresPOSActivationAcceptance(current.StockMode, in.StockMode), "STOCK_LIVE", "STOCK_LIVE_ACCEPTANCE_REQUIRED", "Stock LIVE acceptance required"}, {requiresPOSActivationAcceptance(current.CoversMode, in.CoversMode), "COVERS_LIVE", "COVERS_LIVE_ACCEPTANCE_REQUIRED", "Covers LIVE acceptance required"}}
	consumed := []int64{}
	for _, acceptance := range acceptances {
		if !acceptance.needed {
			continue
		}
		var id int64
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_activation_acceptances WHERE restaurant_id=? AND acceptance_type=? AND consumed_at IS NULL ORDER BY accepted_at DESC LIMIT 1 FOR UPDATE`, a.ActiveRestaurantID, acceptance.kind).Scan(&id); err != nil {
			httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": acceptance.message, "code": acceptance.code})
			return
		}
		consumed = append(consumed, id)
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO pos_settings (restaurant_id,is_enabled,stock_mode,covers_mode,timezone,business_day_cutoff,auto_close_visit,require_open_shift,receipt_prefix) VALUES (?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE is_enabled=VALUES(is_enabled),stock_mode=VALUES(stock_mode),covers_mode=VALUES(covers_mode),timezone=VALUES(timezone),business_day_cutoff=VALUES(business_day_cutoff),auto_close_visit=VALUES(auto_close_visit),require_open_shift=VALUES(require_open_shift),receipt_prefix=VALUES(receipt_prefix)`, a.ActiveRestaurantID, stockBoolInt(in.IsEnabled), in.StockMode, in.CoversMode, in.Timezone, in.BusinessDayCutoff, stockBoolInt(in.AutoCloseVisit), stockBoolInt(in.RequireOpenShift), strings.TrimSpace(in.ReceiptPrefix))
	if err != nil {
		httpx.WriteError(w, 500, "Error saving POS settings")
		return
	}
	for _, id := range consumed {
		if _, err = tx.ExecContext(r.Context(), `UPDATE pos_activation_acceptances SET consumed_at=NOW() WHERE restaurant_id=? AND id=? AND consumed_at IS NULL`, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, 500, "Error consuming activation acceptance")
			return
		}
		_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'settings',?,'LIVE_ACTIVATED',JSON_OBJECT('stockMode',?,'coversMode',?),?)`, a.ActiveRestaurantID, id, in.StockMode, in.CoversMode, a.User.ID)
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error saving POS settings")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) loadPOSBusinessMoment(ctx context.Context, restaurantID int, settings posSettings) (posBusinessMoment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT service_type,TIME_FORMAT(start_time,'%H:%i'),TIME_FORMAT(end_time,'%H:%i') FROM pos_service_periods WHERE restaurant_id=? AND is_active=1 ORDER BY sort_order,id`, restaurantID)
	if err != nil {
		return posBusinessMoment{}, err
	}
	defer rows.Close()
	periods := []posServicePeriod{}
	for rows.Next() {
		var period posServicePeriod
		if err = rows.Scan(&period.ServiceType, &period.Start, &period.End); err != nil {
			return posBusinessMoment{}, err
		}
		periods = append(periods, period)
	}
	return resolvePOSBusinessMoment(time.Now(), settings.Timezone, settings.BusinessDayCutoff, periods)
}

func (s *Server) handleBOPOSBootstrap(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	// Without ?date= the POS shows live service: open visits, regardless of the
	// day they started on. With a date it shows that business day in full,
	// including its already-closed visits, so a past day can be reviewed.
	date, scoped, valid := posQueryDate(r)
	if !valid {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	settings, err := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS")
		return
	}
	restaurant, err := s.loadPOSRestaurantProfile(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS restaurant")
		return
	}
	products, err := s.loadPOSProducts(r.Context(), a.ActiveRestaurantID, true)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS products")
		return
	}
	// Occupancy follows the same scope as the visit list, so a past day never
	// shows tables as occupied by today's service.
	occupiedWhere := "v.status='OPEN'"
	tableArgs := []any{}
	if scoped {
		occupiedWhere = "v.status='OPEN' AND v.service_date=?"
		tableArgs = append(tableArgs, date)
	}
	tableArgs = append(tableArgs, a.ActiveRestaurantID)
	tableRows, err := s.db.QueryContext(r.Context(), `SELECT t.id,t.name,t.capacity,EXISTS(SELECT 1 FROM pos_visits v WHERE v.restaurant_id=t.restaurant_id AND v.table_id=t.id AND `+occupiedWhere+`),COALESCE(t.area_id,0),COALESCE(ra.name,'') FROM restaurant_tables t LEFT JOIN restaurant_areas ra ON ra.id=t.area_id AND ra.restaurant_id=t.restaurant_id WHERE t.restaurant_id=? AND t.is_active=1 ORDER BY ra.display_order,t.display_order,t.id`, tableArgs...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading tables")
		return
	}
	tables := []map[string]any{}
	// areas[] lets the Salón view group tables by room instead of one flat list.
	areaNames := map[int64]string{}
	areaOrder := []int64{}
	for tableRows.Next() {
		var id, areaID int64
		var name, areaName string
		var capacity, occupied int
		if err = tableRows.Scan(&id, &name, &capacity, &occupied, &areaID, &areaName); err != nil {
			tableRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading tables")
			return
		}
		if _, seen := areaNames[areaID]; !seen {
			areaNames[areaID] = areaName
			areaOrder = append(areaOrder, areaID)
		}
		tables = append(tables, map[string]any{"id": id, "name": name, "capacity": capacity, "occupied": occupied != 0, "areaId": areaID, "areaName": areaName})
	}
	tableRows.Close()
	areas := []map[string]any{}
	for _, areaID := range areaOrder {
		name := areaNames[areaID]
		if name == "" {
			name = "Sin salón"
		}
		areas = append(areas, map[string]any{"id": areaID, "name": name})
	}
	visitStatus := "OPEN"
	if scoped {
		visitStatus = ""
	}
	visits, err := s.loadPOSVisits(r.Context(), a.ActiveRestaurantID, visitStatus, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading visits")
		return
	}
	operatorRows, err := s.db.QueryContext(r.Context(), `SELECT id,TRIM(CONCAT(first_name,' ',last_name)) FROM restaurant_members WHERE restaurant_id=? AND is_active=1 ORDER BY first_name,last_name,id`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS operators")
		return
	}
	operators := []map[string]any{}
	for operatorRows.Next() {
		var id int64
		var name string
		if err = operatorRows.Scan(&id, &name); err != nil {
			operatorRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading POS operators")
			return
		}
		operators = append(operators, map[string]any{"id": id, "displayName": name, "isActive": true})
	}
	operatorRows.Close()
	var shiftID int64
	var terminal, shiftStatus string
	var opening int64
	var opened time.Time
	var currentShift any
	shiftErr := s.db.QueryRowContext(r.Context(), `SELECT id,terminal_key,status,opening_cash_cents,opened_at FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' ORDER BY opened_at DESC LIMIT 1`, a.ActiveRestaurantID).Scan(&shiftID, &terminal, &shiftStatus, &opening, &opened)
	if shiftErr == nil {
		currentShift = map[string]any{"id": shiftID, "terminalKey": terminal, "status": shiftStatus, "openingCashCents": opening, "openedAt": opened}
	} else if !errors.Is(shiftErr, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS shift")
		return
	}
	// The cash day of the viewed date travels with the bootstrap so the sell
	// screen knows on first paint whether the till is open and whether it should
	// render read-only, without a second round trip.
	businessDate, err := s.posResolveBusinessDate(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error resolving business date")
		return
	}
	var cashDay any
	day, cashDayErr := s.loadPOSCashDayByDate(r.Context(), s.db, a.ActiveRestaurantID, businessDate)
	if cashDayErr == nil {
		cashDay = day.asMap()
	} else if !errors.Is(cashDayErr, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash day")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "settings": settings, "restaurant": restaurant, "products": products, "tables": tables, "areas": areas, "visits": visits, "operators": operators, "currentShift": currentShift, "date": businessDate, "cashDay": cashDay})
}

func nextPOSTicketNumber(ctx context.Context, tx *sql.Tx, restaurantID int, businessDate, prefix string) (string, error) {
	_, err := tx.ExecContext(ctx, `INSERT INTO pos_daily_sequences (restaurant_id,business_date,sequence_type,next_value) VALUES (?,?,'TICKET',2) ON DUPLICATE KEY UPDATE next_value=LAST_INSERT_ID(next_value+1)`, restaurantID, businessDate)
	if err != nil {
		return "", err
	}
	var next int64
	if err = tx.QueryRowContext(ctx, `SELECT next_value-1 FROM pos_daily_sequences WHERE restaurant_id=? AND business_date=? AND sequence_type='TICKET' FOR UPDATE`, restaurantID, businessDate).Scan(&next); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%04d", prefix, strings.ReplaceAll(businessDate, "-", ""), next), nil
}

// loadPOSVisits lists visits for a restaurant. An empty date keeps the previous
// restaurant-wide behaviour; a date narrows the result to that business day so
// the POS can be viewed on a past date.
func (s *Server) loadPOSVisits(ctx context.Context, restaurantID int, status, date string) ([]map[string]any, error) {
	where := "v.restaurant_id=?"
	args := []any{restaurantID}
	if status != "" {
		where += " AND v.status=?"
		args = append(args, status)
	}
	if date != "" {
		where += " AND v.service_date=?"
		args = append(args, date)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.id,v.channel,v.table_id,COALESCE(t.name,''),v.service_date,v.service_type,v.covers,v.status,v.opened_at,v.version,COALESCE((SELECT SUM(total_gross_cents-refunded_cents) FROM pos_tickets p WHERE p.restaurant_id=v.restaurant_id AND p.visit_id=v.id AND p.status<>'VOIDED'),0),v.parked_at,COALESCE(v.parked_note,''),COALESCE(v.customer_name,''),COALESCE(v.customer_tax_id,''),COALESCE(v.customer_address,'') FROM pos_visits v LEFT JOIN restaurant_tables t ON t.id=v.table_id AND t.restaurant_id=v.restaurant_id WHERE `+where+` ORDER BY v.opened_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var channel, tableName, date, serviceType, statusValue string
		var tableID sql.NullInt64
		var covers, version int
		var opened time.Time
		var total int64
		var parkedAt sql.NullTime
		var parkedNote, customerName, customerTaxID, customerAddress string
		if err = rows.Scan(&id, &channel, &tableID, &tableName, &date, &serviceType, &covers, &statusValue, &opened, &version, &total, &parkedAt, &parkedNote, &customerName, &customerTaxID, &customerAddress); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "channel": channel, "tableId": stockNullableDBInt(tableID), "tableName": tableName, "serviceDate": normalizePOSDate(date), "serviceType": serviceType, "covers": covers, "status": statusValue, "openedAt": opened, "version": version, "totalGrossCents": total, "parked": parkedAt.Valid, "parkedNote": parkedNote, "customerName": customerName, "customerTaxId": customerTaxID, "customerAddress": customerAddress})
	}
	return out, rows.Err()
}

func (s *Server) handleBOPOSVisitsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	date, _, valid := posQueryDate(r)
	if !valid {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	visits, err := s.loadPOSVisits(r.Context(), a.ActiveRestaurantID, status, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS visits")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "visits": visits})
}

func (s *Server) handleBOPOSVisitCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Channel        string `json:"channel"`
		TableID        *int64 `json:"tableId"`
		BookingID      *int   `json:"bookingId"`
		Covers         int    `json:"covers"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid visit")
		return
	}
	in.Channel = strings.ToUpper(strings.TrimSpace(in.Channel))
	if in.Channel == "" {
		in.Channel = "DINE_IN"
	}
	// BAR is a tableless fast-sale channel: like TAKEAWAY it carries no covers,
	// so it never inflates the covers/affluence forecast.
	if strings.TrimSpace(in.IdempotencyKey) == "" || !validPOSMode(in.Channel, "DINE_IN", "TAKEAWAY", "DELIVERY", "BAR") || in.Channel == "DINE_IN" && (in.TableID == nil || *in.TableID <= 0 || in.Covers <= 0) || in.Channel != "DINE_IN" && (in.Covers != 0 || in.TableID != nil) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid visit")
		return
	}
	settings, err := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if err != nil || !settings.IsEnabled {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "POS is disabled", "code": "POS_DISABLED"})
		return
	}
	moment, err := s.loadPOSBusinessMoment(r.Context(), a.ActiveRestaurantID, settings)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error resolving POS service")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error opening visit")
		return
	}
	defer tx.Rollback()
	if in.TableID != nil {
		var table int
		if err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM restaurant_tables WHERE restaurant_id=? AND id=? AND is_active=1 FOR UPDATE`, a.ActiveRestaurantID, *in.TableID).Scan(&table); err != nil {
			httpx.WriteError(w, http.StatusNotFound, "Table not found")
			return
		}
	}
	if in.BookingID != nil {
		var booking int
		if err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM bookings WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, *in.BookingID).Scan(&booking); err != nil {
			httpx.WriteError(w, http.StatusNotFound, "Booking not found")
			return
		}
		var existingVisitID, existingTicketID int64
		var existingVisitStatus string
		if err = tx.QueryRowContext(r.Context(), `SELECT v.id,t.id,v.status FROM pos_visits v JOIN pos_tickets t ON t.restaurant_id=v.restaurant_id AND t.visit_id=v.id WHERE v.restaurant_id=? AND v.booking_id=? ORDER BY t.status='OPEN' DESC,t.id LIMIT 1`, a.ActiveRestaurantID, *in.BookingID).Scan(&existingVisitID, &existingTicketID, &existingVisitStatus); err == nil {
			tx.Rollback()
			if existingVisitStatus != "OPEN" {
				httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Reservation was already seated", "code": "RESERVATION_ALREADY_SEATED", "visitId": existingVisitID})
				return
			}
			ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, existingTicketID)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "visit": map[string]any{"id": existingVisitID}, "ticket": ticket})
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, 500, "Error checking reservation visit")
			return
		}
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO pos_visits (restaurant_id,channel,table_id,booking_id,service_date,service_type,covers,opened_by,open_idempotency_key) VALUES (?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, in.Channel, in.TableID, in.BookingID, moment.ServiceDate, moment.ServiceType, in.Covers, a.User.ID, in.IdempotencyKey)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			var existingVisitID, existingTicketID int64
			if duplicateErr := tx.QueryRowContext(r.Context(), `SELECT v.id,t.id FROM pos_visits v JOIN pos_tickets t ON t.restaurant_id=v.restaurant_id AND t.visit_id=v.id WHERE v.restaurant_id=? AND v.open_idempotency_key=? ORDER BY t.id LIMIT 1`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existingVisitID, &existingTicketID); duplicateErr == nil {
				tx.Rollback()
				ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, existingTicketID)
				httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "visit": map[string]any{"id": existingVisitID}, "ticket": ticket})
				return
			}
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Table is occupied", "code": "TABLE_OCCUPIED"})
			return
		}
		httpx.WriteError(w, http.StatusBadRequest, "Visit could not be opened")
		return
	}
	visitID, _ := res.LastInsertId()
	ticketNumber, err := nextPOSTicketNumber(r.Context(), tx, a.ActiveRestaurantID, moment.ServiceDate, settings.ReceiptPrefix)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating ticket")
		return
	}
	ticketRes, err := tx.ExecContext(r.Context(), `INSERT INTO pos_tickets (restaurant_id,visit_id,ticket_number,creation_idempotency_key,opened_by) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, visitID, ticketNumber, in.IdempotencyKey+":ticket", a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating ticket")
		return
	}
	ticketID, _ := ticketRes.LastInsertId()
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,actor_user_id) VALUES (?,'visit',?,'OPEN',?)`, a.ActiveRestaurantID, visitID, a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error opening visit")
		return
	}
	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "pos_visit_opened", map[string]any{"visitId": visitID, "tableId": in.TableID})
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "visit": map[string]any{"id": visitID, "channel": in.Channel, "tableId": in.TableID, "covers": in.Covers, "serviceDate": moment.ServiceDate, "serviceType": moment.ServiceType, "version": 1}, "ticket": map[string]any{"id": ticketID, "ticketNumber": ticketNumber, "version": 1, "lines": []any{}, "totalGrossCents": 0}})
}

func (s *Server) loadPOSTicket(ctx context.Context, restaurantID int, ticketID int64) (map[string]any, error) {
	var status, number string
	var version int
	var subtotal, discount, tax, total, paid, refunded int64
	var surcharge, tip int64
	var operator sql.NullInt64
	var ticketNote string
	err := s.db.QueryRowContext(ctx, `SELECT ticket_number,status,subtotal_gross_cents,discount_cents,tax_cents,total_gross_cents,paid_cents,refunded_cents,version,surcharge_cents,tip_cents,operator_member_id,COALESCE(note,'') FROM pos_tickets WHERE restaurant_id=? AND id=?`, restaurantID, ticketID).Scan(&number, &status, &subtotal, &discount, &tax, &total, &paid, &refunded, &version, &surcharge, &tip, &operator, &ticketNote)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,line_total_gross_cents,COALESCE(notes,''),status,comped_at,COALESCE(comp_reason,'') FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? ORDER BY id`, restaurantID, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []map[string]any{}
	for rows.Next() {
		var id int64
		var productID sql.NullInt64
		var name, notes, lineStatus string
		var quantity, vat float64
		var unitPrice, lineDiscount, lineTotal int64
		var compedAt sql.NullTime
		var compReason string
		if err = rows.Scan(&id, &productID, &name, &quantity, &unitPrice, &vat, &lineDiscount, &lineTotal, &notes, &lineStatus, &compedAt, &compReason); err != nil {
			return nil, err
		}
		tagRows, tagErr := s.db.QueryContext(ctx, `SELECT tag_id FROM pos_ticket_line_tags WHERE restaurant_id=? AND ticket_line_id=? ORDER BY tag_id`, restaurantID, id)
		if tagErr != nil {
			return nil, tagErr
		}
		tagIDs := []int64{}
		for tagRows.Next() {
			var tagID int64
			if tagErr = tagRows.Scan(&tagID); tagErr != nil {
				tagRows.Close()
				return nil, tagErr
			}
			tagIDs = append(tagIDs, tagID)
		}
		tagRows.Close()
		lines = append(lines, map[string]any{"id": id, "productId": stockNullableDBInt(productID), "productName": name, "quantity": quantity, "unitPriceGrossCents": unitPrice, "vatRate": vat, "discountCents": lineDiscount, "lineTotalGrossCents": lineTotal, "notes": notes, "status": lineStatus, "comped": compedAt.Valid, "compReason": compReason, "tagIds": tagIDs})
	}
	return map[string]any{"id": ticketID, "ticketNumber": number, "status": status, "subtotalGrossCents": subtotal, "discountCents": discount, "surchargeCents": surcharge, "tipCents": tip, "taxCents": tax, "totalGrossCents": total, "paidCents": paid, "refundedCents": refunded, "version": version, "operatorMemberId": stockNullableDBInt(operator), "note": ticketNote, "lines": lines}, rows.Err()
}

// recalculatePOSTicket recomputes the ticket money from its ACTIVE lines. The
// surcharge is read from the ticket row so callers that only change the discount
// cannot silently drop an applied Recargo.
func (s *Server) recalculatePOSTicket(ctx context.Context, tx *sql.Tx, restaurantID int, ticketID int64, ticketDiscount int64) (posTotals, error) {
	var surcharge int64
	if err := tx.QueryRowContext(ctx, `SELECT surcharge_cents FROM pos_tickets WHERE restaurant_id=? AND id=?`, restaurantID, ticketID).Scan(&surcharge); err != nil {
		return posTotals{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT quantity,unit_price_gross_cents,discount_cents,vat_rate_snapshot FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND status='ACTIVE' ORDER BY id`, restaurantID, ticketID)
	if err != nil {
		return posTotals{}, err
	}
	lines := []posTotalLine{}
	for rows.Next() {
		var line posTotalLine
		if err = rows.Scan(&line.Quantity, &line.UnitPriceCents, &line.DiscountCents, &line.VATRate); err != nil {
			rows.Close()
			return posTotals{}, err
		}
		lines = append(lines, line)
	}
	rows.Close()
	totals, err := calculatePOSTotalsWithAdjustments(lines, ticketDiscount, surcharge)
	if err != nil {
		return totals, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pos_tickets SET subtotal_gross_cents=?,discount_cents=?,ticket_discount_cents=?,tax_cents=?,total_gross_cents=?,version=version+1 WHERE restaurant_id=? AND id=?`, totals.SubtotalGrossCents, totals.DiscountCents, ticketDiscount, totals.TaxCents, totals.TotalGrossCents, restaurantID, ticketID)
	return totals, err
}

func (s *Server) handleBOPOSLineCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		ProductID              int64   `json:"productId"`
		Quantity               float64 `json:"quantity"`
		Notes                  string  `json:"notes"`
		IdempotencyKey         string  `json:"idempotencyKey"`
		UnitPriceOverrideCents *int64  `json:"unitPriceOverrideCents"`
	}
	if ticketID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.ProductID <= 0 || in.Quantity <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid ticket line")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error adding line")
		return
	}
	defer tx.Rollback()
	var status string
	var existingDiscount int64
	if err = tx.QueryRowContext(r.Context(), `SELECT status,ticket_discount_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&status, &existingDiscount); err != nil || status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	var name string
	var sku sql.NullString
	var price int64
	var vat float64
	if err = tx.QueryRowContext(r.Context(), `SELECT p.name,p.sku,p.price_gross_cents,COALESCE(v.rate,0) FROM pos_products p LEFT JOIN stock_vat_rates v ON v.restaurant_id=p.restaurant_id AND v.id=p.vat_rate_id WHERE p.restaurant_id=? AND p.id=? AND p.is_active=1 AND p.deleted_at IS NULL`, a.ActiveRestaurantID, in.ProductID).Scan(&name, &sku, &price, &vat); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "POS product not found")
		return
	}
	// Use price override if provided, otherwise use catalog price
	if in.UnitPriceOverrideCents != nil && *in.UnitPriceOverrideCents >= 0 {
		price = *in.UnitPriceOverrideCents
	}
	lineTotal := int64(math.Round(in.Quantity * float64(price)))
	lineRes, err := tx.ExecContext(r.Context(), `INSERT INTO pos_ticket_lines (restaurant_id,ticket_id,pos_product_id,product_name_snapshot,product_sku_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,notes,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, ticketID, in.ProductID, name, sku, in.Quantity, price, vat, lineTotal, stockNullableString(in.Notes), in.IdempotencyKey, a.User.ID)
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			httpx.WriteError(w, http.StatusBadRequest, "Ticket line could not be added")
			return
		}
	} else {
		lineID, _ := lineRes.LastInsertId()
		if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, ticketID, existingDiscount); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error calculating ticket")
			return
		}
		// Real-time stock deduction when stock_mode is LIVE
		settings, settingsErr := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
		if settingsErr == nil && settings.StockMode == "LIVE" {
			_, _ = s.deductStockForLine(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, ticketID, lineID, in.ProductID, in.Quantity, "pos-line-add:"+in.IdempotencyKey)
		}
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error adding line")
		return
	}
	ticket, err := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ticket")
		return
	}
	s.broadcastBOFichajeRevenue(a.ActiveRestaurantID, boTodayDate())
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "ticket": ticket})
}

func (s *Server) handleBOPOSLineVoid(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	lineID, _ := strconv.ParseInt(chi.URLParam(r, "lineId"), 10, 64)
	var in struct {
		Reason string `json:"reason"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Reason) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Void reason is required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error voiding line")
		return
	}
	defer tx.Rollback()
	var ticketStatus string
	var ticketDiscount int64
	if err = tx.QueryRowContext(r.Context(), `SELECT status,ticket_discount_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&ticketStatus, &ticketDiscount); err != nil || ticketStatus != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	// Real-time stock restoration when stock_mode is LIVE (before voiding the line)
	settings, settingsErr := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if settingsErr == nil && settings.StockMode == "LIVE" {
		_ = s.restoreStockForLine(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, lineID, "pos-line-void:"+strconv.FormatInt(ticketID, 10))
	}
	res, err := tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET status='VOIDED',void_reason=?,voided_by=?,voided_at=NOW() WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE'`, strings.TrimSpace(in.Reason), a.User.ID, a.ActiveRestaurantID, ticketID, lineID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error voiding line")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Ticket line not found")
		return
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, ticketID, ticketDiscount); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculating ticket")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error voiding line")
		return
	}
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	s.broadcastBOFichajeRevenue(a.ActiveRestaurantID, boTodayDate())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ticket": ticket})
}

func (s *Server) handleBOPOSDiscount(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		AmountCents int64  `json:"amountCents"`
		Reason      string `json:"reason"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.AmountCents < 0 || in.AmountCents > 100000000 || in.AmountCents > 0 && strings.TrimSpace(in.Reason) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid discount")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error applying discount")
		return
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(r.Context(), `SELECT status FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&status); err != nil || status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, ticketID, in.AmountCents); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket',?,'DISCOUNT',JSON_OBJECT('amountCents',?,'reason',?),?)`, a.ActiveRestaurantID, ticketID, in.AmountCents, strings.TrimSpace(in.Reason), a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error applying discount")
		return
	}
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	s.broadcastBOFichajeRevenue(a.ActiveRestaurantID, boTodayDate())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ticket": ticket})
}
