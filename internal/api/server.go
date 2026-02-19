package api

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/httpx"
)

type Server struct {
	db          *sql.DB
	cfg         config.Config
	tenantCache tenantDomainCache
	fichajeHub  *boFichajeHub
}

func NewServer(db *sql.DB, cfg config.Config) *Server {
	s := &Server{
		db:         db,
		cfg:        cfg,
		fichajeHub: newBOFichajeHub(),
	}
	go s.runBOFichajeAutoCutLoop()
	return s
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()

	// CORS / preflight: some legacy clients rely on OPTIONS support.
	r.Options("/*", func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		allowed := "*"
		if s.cfg.CORSAllowOrigins != "" {
			allowed = s.cfg.CORSAllowOrigins
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowed)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token, X-Api-Token")
		w.WriteHeader(http.StatusNoContent)
	})

	// Backoffice (new React SSR dashboard).
	// Strip /api prefix for /api/admin/* routes to make them work with /admin handlers
	r.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/admin") {
				r.URL.Path = strings.Replace(r.URL.Path, "/api/admin", "/admin", 1)
			}
			next.ServeHTTP(w, r)
		})
	}).Route("/api/admin", func(r chi.Router) {
		// No routes needed - the middleware rewrites to /admin
	})

	r.Route("/admin", func(r chi.Router) {
		reservasGate := s.requireBOSection(boSectionReservas)
		menusGate := s.requireBOSection(boSectionMenus)
		ajustesGate := s.requireBOSection(boSectionAjustes)
		miembrosGate := s.requireBOSection(boSectionMiembros)
		fichajeGate := s.requireBOSection(boSectionFichaje)
		horariosGate := s.requireBOSection(boSectionHorarios)
		facturasGate := s.requireBOSection(boSectionFacturas)
		rolesAdminGate := s.requireBORoleImportanceAtLeast(90)

		r.Post("/login", s.handleBOLogin)
		r.Post("/logout", s.handleBOLogout)
		r.Post("/invitations/validate", s.handleBOInvitationValidate)
		r.Post("/invitations/onboarding/start", s.handleBOInvitationOnboardingStart)
		r.Get("/invitations/onboarding/{guid}", s.handleBOInvitationOnboardingGet)
		r.Post("/invitations/onboarding/{guid}/profile", s.handleBOInvitationOnboardingProfile)
		r.Post("/invitations/onboarding/{guid}/avatar", s.handleBOInvitationOnboardingAvatar)
		r.Post("/invitations/onboarding/{guid}/password", s.handleBOInvitationOnboardingPassword)
		r.Post("/password-resets/validate", s.handleBOPasswordResetValidate)
		r.Post("/password-resets/confirm", s.handleBOPasswordResetConfirm)

		r.With(s.requireBOSession).Get("/me", s.handleBOMe)
		r.With(s.requireBOSession).Post("/me/password", s.handleBOSetPassword)
		r.With(s.requireBOSession).Post("/active-restaurant", s.handleBOSetActiveRestaurant)

		r.With(s.requireBOSession, reservasGate).Get("/dashboard/metrics", s.handleBODashboardMetrics)

		r.With(s.requireBOSession, reservasGate).Get("/calendar", s.handleBOCalendarMonth)

		r.With(s.requireBOSession, reservasGate).Get("/bookings", s.handleBOBookingsList)
		r.With(s.requireBOSession, reservasGate).Get("/bookings/export", s.handleBOBookingsExport)
		r.With(s.requireBOSession, reservasGate).Get("/bookings/{id}", s.handleBOBookingGet)
		r.With(s.requireBOSession, reservasGate).Post("/bookings", s.handleBOBookingCreate)
		r.With(s.requireBOSession, reservasGate).Patch("/bookings/{id}", s.handleBOBookingPatch)
		r.With(s.requireBOSession, reservasGate).Post("/bookings/{id}/cancel", s.handleBOBookingCancel)

		r.With(s.requireBOSession, reservasGate).Get("/arroz-types", s.handleBOArrozTypes)

		// Backoffice menu management.
		r.With(s.requireBOSession, menusGate).Get("/menu-visibility", s.handleBOMenuVisibilityGet)
		r.With(s.requireBOSession, menusGate).Post("/menu-visibility", s.handleBOMenuVisibilitySet)

		r.With(s.requireBOSession, menusGate).Get("/menus/dia", s.handleBOMenuDiaGet)
		r.With(s.requireBOSession, menusGate).Post("/menus/dia/dishes", s.handleBOMenuDiaDishCreate)
		r.With(s.requireBOSession, menusGate).Patch("/menus/dia/dishes/{id}", s.handleBOMenuDiaDishPatch)
		r.With(s.requireBOSession, menusGate).Delete("/menus/dia/dishes/{id}", s.handleBOMenuDiaDishDelete)
		r.With(s.requireBOSession, menusGate).Post("/menus/dia/price", s.handleBOMenuDiaSetPrice)

		r.With(s.requireBOSession, menusGate).Get("/menus/finde", s.handleBOMenuFindeGet)
		r.With(s.requireBOSession, menusGate).Post("/menus/finde/dishes", s.handleBOMenuFindeDishCreate)
		r.With(s.requireBOSession, menusGate).Patch("/menus/finde/dishes/{id}", s.handleBOMenuFindeDishPatch)
		r.With(s.requireBOSession, menusGate).Delete("/menus/finde/dishes/{id}", s.handleBOMenuFindeDishDelete)
		r.With(s.requireBOSession, menusGate).Post("/menus/finde/price", s.handleBOMenuFindeSetPrice)

		r.With(s.requireBOSession, menusGate).Get("/postres", s.handleBOPostresList)
		r.With(s.requireBOSession, menusGate).Post("/postres", s.handleBOPostreCreate)
		r.With(s.requireBOSession, menusGate).Patch("/postres/{id}", s.handleBOPostrePatch)
		r.With(s.requireBOSession, menusGate).Delete("/postres/{id}", s.handleBOPostreDelete)

		r.With(s.requireBOSession, menusGate).Get("/vinos", s.handleBOVinosList)
		r.With(s.requireBOSession, menusGate).Post("/vinos", s.handleBOVinoCreate)
		r.With(s.requireBOSession, menusGate).Patch("/vinos/{id}", s.handleBOVinoPatch)
		r.With(s.requireBOSession, menusGate).Delete("/vinos/{id}", s.handleBOVinoDelete)

		// New comida module endpoints (typed routes).
		r.With(s.requireBOSession, menusGate).Get("/comida/platos/categorias", s.handleBOComidaPlatoCategoriesList)
		r.With(s.requireBOSession, menusGate).Post("/comida/platos/categorias", s.handleBOComidaPlatoCategoriesCreate)
		r.With(s.requireBOSession, menusGate).Get("/comida/{tipo}", s.handleBOComidaList)
		r.With(s.requireBOSession, menusGate).Get("/comida/{tipo}/{id}", s.handleBOComidaGet)
		r.With(s.requireBOSession, menusGate).Post("/comida/{tipo}", s.handleBOComidaCreate)
		r.With(s.requireBOSession, menusGate).Patch("/comida/{tipo}/{id}", s.handleBOComidaPatch)
		r.With(s.requireBOSession, menusGate).Delete("/comida/{tipo}/{id}", s.handleBOComidaDelete)

		// Legacy aliases consumed by current backoffice comida screen.
		r.With(s.requireBOSession, menusGate).Get("/platos", s.handleBOPlatosList)
		r.With(s.requireBOSession, menusGate).Post("/platos", s.handleBOPlatosCreate)
		r.With(s.requireBOSession, menusGate).Patch("/platos/{id}", s.handleBOPlatosPatch)
		r.With(s.requireBOSession, menusGate).Delete("/platos/{id}", s.handleBOPlatosDelete)
		r.With(s.requireBOSession, menusGate).Post("/platos/{id}/toggle", s.handleBOPlatosToggle)

		r.With(s.requireBOSession, menusGate).Get("/bebidas", s.handleBOBebidasList)
		r.With(s.requireBOSession, menusGate).Post("/bebidas", s.handleBOBebidasCreate)
		r.With(s.requireBOSession, menusGate).Patch("/bebidas/{id}", s.handleBOBebidasPatch)
		r.With(s.requireBOSession, menusGate).Delete("/bebidas/{id}", s.handleBOBebidasDelete)
		r.With(s.requireBOSession, menusGate).Post("/bebidas/{id}/toggle", s.handleBOBebidasToggle)

		r.With(s.requireBOSession, menusGate).Get("/cafes", s.handleBOCafesList)
		r.With(s.requireBOSession, menusGate).Post("/cafes", s.handleBOCafesCreate)
		r.With(s.requireBOSession, menusGate).Patch("/cafes/{id}", s.handleBOCafesPatch)
		r.With(s.requireBOSession, menusGate).Delete("/cafes/{id}", s.handleBOCafesDelete)
		r.With(s.requireBOSession, menusGate).Post("/cafes/{id}/toggle", s.handleBOCafesToggle)

		r.With(s.requireBOSession, menusGate).Get("/group-menus", s.handleBOGroupMenusList)
		r.With(s.requireBOSession, menusGate).Get("/group-menus/{id}", s.handleBOGroupMenuGet)
		r.With(s.requireBOSession, menusGate).Post("/group-menus", s.handleBOGroupMenuCreate)
		r.With(s.requireBOSession, menusGate).Put("/group-menus/{id}", s.handleBOGroupMenuUpdate)
		r.With(s.requireBOSession, menusGate).Post("/group-menus/{id}/toggle", s.handleBOGroupMenuToggleActive)
		r.With(s.requireBOSession, menusGate).Delete("/group-menus/{id}", s.handleBOGroupMenuDelete)
		r.With(s.requireBOSession, menusGate).Get("/group-menus-v2", s.handleBOGroupMenusV2List)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/drafts", s.handleBOGroupMenusV2CreateDraft)
		r.With(s.requireBOSession, menusGate).Get("/group-menus-v2/{id}", s.handleBOGroupMenusV2Get)
		r.With(s.requireBOSession, menusGate).Patch("/group-menus-v2/{id}/basics", s.handleBOGroupMenusV2PatchBasics)
		r.With(s.requireBOSession, menusGate).Patch("/group-menus-v2/{id}/menu-type", s.handleBOGroupMenusV2PatchMenuType)
		r.With(s.requireBOSession, menusGate).Put("/group-menus-v2/{id}/sections", s.handleBOGroupMenusV2PutSections)
		r.With(s.requireBOSession, menusGate).Put("/group-menus-v2/{id}/sections/{sectionId}/dishes", s.handleBOGroupMenusV2PutSectionDishes)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/publish", s.handleBOGroupMenusV2Publish)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/toggle-active", s.handleBOGroupMenusV2ToggleActive)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/special-image", s.handleBOSpecialMenuImageUpload)
		r.With(s.requireBOSession, menusGate).Delete("/group-menus-v2/{id}", s.handleBOGroupMenusV2Delete)
		r.With(s.requireBOSession, menusGate).Get("/dishes-catalog/search", s.handleBODishesCatalogSearch)
		r.With(s.requireBOSession, menusGate).Post("/dishes-catalog/upsert", s.handleBODishesCatalogUpsert)

		// Backoffice configuration for reservations.
		r.With(s.requireBOSession, reservasGate).Get("/config/defaults", s.handleBOConfigDefaultsGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/defaults", s.handleBOConfigDefaultsSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/day", s.handleBOConfigDayGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/day", s.handleBOConfigDaySet)

		r.With(s.requireBOSession, reservasGate).Get("/config/opening-hours", s.handleBOConfigOpeningHoursGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/opening-hours", s.handleBOConfigOpeningHoursSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/mesas-de-dos", s.handleBOConfigMesasDeDosGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/mesas-de-dos", s.handleBOConfigMesasDeDosSet)
		r.With(s.requireBOSession, reservasGate).Get("/config/mesas-de-tres", s.handleBOConfigMesasDeTresGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/mesas-de-tres", s.handleBOConfigMesasDeTresSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/floors/defaults", s.handleBOConfigFloorsDefaultsGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/floors/defaults", s.handleBOConfigFloorsDefaultsSet)
		r.With(s.requireBOSession, reservasGate).Get("/config/floors", s.handleBOConfigFloorsGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/floors", s.handleBOConfigFloorsSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/salon-condesa", s.handleBOConfigSalonCondesaGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/salon-condesa", s.handleBOConfigSalonCondesaSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/daily-limit", s.handleBOConfigDailyLimitGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/daily-limit", s.handleBOConfigDailyLimitSet)

		// Restaurant-level settings (integrations/branding).
		r.With(s.requireBOSession, ajustesGate).Get("/integrations", s.handleBOIntegrationsGet)
		r.With(s.requireBOSession, ajustesGate).Post("/integrations", s.handleBOIntegrationsSet)
		r.With(s.requireBOSession, ajustesGate).Get("/branding", s.handleBOBrandingGet)
		r.With(s.requireBOSession, ajustesGate).Post("/branding", s.handleBOBrandingSet)

		// Members and role administration.
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members", s.handleBOMembersList)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members", s.handleBOMemberCreate)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}", s.handleBOMemberGet)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Patch("/members/{id}", s.handleBOMemberPatch)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/avatar", s.handleBOMemberAvatarUpload)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}/stats", s.handleBOMemberStats)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}/stats-year", s.handleBOMemberStatsYear)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}/stats-range", s.handleBOMemberStatsRange)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}/table-data", s.handleBOMemberTableData)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}/time-balance", s.handleBOMemberQuarterBalance)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/ensure-user", s.handleBOMemberEnsureUser)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/invitation/resend", s.handleBOMemberInvitationResend)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/password-reset/send", s.handleBOMemberPasswordResetSend)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/roles", s.handleBORolesGet)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/roles", s.handleBORoleCreate)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Patch("/users/{id}/role", s.handleBOUserRolePatch)

		// Fichaje and schedules.
		r.With(s.requireBOSession, fichajeGate).Get("/fichaje/ping", s.handleBOFichajePing)
		r.With(s.requireBOSession, fichajeGate).Get("/fichaje/state", s.handleBOFichajeState)
		r.With(s.requireBOSession, fichajeGate).Post("/fichaje/start", s.handleBOFichajeStart)
		r.With(s.requireBOSession, fichajeGate).Post("/fichaje/stop", s.handleBOFichajeStop)
		r.With(s.requireBOSession, fichajeGate).Get("/fichaje/ws", s.handleBOFichajeWS)
		r.With(s.requireBOSession, fichajeGate, rolesAdminGate).Post("/fichaje/admin/start", s.handleBOFichajeAdminStart)
		r.With(s.requireBOSession, fichajeGate, rolesAdminGate).Post("/fichaje/admin/stop", s.handleBOFichajeAdminStop)
		r.With(s.requireBOSession, fichajeGate, rolesAdminGate).Get("/fichaje/entries", s.handleBOFichajeEntriesList)
		r.With(s.requireBOSession, fichajeGate, rolesAdminGate).Patch("/fichaje/entries/{id}", s.handleBOFichajeEntryPatch)

		r.With(s.requireBOSession, horariosGate).Get("/horarios", s.handleBOHorariosList)
		r.With(s.requireBOSession, horariosGate).Post("/horarios", s.handleBOHorariosAssign)
		r.With(s.requireBOSession, horariosGate).Put("/horarios/{id}", s.handleBOHorariosUpdate)
		r.With(s.requireBOSession, horariosGate).Delete("/horarios/{id}", s.handleBOHorariosDelete)
		r.With(s.requireBOSession, horariosGate).Get("/horarios/month", s.handleBOHorariosMonth)
		r.With(s.requireBOSession, fichajeGate).Get("/horarios/my-schedule", s.handleBOHorariosMySchedule)

		// Invoices management
		r.With(s.requireBOSession, facturasGate).Get("/invoices", s.handleBOInvoicesList)
		r.With(s.requireBOSession, facturasGate).Get("/invoices/{id}", s.handleBOInvoiceGet)
		r.With(s.requireBOSession, facturasGate).Post("/invoices", s.handleBOInvoiceCreate)
		r.With(s.requireBOSession, facturasGate).Put("/invoices/{id}", s.handleBOInvoiceUpdate)
		r.With(s.requireBOSession, facturasGate).Delete("/invoices/{id}", s.handleBOInvoiceDelete)
		r.With(s.requireBOSession, facturasGate).Post("/invoices/{id}/send", s.handleBOInvoiceSend)
		r.With(s.requireBOSession, facturasGate).Post("/invoices/{id}/upload-image", s.handleBOInvoiceUploadImage)
		r.With(s.requireBOSession, facturasGate).Get("/invoices/search-reservation", s.handleBOInvoicesSearchReservation)
	})

	// Everything below is restaurant-scoped.
	r.Group(func(r chi.Router) {
		r.Use(s.withRestaurant)

		// Public endpoints (used by the Preact client).
		r.Get("/menu-visibility", s.handleMenuVisibility)
		r.With(s.requireAdmin).Post("/menu-visibility", s.handleMenuVisibilityToggle)
		r.Get("/menus/public", s.handlePublicMenus)
		r.Get("/menus/dia", s.handleMenuDia)
		r.Get("/menus/finde", s.handleMenuFinde)
		r.Get("/postres", s.handlePostres)
		r.Get("/vinos", s.handleVinos)
		r.Get("/comida/platos/categorias", s.handleComidaPublicPlatoCategoriesList)
		r.Get("/comida/{tipo}", s.handleComidaPublicList)
		r.Get("/comida/{tipo}/{id}", s.handleComidaPublicGet)
		r.With(s.requireAdmin).Post("/comida/platos/categorias", s.handleComidaPublicPlatoCategoriesCreate)
		r.With(s.requireAdmin).Post("/comida/{tipo}", s.handleComidaPublicCreate)
		r.With(s.requireAdmin).Patch("/comida/{tipo}/{id}", s.handleComidaPublicPatch)
		r.With(s.requireAdmin).Delete("/comida/{tipo}/{id}", s.handleComidaPublicDelete)

		// Admin actions for wines (legacy admin UI uses api_vinos.php).
		r.With(s.requireAdmin).Post("/vinos", s.handleVinosAdmin)
		r.Get("/api_vinos.php", s.handleVinos)
		r.With(s.requireAdmin).Post("/api_vinos.php", s.handleVinosAdmin)

		// Legacy-compatible admin endpoints for menu/postre management.
		r.With(s.requireAdmin).Post("/updateDishDia.php", s.handleUpdateDishDia)
		r.With(s.requireAdmin).Post("/toggleDishStatusDia.php", s.handleToggleDishStatusDia)
		r.Get("/searchDishesDia.php", s.handleSearchDishesDia)

		r.With(s.requireAdmin).Post("/updateDish.php", s.handleUpdateDishFinde)
		r.With(s.requireAdmin).Post("/toggleDishStatus.php", s.handleToggleDishStatusGeneric)
		r.Get("/searchDishesFinde.php", s.handleSearchDishesFinde)

		r.With(s.requireAdmin).Post("/updatePostre.php", s.handleUpdatePostre)
		r.With(s.requireAdmin).Get("/updatePostre.php", s.handleUpdatePostre) // supports GET?action=getPostres
		r.With(s.requireAdmin).Get("/searchPostres.php", s.handleSearchPostres)

		// Legacy menu visibility backend.
		r.Route("/menuVisibilityBackend", func(r chi.Router) {
			r.Get("/getMenuVisibility.php", s.handleGetMenuVisibilityLegacy)
			r.With(s.requireAdmin).Post("/toggleMenuVisibility.php", s.handleToggleMenuVisibilityLegacy)
		})

		// Legacy group menus backend.
		r.Route("/menuDeGruposBackend", func(r chi.Router) {
			r.Get("/getAllMenus.php", s.handleGetAllGroupMenus)
			r.Get("/getMenu.php", s.handleGetGroupMenu)
			r.Get("/getActiveMenusForDisplay.php", s.handleGetActiveGroupMenusForDisplay)
			r.With(s.requireAdmin).Post("/addMenu.php", s.handleAddGroupMenu)
			r.With(s.requireAdmin).Post("/updateMenu.php", s.handleUpdateGroupMenu)
			r.With(s.requireAdmin).Put("/updateMenu.php", s.handleUpdateGroupMenu)
			r.With(s.requireAdmin).Post("/toggleActive.php", s.handleToggleGroupMenuActive)
			r.With(s.requireAdmin).Post("/deleteMenu.php", s.handleDeleteGroupMenu)
			r.With(s.requireAdmin).Delete("/deleteMenu.php", s.handleDeleteGroupMenu)
		})

		// Reservations / booking endpoints (legacy names).
		r.Get("/fetch_arroz.php", s.handleFetchArroz)

		// Public availability helpers used by reservas.php.
		r.Post("/fetch_daily_limit.php", s.handleFetchDailyLimit)
		r.Post("/fetch_month_availability.php", s.handleFetchMonthAvailability)
		r.Get("/fetch_closed_days.php", s.handleFetchClosedDays)
		r.Post("/fetch_mesas_de_dos.php", s.handleFetchMesasDeDos)

		// Salón Condesa state: public GET, admin POST.
		r.Get("/salon_condesa_api.php", s.handleSalonCondesaGet)
		r.With(s.requireAdmin).Post("/salon_condesa_api.php", s.handleSalonCondesaSet)

		// Hour availability configuration (legacy /api/gethourdata.php + savehourdata.php).
		r.Get("/gethourdata.php", s.handleGetHourData)
		r.With(s.requireAdmin).Post("/savehourdata.php", s.handleSaveHourData)

		// Opening hours (public GET, admin POST).
		r.Get("/getopeninghours.php", s.handleGetOpeningHours)
		r.With(s.requireAdmin).Post("/editopeninghours.php", s.handleEditOpeningHours)

		// Hour percentages (public GET, admin POST).
		r.Get("/gethourpercentages.php", s.handleGetHourPercentages)
		r.With(s.requireAdmin).Post("/updatehourpercentages.php", s.handleUpdateHourPercentages)

		// Calendar data (admin UI).
		r.Get("/get_calendar_data.php", s.handleGetCalendarData)

		// Group menus: helper for reservas.php flow.
		r.Get("/getValidMenusForPartySize.php", s.handleGetValidMenusForPartySize)

		// Automation helpers (n8n).
		r.Get("/get_available_rice_types.php", s.handleGetAvailableRiceTypes)
		r.Get("/get_booking_availability_context.php", s.handleGetBookingAvailabilityContext)
		r.Post("/get_booking_availability_context.php", s.handleGetBookingAvailabilityContext)
		r.Post("/check_date_availability.php", s.handleCheckDateAvailability)
		r.Post("/check_party_size_availability.php", s.handleCheckPartySizeAvailability)
		r.Post("/validate_booking_modifiable.php", s.handleValidateBookingModifiable)
		r.Post("/update_reservation.php", s.handleUpdateReservation)
		r.Post("/save_modification_history.php", s.handleSaveModificationHistory)
		r.Post("/notify_restaurant_modification.php", s.handleNotifyRestaurantModification)

		// Navidad contact form.
		r.Post("/navidad_booking.php", s.handleNavidadBooking)

		// Conversation history storage endpoints.
		r.Get("/get_conversation_history.php", s.handleGetConversationHistory)
		r.Post("/save_conversation_message.php", s.handleSaveConversationMessage)

		// Legacy marketing/admin tool (AJAX mode only).
		r.With(s.requireAdmin).Post("/emailAdvertising/sendEmailAndWhastappAd.php", s.handleSendEmailAndWhatsappAd)

		// Legacy root endpoints used by n8n workflows (exposed under /api and aliased at /).
		r.Get("/get_conversation_state.php", s.handleGetConversationState)
		r.Post("/save_conversation_state.php", s.handleSaveConversationState)
		r.Get("/modification_checker.php", s.handleModificationChecker)
		r.Get("/checkcancel.php", s.handleCheckCancel)
		r.Post("/checkcancel.php", s.handleCheckCancel)
		r.Get("/n8nReminder.php", s.handleN8nReminder)

		// Public HTML endpoints used in WhatsApp links (legacy PHP pages).
		r.Get("/confirm_reservation.php", s.handleConfirmReservationPage)
		r.Post("/confirm_reservation.php", s.handleConfirmReservationPage)
		r.Get("/cancel_reservation.php", s.handleCancelReservationPage)
		r.Post("/cancel_reservation.php", s.handleCancelReservationPage)
		r.Get("/book_rice.php", s.handleBookRicePage)
		r.Post("/book_rice.php", s.handleBookRicePage)

		// Public booking creation (front form).
		r.Post("/insert_booking_front.php", s.handleInsertBookingFront)

		// Admin booking management (confreservas.php).
		r.With(s.requireAdmin).Post("/insert_booking.php", s.handleInsertBookingAdmin)
		r.With(s.requireAdmin).Post("/fetch_bookings.php", s.handleFetchBookings)
		r.With(s.requireAdmin).Post("/get_booking.php", s.handleGetBooking)
		r.With(s.requireAdmin).Post("/edit_booking.php", s.handleEditBooking)
		r.With(s.requireAdmin).Post("/delete_booking.php", s.handleDeleteBooking)
		r.With(s.requireAdmin).Post("/update_table_number.php", s.handleUpdateTableNumber)
		r.With(s.requireAdmin).Post("/get_reservations.php", s.handleGetReservations)
		r.With(s.requireAdmin).Post("/fetch_cancelled_bookings.php", s.handleFetchCancelledBookings)
		r.With(s.requireAdmin).Post("/reactivate_booking.php", s.handleReactivateBooking)

		// Admin tools / settings.
		r.With(s.requireAdmin).Post("/update_daily_limit.php", s.handleUpdateDailyLimit)
		r.With(s.requireAdmin).Post("/limitemesasdedos.php", s.handleSetMesasDeDosLimit)
		r.With(s.requireAdmin).Post("/get_mesasdedos_limit.php", s.handleGetMesasDeDosLimit)
		r.With(s.requireAdmin).Post("/check_day_status.php", s.handleCheckDayStatus)
		r.With(s.requireAdmin).Post("/open_day.php", s.handleOpenDay)
		r.With(s.requireAdmin).Post("/close_day.php", s.handleCloseDay)
		r.With(s.requireAdmin).Post("/fetch_occupancy.php", s.handleFetchOccupancy)
	})

	return r
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	// If ADMIN_TOKEN is not set, don't gate admin endpoints (dev convenience).
	if strings.TrimSpace(s.cfg.AdminToken) == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
		if token == "" {
			// Bearer token support.
			authz := strings.TrimSpace(r.Header.Get("Authorization"))
			if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
				token = strings.TrimSpace(authz[len("bearer "):])
			}
		}

		if token == "" || token != s.cfg.AdminToken {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleMenuVisibility(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), "SELECT menu_key, is_active FROM menu_visibility WHERE restaurant_id = ?", restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menu_visibility")
		return
	}
	defer rows.Close()

	visibility := map[string]bool{
		"menudeldia":      true,
		"menufindesemana": true,
	}

	for rows.Next() {
		var key string
		var active int
		if err := rows.Scan(&key, &active); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo menu_visibility")
			return
		}
		visibility[key] = active != 0
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"menuVisibility": visibility,
	})
}

type Dish struct {
	Descripcion string   `json:"descripcion"`
	Alergenos   []string `json:"alergenos"`
}

type MenuResponse struct {
	Success     bool   `json:"success"`
	Entrantes   []Dish `json:"entrantes"`
	Principales []Dish `json:"principales"`
	Arroces     []Dish `json:"arroces"`
	Precio      string `json:"precio"`
}

func (s *Server) handleMenuDia(w http.ResponseWriter, r *http.Request) {
	resp, err := s.fetchMenuByTable(r, "DIA")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMenuFinde(w http.ResponseWriter, r *http.Request) {
	resp, err := s.fetchMenuByTable(r, "FINDE")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Server) fetchMenuByTable(r *http.Request, table string) (MenuResponse, error) {
	entrantes, err := s.fetchDishes(r, table, "ENTRANTE")
	if err != nil {
		return MenuResponse{}, err
	}
	principales, err := s.fetchDishes(r, table, "PRINCIPAL")
	if err != nil {
		return MenuResponse{}, err
	}
	arroces, err := s.fetchDishes(r, table, "ARROZ")
	if err != nil {
		return MenuResponse{}, err
	}

	precio, err := s.fetchPrecio(r, table)
	if err != nil {
		return MenuResponse{}, err
	}

	return MenuResponse{
		Success:     true,
		Entrantes:   entrantes,
		Principales: principales,
		Arroces:     arroces,
		Precio:      precio,
	}, nil
}

func (s *Server) fetchDishes(r *http.Request, table string, dishType string) ([]Dish, error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return nil, errors.New("Unknown restaurant")
	}

	q := "SELECT DESCRIPCION, alergenos FROM " + table + " WHERE restaurant_id = ? AND TIPO = ? AND active = 1 ORDER BY NUM ASC"
	rows, err := s.db.QueryContext(r.Context(), q, restaurantID, dishType)
	if err != nil {
		return nil, errors.New("Error consultando " + table)
	}
	defer rows.Close()

	var dishes []Dish
	for rows.Next() {
		var descripcion string
		var alergenosRaw sql.NullString
		if err := rows.Scan(&descripcion, &alergenosRaw); err != nil {
			return nil, errors.New("Error leyendo " + table)
		}
		dishes = append(dishes, Dish{
			Descripcion: descripcion,
			Alergenos:   parseAlergenos(alergenosRaw),
		})
	}
	return dishes, nil
}

func (s *Server) fetchPrecio(r *http.Request, table string) (string, error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return "", errors.New("Unknown restaurant")
	}

	q := "SELECT DESCRIPCION FROM " + table + " WHERE restaurant_id = ? AND TIPO = 'PRECIO' AND active = 1 ORDER BY NUM ASC LIMIT 1"
	var precio sql.NullString
	if err := s.db.QueryRowContext(r.Context(), q, restaurantID).Scan(&precio); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", errors.New("Error consultando precio en " + table)
	}
	if !precio.Valid {
		return "", nil
	}
	return precio.String, nil
}

func (s *Server) handlePostres(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), "SELECT DESCRIPCION, alergenos FROM POSTRES WHERE restaurant_id = ? AND active = 1 ORDER BY NUM ASC", restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando POSTRES")
		return
	}
	defer rows.Close()

	var postres []Dish
	for rows.Next() {
		var descripcion string
		var alergenosRaw sql.NullString
		if err := rows.Scan(&descripcion, &alergenosRaw); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo POSTRES")
			return
		}
		postres = append(postres, Dish{
			Descripcion: descripcion,
			Alergenos:   parseAlergenos(alergenosRaw),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"postres": postres,
	})
}

func (s *Server) handleVinos(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	q := r.URL.Query()

	includeImage := true
	if v := q.Get("include_image"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			includeImage = parsed
		}
	}

	active := 1
	if v := q.Get("active"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			active = parsed
		}
	}

	var requestedNum *int
	if v := q.Get("num"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "El parámetro num debe ser un entero mayor que 0")
			return
		}
		requestedNum = &parsed
	}

	tipo := q.Get("tipo")
	if requestedNum == nil && tipo == "" {
		httpx.WriteError(w, http.StatusBadRequest, "El parámetro tipo es obligatorio")
		return
	}

	fields := "num, nombre, precio, descripcion, bodega, denominacion_origen, tipo, graduacion, anyo, active, (foto_path IS NOT NULL AND LENGTH(foto_path) > 0) AS has_foto"
	if includeImage {
		fields += ", foto_path"
	}

	query := "SELECT " + fields + " FROM VINOS WHERE restaurant_id = ? AND active = ?"
	args := []any{restaurantID, active}
	if requestedNum != nil {
		query += " AND num = ?"
		args = append(args, *requestedNum)
	} else {
		query += " AND tipo = ?"
		args = append(args, tipo)
	}

	query += " ORDER BY num ASC"
	if requestedNum != nil {
		query += " LIMIT 1"
	}

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando VINOS")
		return
	}
	defer rows.Close()

	type Vino struct {
		Num                int     `json:"num"`
		Nombre             string  `json:"nombre"`
		Precio             float64 `json:"precio"`
		Descripcion        string  `json:"descripcion"`
		Bodega             string  `json:"bodega"`
		DenominacionOrigen string  `json:"denominacion_origen"`
		Tipo               string  `json:"tipo"`
		Graduacion         float64 `json:"graduacion"`
		Anyo               string  `json:"anyo"`
		Active             int     `json:"active"`
		HasFoto            bool    `json:"has_foto"`
		FotoURL            *string `json:"foto_url,omitempty"`
	}

	var vinos []Vino
	for rows.Next() {
		var v Vino
		var nombre sql.NullString
		var precio sql.NullFloat64
		var descripcion sql.NullString
		var bodega sql.NullString
		var denominacionOrigen sql.NullString
		var tipoVal sql.NullString
		var graduacion sql.NullFloat64
		var anyo sql.NullString
		var hasFotoInt int
		var fotoPath sql.NullString

		if includeImage {
			if err := rows.Scan(&v.Num, &nombre, &precio, &descripcion, &bodega, &denominacionOrigen, &tipoVal, &graduacion, &anyo, &v.Active, &hasFotoInt, &fotoPath); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo VINOS")
				return
			}
			if fotoPath.Valid && strings.TrimSpace(fotoPath.String) != "" {
				u := s.bunnyPullURL(fotoPath.String)
				v.FotoURL = &u
			}
		} else {
			if err := rows.Scan(&v.Num, &nombre, &precio, &descripcion, &bodega, &denominacionOrigen, &tipoVal, &graduacion, &anyo, &v.Active, &hasFotoInt); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo VINOS")
				return
			}
		}

		// Some legacy rows may contain NULLs in nullable columns; normalize to zero-values
		// to keep response shape stable for the frontend.
		if nombre.Valid {
			v.Nombre = nombre.String
		}
		if precio.Valid {
			v.Precio = precio.Float64
		}
		if descripcion.Valid {
			v.Descripcion = descripcion.String
		}
		if bodega.Valid {
			v.Bodega = bodega.String
		}
		if denominacionOrigen.Valid {
			v.DenominacionOrigen = denominacionOrigen.String
		}
		if tipoVal.Valid {
			v.Tipo = tipoVal.String
		}
		if graduacion.Valid {
			v.Graduacion = graduacion.Float64
		}
		if anyo.Valid {
			v.Anyo = anyo.String
		}

		v.HasFoto = hasFotoInt != 0
		vinos = append(vinos, v)
	}

	response := map[string]any{
		"success": true,
		"vinos":   vinos,
	}
	payload, _ := json.Marshal(response)

	w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=300")
	w.Header().Set("Surrogate-Control", "max-age=300")
	w.Header().Set("Vary", "Accept-Encoding")

	etag := `"` + md5Hex(payload) + `"`
	w.Header().Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); strings.TrimSpace(inm) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func parseAlergenos(raw sql.NullString) []string {
	if !raw.Valid {
		return []string{}
	}
	s := strings.TrimSpace(raw.String)
	if s == "" || s == "null" {
		return []string{}
	}

	var out []string
	if err := json.Unmarshal([]byte(s), &out); err == nil && len(out) > 0 {
		return out
	}

	parts := strings.Split(s, ",")
	var cleaned []string
	for _, part := range parts {
		t := strings.TrimSpace(part)
		if t == "" {
			continue
		}
		cleaned = append(cleaned, t)
	}
	return cleaned
}

func SPAHandler(staticDir string) http.Handler {
	fsys := os.DirFS(staticDir)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Cache hashed Vite assets aggressively.
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		f, err := fsys.Open(path)
		if err == nil {
			defer f.Close()
			http.FileServer(http.FS(fsys)).ServeHTTP(w, r)
			return
		}

		// Fallback to SPA entrypoint for client-side routes.
		r.URL.Path = "/index.html"
		_, _ = fs.Stat(fsys, "index.html")
		http.FileServer(http.FS(fsys)).ServeHTTP(w, r)
	})
}
