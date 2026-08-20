package api

import (
	"context"
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
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/httpx"
)

type Server struct {
	db                    *sql.DB
	cfg                   config.Config
	tenantCache           tenantDomainCache
	fichajeHub            *boFichajeHub
	tablesHub             *boTablesHub
	sheetHub              *sheetWSHub
	groupMenusV2AIHub     *boGroupMenuV2AIHub
	groupMenusV2AIQueue   chan struct{}
	vinoAIHub             *boVinoAIHub
	comidaAIHub           *boComidaAIHub
	whatsappConnectionHub *boWhatsAppConnectionHub
	rateMu                sync.Mutex
	// fichajeMu serializes check-then-write clock operations. The active-entry
	// invariant must hold even when two kiosk requests arrive concurrently.
	fichajeMu sync.Mutex
	// scheduleMu protects overlap validation followed by schedule writes.
	scheduleMu           sync.Mutex
	rateLimit            map[string]*rateLimitState
	botSeenMu            sync.Mutex
	botSeen              map[string]int64
	botCapMu             sync.Mutex
	botCapDay            string
	botCapCount          map[int]int
	botSem               chan struct{} // bounds concurrent inbound agent turns
	provisionMu          sync.Mutex    // ponytail: serializes UAZAPI provisioning; single-instance only — use a DB lock if you run multiple backend replicas
	instatic             *instaticManager
	siteBuilderHub       *siteBuilderWSHub
	assistantRateMu      sync.Mutex
	assistantRateBuckets map[string]*assistantRateBucket
	// assistantKeepalive overrides the WebSocket keepalive timings; zero means
	// production defaults. Set once at construction, read-only afterwards.
	assistantKeepalive assistantKeepaliveConfig
	confirmationStore  *confirmationStore
	sessionCache       *boSessionCache
	bunnyCredsCache    *bunnyCredentialsCache
	minimaxStoreCache  *minimaxStoreCache
}

// assistantKeepaliveConfig lets tests shorten the assistant WebSocket timings.
type assistantKeepaliveConfig struct {
	readTimeout  time.Duration
	pingInterval time.Duration
}

func NewServer(db *sql.DB, cfg config.Config) *Server {
	aiConcurrency := cfg.OpenAIConcurrency
	if aiConcurrency <= 0 {
		aiConcurrency = 1
	}
	s := &Server{
		db:                    db,
		cfg:                   cfg,
		fichajeHub:            newBOFichajeHub(),
		tablesHub:             newBOTablesHub(),
		sheetHub:              newSheetWSHub(),
		groupMenusV2AIHub:     newBOGroupMenuV2AIHub(),
		groupMenusV2AIQueue:   make(chan struct{}, aiConcurrency),
		vinoAIHub:             newBOVinoAIHub(),
		comidaAIHub:           newBOComidaAIHub(),
		whatsappConnectionHub: newBOWAConnectionHub(),
		rateLimit:             make(map[string]*rateLimitState),
		botSem:                make(chan struct{}, botMaxConcurrentTurns),
		confirmationStore:     newConfirmationStore(db),
		sessionCache:          newBOSessionCache(30 * time.Second),
		bunnyCredsCache:       newBunnyCredentialsCache(),
		minimaxStoreCache:     newMiniMaxStoreCache(),
	}
	s.instatic = newInstaticManager(db, cfg)
	s.instatic.StartSupervisor()
	s.siteBuilderHub = newSiteBuilderWSHub()
	go s.runBOFichajeAutoCutLoop()
	go s.runPreShiftReminderLoop(context.Background())
	go s.runWhatsAppOutboxLoop(context.Background())
	return s
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	// Compress JSON/text responses. SSE (text/event-stream) and websocket
	// upgrades are skipped by chi's default compressible-content-type list.
	r.Use(middleware.Compress(5))
	websiteBuilder := newWebsiteBuilder(s)

	// Restaurant website virtual host: `<slug>.<app_base_url>` → instatic.
	// Runs before path routing so restaurant sites never hit admin/API routes.
	// Non-restaurant hosts fall through to normal routing.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.serveRestaurantSiteIfHost(w, r, next)
		})
	})

	// CORS for API and legacy endpoints.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			allowedOrigin := s.resolveAllowedOrigin(origin)
			if allowedOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token, X-Api-Token")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	// Backoffice (new React SSR dashboard).
	// Strip /api prefix for /api/admin/* routes to make them work with /admin handlers
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/admin") {
				r.URL.Path = strings.Replace(r.URL.Path, "/api/admin", "/admin", 1)
			} else if strings.HasPrefix(r.URL.Path, "/api/") {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
			}
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/healthz", s.handleHealthz)

	r.Route("/admin", func(r chi.Router) {
		reservasGate := s.requireBOSection(boSectionReservas)
		menusGate := s.requireBOSection(boSectionMenus)
		ajustesGate := s.requireBOSection(boSectionAjustes)
		miembrosGate := s.requireBOSection(boSectionMiembros)
		fichajeGate := s.requireBOSection(boSectionFichaje)
		horariosGate := s.requireBOSection(boSectionHorarios)
		facturasGate := s.requireBOSection(boSectionFacturas)
		stockViewGate := s.requireBOStockPermission(stockPermissionView)
		stockTransferGate := s.requireBOStockPermission(stockPermissionTransfer)
		stockItemsGate := s.requireBOStockPermission(stockPermissionItemsManage)
		stockWarehousesGate := s.requireBOStockPermission(stockPermissionWarehousesManage)
		stockCountPerformGate := s.requireBOStockPermission(stockPermissionCountPerform)
		stockCountCloseGate := s.requireBOStockPermission(stockPermissionCountClose)
		stockRecipesViewGate := s.requireBOStockPermission(stockPermissionRecipesView)
		stockRecipesManageGate := s.requireBOStockPermission(stockPermissionRecipesManage)
		stockProductionGate := s.requireBOStockPermission(stockPermissionProduction)
		stockForecastGate := s.requireBOStockPermission(stockPermissionForecastView)
		stockOCRUploadGate := s.requireBOStockPermission(stockPermissionOCRUpload)
		stockOCRConfirmGate := s.requireBOStockPermission(stockPermissionOCRConfirm)
		stockCostsViewGate := s.requireBOStockPermission(stockPermissionCostsView)
		stockCostsManageGate := s.requireBOStockPermission(stockPermissionCostsManage)
		stockSettingsGate := s.requireBOStockPermission(stockPermissionSettingsManage)
		posViewGate := s.requireBOPOSPermission(posPermissionView)
		posSellGate := s.requireBOPOSPermission(posPermissionSell)
		posVisitManageGate := s.requireBOPOSPermission(posPermissionVisitManage)
		posLineVoidGate := s.requireBOPOSPermission(posPermissionLineVoid)
		posDiscountGate := s.requireBOPOSPermission(posPermissionDiscount)
		posCheckoutGate := s.requireBOPOSPermission(posPermissionCheckout)
		posRefundGate := s.requireBOPOSPermission(posPermissionRefund)
		posShiftGate := s.requireBOPOSPermission(posPermissionShiftManage)
		posCatalogGate := s.requireBOPOSPermission(posPermissionCatalog)
		posStockMappingGate := s.requireBOPOSPermission(posPermissionStockMapping)
		posCoversAdjustGate := s.requireBOPOSPermission(posPermissionCoversAdjust)
		posReportsGate := s.requireBOPOSPermission(posPermissionReports)
		posSettingsGate := s.requireBOPOSPermission(posPermissionSettings)
		posKitchenGate := s.requireBOPOSPermission(posPermissionKitchen)
		statisticsGate := s.requireBOSection(boSectionEstadisticas)
		rolesAdminGate := s.requireBORoleImportanceAtLeast(90)
		rootOnlyGate := s.requireBORoleImportanceAtLeast(100)

		r.Post("/login", s.handleBOLogin)
		r.Post("/logout", s.handleBOLogout)

		// Error reporting from the backoffice ErrorBoundary.
		// Does not require authentication — errors should be logged even if the
		// session is broken. The request is fire-and-forget from the client.
		r.Post("/errors", s.handleAdminErrorReport)

		r.Post("/invitations/validate", s.handleBOInvitationValidate)
		r.Post("/invitations/onboarding/start", s.handleBOInvitationOnboardingStart)
		r.Get("/invitations/onboarding/{guid}", s.handleBOInvitationOnboardingGet)
		r.Post("/invitations/onboarding/{guid}/profile", s.handleBOInvitationOnboardingProfile)
		r.Post("/invitations/onboarding/{guid}/avatar", s.handleBOInvitationOnboardingAvatar)
		r.Post("/invitations/onboarding/{guid}/password", s.handleBOInvitationOnboardingPassword)
		r.Post("/password-resets/validate", s.handleBOPasswordResetValidate)
		r.Post("/password-resets/confirm", s.handleBOPasswordResetConfirm)

		r.With(s.requireBOSession).Get("/me", s.handleBOMe)
		r.With(s.requireBOSession).Put("/me/preferences", s.handleBOPreferencesSet)
		r.With(s.requireBOSession).Post("/me/password", s.handleBOSetPassword)
		r.With(s.requireBOSession).Post("/active-restaurant", s.handleBOSetActiveRestaurant)

		r.With(s.requireBOSession, reservasGate).Get("/dashboard/metrics", s.handleBODashboardMetrics)
		r.With(s.requireBOSession, menusGate).Get("/comida/counts", s.handleBOComidaCounts)

		r.With(s.requireBOSession, reservasGate).Get("/calendar", s.handleBOCalendarMonth)

		r.With(s.requireBOSession, reservasGate).Get("/bookings", s.handleBOBookingsList)
		r.With(s.requireBOSession, reservasGate).Get("/bookings/search", s.handleBOBookingsSearch)
		r.With(s.requireBOSession, reservasGate).Get("/bookings/export", s.handleBOBookingsExport)
		r.With(s.requireBOSession, reservasGate).Get("/bookings/{id}", s.handleBOBookingGet)
		r.With(s.requireBOSession, reservasGate).Post("/bookings", s.handleBOBookingCreate)
		r.With(s.requireBOSession, reservasGate).Patch("/bookings/{id}", s.handleBOBookingPatch)
		r.With(s.requireBOSession, reservasGate).Post("/bookings/{id}/cancel", s.handleBOBookingCancel)
		r.With(s.requireBOSession, reservasGate).Get("/bookings/cancelled", s.handleBOBookingsCancelledByDate)
		r.With(s.requireBOSession, reservasGate).Get("/bookings/modified", s.handleBOBookingsModifiedByDate)
		r.With(s.requireBOSession, reservasGate).Post("/bookings/cancelled/{id}/reactivate", s.handleBOBookingReactivate)

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
		r.With(s.requireBOSession, menusGate).Get("/vinos/{id}", s.handleBOVinoGet)
		r.With(s.requireBOSession, menusGate).Post("/vinos/{id}/image", s.handleBOVinoImageUpload)
		r.With(s.requireBOSession, menusGate).Post("/vinos/{id}/image/ai", s.handleBOVinoAIImageGenerate)
		r.With(s.requireBOSession, menusGate).Get("/vinos/ws", s.handleBOVinosAIWS)

		// Comida AI image enhancement.
		r.With(s.requireBOSession, menusGate).Post("/comida/{tipo}/{id}/image", s.handleBOComidaImageUpload)
		r.With(s.requireBOSession, menusGate).Post("/comida/{tipo}/{id}/image/ai", s.handleBOComidaImageAI)
		r.With(s.requireBOSession, menusGate).Get("/comida/ws", s.handleBOComidaAIWS)

		// New comida module endpoints (typed routes).
		r.With(s.requireBOSession, menusGate).Get("/comida/platos/categorias", s.handleBOComidaPlatoCategoriesList)
		r.With(s.requireBOSession, menusGate).Post("/comida/platos/categorias", s.handleBOComidaPlatoCategoriesCreate)
		r.With(s.requireBOSession, menusGate).Get("/comida/bebidas/categorias", s.handleBOComidaBebidaCategoriesList)
		r.With(s.requireBOSession, menusGate).Post("/comida/bebidas/categorias", s.handleBOComidaBebidaCategoriesCreate)
		r.With(s.requireBOSession, menusGate).Get("/comida/bebidas/categorias/check", s.handleBOComidaBebidaCategoriesCheck)

		// Unified category catalogue (per food type + global). Declared before the
		// /comida/{tipo} wildcard so the static "categorias" segment wins.
		r.With(s.requireBOSession, menusGate).Get("/comida/categorias", s.handleBOComidaCategoriesList)
		r.With(s.requireBOSession, menusGate).Post("/comida/categorias", s.handleBOComidaCategoryCreate)
		r.With(s.requireBOSession, menusGate).Patch("/comida/categorias/{id}", s.handleBOComidaCategoryPatch)
		r.With(s.requireBOSession, menusGate).Delete("/comida/categorias/{id}", s.handleBOComidaCategoryDelete)

		r.With(s.requireBOSession, menusGate).Get("/comida/{tipo}", s.handleBOComidaList)
		r.With(s.requireBOSession, menusGate).Get("/comida/{tipo}/{id}", s.handleBOComidaGet)
		r.With(s.requireBOSession, menusGate).Post("/comida/{tipo}", s.handleBOComidaCreate)
		r.With(s.requireBOSession, menusGate).Patch("/comida/{tipo}/{id}", s.handleBOComidaPatch)
		r.With(s.requireBOSession, menusGate).Delete("/comida/{tipo}/{id}", s.handleBOComidaDelete)

		// Stock control. Section gate is admin-only by default; role table can grant it later.
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/warehouses", s.handleBOStockWarehousesList)
		r.With(s.requireBOSession, withBOStockTimeout, stockWarehousesGate).Post("/stock/warehouses", s.handleBOStockWarehouseCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockWarehousesGate).Patch("/stock/warehouses/{id}", s.handleBOStockWarehousePatch)
		r.With(s.requireBOSession, withBOStockTimeout, stockWarehousesGate).Delete("/stock/warehouses/{id}", s.handleBOStockWarehouseDelete)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/categories", s.handleBOStockCategoriesList)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Post("/stock/categories", s.handleBOStockCategoryCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Patch("/stock/categories/{id}", s.handleBOStockCategoryPatch)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Delete("/stock/categories/{id}", s.handleBOStockCategoryDelete)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/items", s.handleBOStockItemsList)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/item-options", s.handleBOStockItemOptions)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Post("/stock/items", s.handleBOStockItemCreate)
		r.With(s.requireBOSession, stockItemsGate).Post("/stock/items/import", s.handleBOStockItemsImport)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Patch("/stock/items/{id}", s.handleBOStockItemPatch)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Delete("/stock/items/{id}", s.handleBOStockItemDelete)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Patch("/stock/items/{id}/targets", s.handleBOStockLevelTargetsPatch)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/items/{id}/units", s.handleBOStockItemUnitsList)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Post("/stock/items/{id}/units", s.handleBOStockItemUnitCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockItemsGate).Delete("/stock/items/{id}/units/{unitId}", s.handleBOStockItemUnitDelete)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/items/{id}/movements", s.handleBOStockItemMovementsList)
		r.With(s.requireBOSession, withBOStockTimeout).Post("/stock/items/{id}/movements", s.handleBOStockMovementCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockTransferGate).Post("/stock/transfers", s.handleBOStockTransferCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/summary", s.handleBOStockSummary)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/reconciliation", s.handleBOStockReconciliationGet)
		r.With(s.requireBOSession, withBOStockTimeout, stockSettingsGate).Post("/stock/reconciliation/rebuild", s.handleBOStockReconciliationRebuild)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/settings", s.handleBOStockSettingsGet)
		r.With(s.requireBOSession, withBOStockTimeout, stockSettingsGate).Patch("/stock/settings", s.handleBOStockSettingsPatch)
		r.With(s.requireBOSession, stockSettingsGate, s.requireBOStockAI).Post("/stock/settings/classify-seasonality", s.handleBOStockSeasonalityClassify)
		r.With(s.requireBOSession, withBOStockTimeout, stockSettingsGate).Get("/stock/roles/{slug}/permissions", s.handleBOStockRolePermissionsGet)
		r.With(s.requireBOSession, withBOStockTimeout, stockSettingsGate).Put("/stock/roles/{slug}/permissions", s.handleBOStockRolePermissionsPut)

		sheetsViewGate := s.requireBOStockPermission(stockPermissionSheetsView)
		sheetsManageGate := s.requireBOStockPermission(stockPermissionSheetsManage)
		sheetsPublishGate := s.requireBOStockPermission(stockPermissionSheetsPublish)
		sheetsDeleteGate := s.requireBOStockPermission(stockPermissionSheetsDelete)
		sheetsStepsGate := s.requireBOStockPermission(stockPermissionSheetsStepsManage)
		sheetsImagesAIGate := s.requireBOStockPermission(stockPermissionSheetsImagesAI)
		productionTypeGate := s.requireBOStockPermission(comidaPermissionProductionTypeManage)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Post("/comida/technical-sheets", s.handleBOTechnicalSheetCreate)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Post("/comida/technical-sheets/ensure", s.handleBOTechnicalSheetEnsureForProduct)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets/{id}", s.handleBOTechnicalSheetGet)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Patch("/comida/technical-sheets/{id}", s.handleBOTechnicalSheetPatch)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsPublishGate).Post("/comida/technical-sheets/{id}/publish", s.handleBOTechnicalSheetPublish)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets/{id}/allergens", s.handleBOTechnicalSheetAllergensGet)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Patch("/comida/technical-sheets/{id}/allergens", s.handleBOTechnicalSheetAllergensPatch)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets", s.handleBOTechnicalSheetList)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets/{id}/usage", s.handleBOTechnicalSheetUsage)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets/{id}/cost", s.handleBOTechnicalSheetCost)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Post("/comida/technical-sheets/{id}/duplicate", s.handleBOTechnicalSheetDuplicate)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsDeleteGate).Delete("/comida/technical-sheets/{id}", s.handleBOTechnicalSheetDelete)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets/{id}/components", s.handleBOTechnicalSheetComponentsList)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Post("/comida/technical-sheets/{id}/components", s.handleBOTechnicalSheetComponentCreate)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Patch("/comida/technical-sheets/{id}/components/{componentId}", s.handleBOTechnicalSheetComponentPatch)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsManageGate).Delete("/comida/technical-sheets/{id}/components/{componentId}", s.handleBOTechnicalSheetComponentDelete)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets/{id}/steps", s.handleBOTechnicalSheetStepsList)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsStepsGate).Post("/comida/technical-sheets/{id}/steps", s.handleBOTechnicalSheetStepCreate)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsStepsGate).Patch("/comida/technical-sheets/{id}/steps/{stepId}", s.handleBOTechnicalSheetStepPatch)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsStepsGate).Delete("/comida/technical-sheets/{id}/steps/{stepId}", s.handleBOTechnicalSheetStepDelete)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsStepsGate).Put("/comida/technical-sheets/{id}/steps/order", s.handleBOTechnicalSheetStepsReorder)
		r.With(s.requireBOSession, withBOStockTimeout, productionTypeGate).Patch("/comida/items/{id}/production-type", s.handleBOComidaProductionTypePatch)
		r.With(s.requireBOSession, withBOStockTimeout, productionTypeGate).Post("/comida/bulk-stock-link/preview", s.handleBOComidaBulkLinkPreview)
		r.With(s.requireBOSession, withBOStockTimeout, productionTypeGate).Post("/comida/bulk-stock-link/apply", s.handleBOComidaBulkLinkApply)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsImagesAIGate).Post("/comida/technical-sheets/{id}/steps/{stepId}/image-jobs", s.handleBOTechnicalSheetStepImageJobCreate)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsViewGate).Get("/comida/technical-sheets/{id}/steps/{stepId}/image-jobs", s.handleBOTechnicalSheetStepImageJobGet)
		r.With(s.requireBOSession, withBOStockTimeout, sheetsStepsGate).Post("/comida/technical-sheets/{id}/steps/{stepId}/image", s.handleBOTechnicalSheetStepImageUpload)
		r.With(s.requireBOSession, sheetsViewGate).Get("/comida/technical-sheets/ws", s.handleBOTechnicalSheetsWS)
		r.With(s.requireBOSession, withBOStockTimeout, stockCountPerformGate).Post("/stock/counts", s.handleBOStockCountCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockViewGate).Get("/stock/counts/{id}", s.handleBOStockCountGet)
		r.With(s.requireBOSession, withBOStockTimeout, stockCountCloseGate).Post("/stock/counts/{id}/close", s.handleBOStockCountClose)
		r.With(s.requireBOSession, withBOStockTimeout, stockRecipesViewGate).Get("/stock/recipes", s.handleBOStockRecipesList)
		r.With(s.requireBOSession, withBOStockTimeout, stockRecipesViewGate).Get("/stock/recipes/{id}", s.handleBOStockRecipeGet)
		r.With(s.requireBOSession, withBOStockTimeout, stockRecipesManageGate).Post("/stock/recipes", s.handleBOStockRecipeCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockRecipesManageGate).Patch("/stock/recipes/{id}", s.handleBOStockRecipePatch)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Patch("/stock/recipes/{id}/pricing", s.handleBOStockRecipePricingPatch)
		r.With(s.requireBOSession, withBOStockTimeout, stockRecipesManageGate).Delete("/stock/recipes/{id}", s.handleBOStockRecipeDelete)
		r.With(s.requireBOSession, withBOStockTimeout, stockRecipesViewGate).Post("/stock/recipes/{id}/production/preview", s.handleBOStockProductionPreview)
		r.With(s.requireBOSession, withBOStockTimeout, stockProductionGate).Post("/stock/recipes/{id}/production", s.handleBOStockProductionCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate, rolesAdminGate).Get("/stock/production-orders", s.handleBOProductionOrdersList)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate, rolesAdminGate).Get("/stock/production-labour/entries", s.handleBOProductionLabourEntries)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate, rolesAdminGate).Get("/stock/production-orders/{id}/labour", s.handleBOProductionLabourList)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate, rolesAdminGate).Post("/stock/production-orders/{id}/labour", s.handleBOProductionLabourCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate, rolesAdminGate).Delete("/stock/production-orders/{id}/labour/{allocationId}", s.handleBOProductionLabourDelete)
		r.With(s.requireBOSession, withBOStockTimeout, stockForecastGate).Put("/stock/affluence", s.handleBOStockAffluenceUpsert)
		r.With(s.requireBOSession, withBOStockTimeout, stockForecastGate).Get("/stock/forecast", s.handleBOStockForecast)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate).Get("/stock/vat-rates", s.handleBOStockVATList)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Post("/stock/vat-rates", s.handleBOStockVATCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Patch("/stock/vat-rates/{id}", s.handleBOStockVATPatch)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Delete("/stock/vat-rates/{id}", s.handleBOStockVATDelete)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Post("/stock/items/{id}/prices", s.handleBOStockItemPriceCreate)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate).Get("/stock/costing", s.handleBOStockCosting)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Get("/stock/labour-members", s.handleBOStockLabourMembers)
		r.With(s.requireBOSession, stockForecastGate, stockCostsViewGate, s.requireBOStockAI).Post("/stock/ai/recommendations", s.handleBOStockAIRecommendations)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate).Get("/stock/margin-scopes", s.handleBOStockMarginScopesList)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate).Get("/stock/margin-scopes/defaults", s.handleBOStockMarginScopeDefaults)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate).Get("/stock/margin-scopes/resolve", s.handleBOStockMarginScopeResolve)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsViewGate).Get("/stock/margin-scopes/targets", s.handleBOStockMarginTargets)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Put("/stock/margin-scopes", s.handleBOStockMarginScopePut)
		r.With(s.requireBOSession, withBOStockTimeout, stockCostsManageGate).Delete("/stock/margin-scopes/{id}", s.handleBOStockMarginScopeDelete)
		r.With(s.requireBOSession, stockOCRUploadGate).Get("/stock/documents", s.handleBOStockDocumentsList)
		r.With(s.requireBOSession, stockOCRUploadGate).Get("/stock/documents/{id}", s.handleBOStockDocumentGet)
		r.With(s.requireBOSession, stockOCRUploadGate).Get("/stock/documents/{id}/original", s.handleBOStockDocumentOriginalGet)
		r.With(s.requireBOSession, stockOCRConfirmGate).Delete("/stock/documents/{id}/original", s.handleBOStockDocumentOriginalDelete)
		r.With(s.requireBOSession, stockOCRUploadGate, s.requireBOStockAI).Post("/stock/documents/extract-text", s.handleBOStockDocumentTextExtract)
		r.With(s.requireBOSession, stockOCRUploadGate, s.requireBOStockAI).Post("/stock/documents/upload", s.handleBOStockDocumentUpload)
		r.With(s.requireBOSession, stockOCRUploadGate).Patch("/stock/documents/{id}/review", s.handleBOStockDocumentReview)
		r.With(s.requireBOSession, stockOCRUploadGate).Post("/stock/documents/{id}/reject", s.handleBOStockDocumentReject)
		r.With(s.requireBOSession, stockOCRConfirmGate).Post("/stock/documents/{id}/confirm-invoice", s.handleBOStockInvoiceConfirm)
		r.With(s.requireBOSession, stockOCRConfirmGate).Post("/stock/documents/{id}/confirm-recipe", s.handleBOStockRecipeDocumentConfirm)

	// Camera OCR from the "Nuevo articulo" modal: photograph a document and let
	// MiniMax vision turn it into structured stock-article data.
	r.With(s.requireBOSession, stockOCRUploadGate, s.requireBOStockAI).Post("/stock/ocr-scan", s.handleBOStockOCRScan)

		// POS. Paid checkout is authoritative boundary for stock deductions and covers.
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/bootstrap", s.handleBOPOSBootstrap)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/settings", s.handleBOPOSSettingsGet)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Patch("/pos/settings", s.handleBOPOSSettingsPatch)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/service-periods", s.handleBOPOSServicePeriodsList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Post("/pos/service-periods", s.handleBOPOSServicePeriodCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Patch("/pos/service-periods/{id}", s.handleBOPOSServicePeriodPatch)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Delete("/pos/service-periods/{id}", s.handleBOPOSServicePeriodDelete)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/categories", s.handleBOPOSCategoriesList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Post("/pos/categories", s.handleBOPOSCategoryCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Patch("/pos/categories/{id}", s.handleBOPOSCategoryPatch)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Delete("/pos/categories/{id}", s.handleBOPOSCategoryDelete)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/products", s.handleBOPOSProductsList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/products/{id}", s.handleBOPOSProductGet)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Post("/pos/products", s.handleBOPOSProductCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Patch("/pos/products/{id}", s.handleBOPOSProductPatch)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Delete("/pos/products/{id}", s.handleBOPOSProductDelete)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Post("/pos/products/import-preview", s.handleBOPOSImportPreview)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Post("/pos/products/import-confirm", s.handleBOPOSImportConfirm)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/products/{id}/stock-rules", s.handleBOPOSStockRulesGet)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posStockMappingGate).Put("/pos/products/{id}/stock-rules", s.handleBOPOSStockRulesPut)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posStockMappingGate).Get("/pos/stock-readiness", s.handleBOPOSStockReadiness)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posStockMappingGate).Get("/pos/stock-exceptions", s.handleBOPOSStockExceptionsList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posStockMappingGate).Post("/pos/stock-exceptions/replay", s.handleBOPOSStockExceptionsReplay)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/shifts/current", s.handleBOPOSShiftCurrent)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posShiftGate).Post("/pos/shifts/open", s.handleBOPOSShiftOpen)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posShiftGate).Post("/pos/shifts/{id}/close", s.handleBOPOSShiftClose)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/cash/summary", s.handleBOPOSCashSummary)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/cash/movements", s.handleBOPOSCashMovements)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCheckoutGate).Post("/pos/cash/movements", s.handleBOPOSCashMovementCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/cash/closures", s.handleBOPOSCashClosures)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/cash/closures/{id}", s.handleBOPOSCashClosureGet)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posShiftGate).Post("/pos/cash/closures", s.handleBOPOSCashClosureCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/cash-days", s.handleBOPOSCashDaysRange)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/cash-days/current", s.handleBOPOSCashDayCurrent)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/cash-days/{date}/tables", s.handleBOPOSCashDayTables)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posShiftGate).Post("/pos/cash-days", s.handleBOPOSCashDayOpen)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posShiftGate).Post("/pos/cash-days/{id}/close", s.handleBOPOSCashDayClose)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCheckoutGate).Post("/pos/cash-days/{date}/bulk-checkout", s.handleBOPOSCashDayBulkCheckout)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCheckoutGate).Post("/pos/drawer/open", s.handleBOPOSDrawerOpen)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/tags", s.handleBOPOSTagsList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCatalogGate).Post("/pos/tags", s.handleBOPOSTagCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/tickets", s.handleBOPOSTicketsList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, s.requireBOPOSViewOrFichajeAdmin).Get("/pos/tickets/hourly", s.handleBOPOSTicketsHourly)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, s.requireBOPOSViewOrFichajeAdmin).Get("/pos/tickets/series", s.handleBOPOSTicketsSeries)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/tickets/{id}", s.handleBOPOSTicketGet)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/visits", s.handleBOPOSVisitsList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/reservations/eligible", s.handleBOPOSReservationsEligible)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/reservations/{bookingId}/visit", s.handleBOPOSReservationVisit)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/visits/{id}", s.handleBOPOSVisitGet)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/visits", s.handleBOPOSVisitCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posVisitManageGate).Patch("/pos/visits/{id}", s.handleBOPOSVisitPatch)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posVisitManageGate).Post("/pos/visits/{id}/cancel", s.handleBOPOSVisitCancel)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posVisitManageGate).Post("/pos/visits/{id}/park", s.handleBOPOSVisitPark)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posVisitManageGate).Post("/pos/visits/{id}/merge", s.handleBOPOSVisitMerge)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Patch("/pos/visits/{id}/customer", s.handleBOPOSVisitCustomer)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/visits/{id}/tickets", s.handleBOPOSVisitTicketCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCheckoutGate).Post("/pos/visits/{id}/close", s.handleBOPOSVisitClose)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/tickets/{id}/void", s.handleBOPOSTicketVoid)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/tickets/{id}/lines", s.handleBOPOSLineCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Patch("/pos/tickets/{id}/lines/{lineId}", s.handleBOPOSLinePatch)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/tickets/{id}/lines/{lineId}/move", s.handleBOPOSLineMove)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posLineVoidGate).Post("/pos/tickets/{id}/lines/{lineId}/void", s.handleBOPOSLineVoid)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posDiscountGate).Post("/pos/tickets/{id}/discount", s.handleBOPOSDiscount)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posDiscountGate).Post("/pos/tickets/{id}/adjustments", s.handleBOPOSTicketAdjustment)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posDiscountGate).Post("/pos/tickets/{id}/lines/{lineId}/comp", s.handleBOPOSLineComp)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Patch("/pos/tickets/{id}/operator", s.handleBOPOSTicketOperator)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/tickets/{id}/tags", s.handleBOPOSTicketTagAttach)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/tickets/{id}/lines/{lineId}/tags", s.handleBOPOSLineTagAttach)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCheckoutGate).Post("/pos/tickets/{id}/checkout", s.handleBOPOSCheckout)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posRefundGate).Post("/pos/tickets/{id}/refunds", s.handleBOPOSRefund)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/covers", s.handleBOPOSCoversReport)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posCoversAdjustGate).Post("/pos/covers/adjustments", s.handleBOPOSCoverAdjustment)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/covers/reconciliation", s.handleBOPOSCoversReconciliation)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Post("/pos/covers/reconciliation/rebuild", s.handleBOPOSCoversRebuild)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/reports/sales", s.handleBOPOSSalesReport)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/reports/stock", s.handleBOPOSStockReport)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/reports/card-reconciliation", s.handleBOPOSCardReconciliation)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/reports/sales.csv", s.handleBOPOSExportSales)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posReportsGate).Get("/pos/accounting/export.csv", s.handleBOAccountingExport)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Get("/pos/health", s.handleBOPOSHealth)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posStockMappingGate).Post("/pos/stock-anomalies/{id}/resolve", s.handleBOPOSStockAnomalyResolve)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Get("/pos/roles/{slug}/permissions", s.handleBOPOSRolePermissionsGet)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Put("/pos/roles/{slug}/permissions", s.handleBOPOSRolePermissionsPut)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/kitchen/stations", s.handleBOPOSKitchenStationsList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posKitchenGate).Post("/pos/kitchen/stations", s.handleBOPOSKitchenStationCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posKitchenGate).Patch("/pos/kitchen/stations/{id}", s.handleBOPOSKitchenStationPatch)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/kitchen/routes", s.handleBOPOSKitchenRoutesList)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posKitchenGate).Post("/pos/kitchen/routes", s.handleBOPOSKitchenRouteCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posKitchenGate).Delete("/pos/kitchen/routes/{id}", s.handleBOPOSKitchenRouteDelete)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSellGate).Post("/pos/tickets/{id}/kitchen-dispatches", s.handleBOPOSKitchenDispatchCreate)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posViewGate).Get("/pos/kitchen/queue", s.handleBOPOSKitchenQueue)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posKitchenGate).Post("/pos/kitchen/dispatches/{id}/status", s.handleBOPOSKitchenDispatchStatus)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Get("/pos/activation-readiness", s.handleBOPOSActivationReadiness)
		r.With(s.requireBOSession, s.requireBOPOSFeature, withBOPOSTimeout, posSettingsGate).Post("/pos/activation-acceptances", s.handleBOPOSActivationAccept)

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
		r.With(s.requireBOSession, menusGate).Get("/group-menus-v2/ws", s.handleBOGroupMenusV2AIWS)
		r.With(s.requireBOSession, menusGate).Get("/group-menus-v2/{id}", s.handleBOGroupMenusV2Get)
		r.With(s.requireBOSession, menusGate).Patch("/group-menus-v2/{id}/basics", s.handleBOGroupMenusV2PatchBasics)
		r.With(s.requireBOSession, menusGate).Patch("/group-menus-v2/{id}/menu-type", s.handleBOGroupMenusV2PatchMenuType)
		r.With(s.requireBOSession, menusGate).Put("/group-menus-v2/{id}/sections", s.handleBOGroupMenusV2PutSections)
		r.With(s.requireBOSession, menusGate).Patch("/group-menus-v2/{id}/sections/{sectionId}/annotations", s.handleBOGroupMenusV2PatchSectionAnnotations)
		r.With(s.requireBOSession, menusGate).Get("/group-menus-v2/{id}/sections/{sectionId}/dishes", s.handleBOGroupMenusV2GetSectionDishes)
		r.With(s.requireBOSession, menusGate).Put("/group-menus-v2/{id}/sections/{sectionId}/dishes", s.handleBOGroupMenusV2PutSectionDishes)
		r.With(s.requireBOSession, menusGate).Patch("/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}", s.handleBOGroupMenusV2PatchSectionDish)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image", s.handleBOGroupMenusV2UploadSectionDishImage)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image/ai", s.handleBOGroupMenusV2GenerateSectionDishAIImage)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/preview-image", s.handleBOGroupMenusV2UploadMenuPreviewImage)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/preview-image/ai", s.handleBOGroupMenusV2GenerateMenuPreviewAIImage)
		r.With(s.requireBOSession, menusGate).Get("/group-menus-v2/{id}/slider", s.handleBOGroupMenusV2GetSlider)
		r.With(s.requireBOSession, menusGate).Patch("/group-menus-v2/{id}/slider", s.handleBOGroupMenusV2PatchSlider)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/slider/images", s.handleBOGroupMenusV2UploadSliderImage)
		r.With(s.requireBOSession, menusGate).Delete("/group-menus-v2/{id}/slider/images/{imageId}", s.handleBOGroupMenusV2DeleteSliderImage)
		r.With(s.requireBOSession, menusGate).Put("/group-menus-v2/{id}/slider/images", s.handleBOGroupMenusV2ReorderSliderImages)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/slider/images/ai", s.handleBOGroupMenusV2GenerateSliderAIImage)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/publish", s.handleBOGroupMenusV2Publish)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/toggle-active", s.handleBOGroupMenusV2ToggleActive)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/special-image", s.handleBOSpecialMenuImageUpload)
		r.With(s.requireBOSession, menusGate).Delete("/group-menus-v2/{id}", s.handleBOGroupMenusV2Delete)
		r.With(s.requireBOSession, menusGate).Get("/menus/selector", s.handleBOMenuSelectorGet)
		r.With(s.requireBOSession, menusGate).Get("/dishes-catalog/search", s.handleBODishesCatalogSearch)
		r.With(s.requireBOSession, menusGate).Post("/dishes-catalog/upsert", s.handleBODishesCatalogUpsert)
		r.With(s.requireBOSession, menusGate).Get("/group-menus-v2/{id}/same-day-booking", s.handleBODishSameDayBookingList)
		r.With(s.requireBOSession, menusGate).Post("/group-menus-v2/{id}/same-day-booking/{dishId}", s.handleBODishSameDayBookingCreate)
		r.With(s.requireBOSession, menusGate).Delete("/group-menus-v2/{id}/same-day-booking/{dishId}", s.handleBODishSameDayBookingDelete)

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
		r.With(s.requireBOSession, reservasGate).Post("/config/floors/date", s.handleBOConfigFloorsDateSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/salons", s.handleBOConfigSalonsList)
		r.With(s.requireBOSession, reservasGate).Post("/config/salons", s.handleBOConfigSalonsCreate)
		r.With(s.requireBOSession, reservasGate).Put("/config/salons/{salonId}", s.handleBOConfigSalonsUpdate)
		r.With(s.requireBOSession, reservasGate).Delete("/config/salons/{salonId}", s.handleBOConfigSalonsDelete)
		r.With(s.requireBOSession, reservasGate).Post("/config/salons/day-status", s.handleBOConfigSalonsDayStatusSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/salon-condesa", s.handleBOConfigSalonCondesaGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/salon-condesa", s.handleBOConfigSalonCondesaSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/daily-limit", s.handleBOConfigDailyLimitGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/daily-limit", s.handleBOConfigDailyLimitSet)

		r.With(s.requireBOSession, reservasGate).Get("/config/restaurant-info", s.handleBORestaurantInfoGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/restaurant-info", s.handleBORestaurantInfoSet)
		r.With(s.requireBOSession, reservasGate).Post("/config/check-website", s.handleBOWebsiteCheck)

		r.With(s.requireBOSession, reservasGate).Get("/config/mandatory-menus", s.handleBOMandatoryMenusGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/mandatory-menus", s.handleBOMandatoryMenusSave)

		// By-hour client split configuration (toggle + per-hour percentages).
		r.With(s.requireBOSession, reservasGate).Get("/config/hour-split", s.handleBOConfigHourSplitGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/hour-split", s.handleBOConfigHourSplitSet)
		r.With(s.requireBOSession, reservasGate).Post("/config/hour-split-percentages", s.handleBOConfigHourSplitPercentagesSet)

		// Location booking toggles (floor/salon reservation, default + per-date override).
		r.With(s.requireBOSession, reservasGate).Get("/config/location-booking", s.handleBOConfigLocationBookingGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/location-booking", s.handleBOConfigLocationBookingSet)

		// Widget settings (booking manager embed).
		r.With(s.requireBOSession, reservasGate).Get("/widget/settings", s.handleBOWidgetSettingsGet)
		r.With(s.requireBOSession, reservasGate).Put("/widget/settings", s.handleBOWidgetSettingsPut)

		r.With(s.requireBOSession, reservasGate).Get("/config/email-provider", s.handleBOEmailProviderGet)
		r.With(s.requireBOSession, reservasGate).Post("/config/email-provider", s.handleBOEmailProviderSet)

		// AI image config validity — any comida editor (used to gate the AI advisor).
		r.With(s.requireBOSession, menusGate).Get("/comida/ai-image/status", s.handleBOAIImageStatus)

		// AI image provider configuration — root only.
		r.With(s.requireBOSession, rootOnlyGate).Get("/config/ai-image/providers", s.handleBOAIImageProvidersGet)
		r.With(s.requireBOSession, rootOnlyGate).Get("/config/ai-image", s.handleBOAIImageConfigGet)
		r.With(s.requireBOSession, rootOnlyGate).Post("/config/ai-image", s.handleBOAIImageConfigSet)
		r.With(s.requireBOSession, rootOnlyGate).Get("/config/bunny-storage", s.handleBOBunnyStorageConfigGet)
		r.With(s.requireBOSession, rootOnlyGate).Post("/config/bunny-storage", s.handleBOBunnyStorageConfigSet)

		// MiniMax AI config (api key + model) — root only.
		r.With(s.requireBOSession, rootOnlyGate).Get("/config/minimax", s.handleBOMiniMaxConfigGet)
		r.With(s.requireBOSession, rootOnlyGate).Post("/config/minimax", s.handleBOMiniMaxConfigSet)

		// Legal pages CMS (aviso-legal, booking-policies, proteccion-datos).
		r.With(s.requireBOSession, ajustesGate).Get("/legal-pages", s.handleAdminLegalPageList)
		r.With(s.requireBOSession, ajustesGate).Get("/legal-pages/{slug}", s.handleAdminLegalPageGet)
		r.With(s.requireBOSession, ajustesGate).Post("/legal-pages/{slug}", s.handleAdminLegalPageUpsert)

		// Restaurant-level settings (integrations/branding).
		r.With(s.requireBOSession, ajustesGate).Get("/integrations", s.handleBOIntegrationsGet)
		r.With(s.requireBOSession, ajustesGate).Post("/integrations", s.handleBOIntegrationsSet)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Get("/integrations/uazapi/servers", s.handleBOUAZAPIServersList)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Post("/integrations/uazapi/servers", s.handleBOUAZAPIServersCreate)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Patch("/integrations/uazapi/servers/{id}", s.handleBOUAZAPIServersPatch)

		// WhatsApp bot personalization (per restaurant).
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Get("/bot/config", s.handleBOBotConfigGet)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Put("/bot/config", s.handleBOBotConfigPut)

		// WhatsApp bot settings per explicit restaurant id + prompt preview (root IA tab).
		r.With(s.requireBOSession, rootOnlyGate).Get("/bot/settings/{restaurantId}", s.handleBOBotSettingsGet)
		r.With(s.requireBOSession, rootOnlyGate).Put("/bot/settings/{restaurantId}", s.handleBOBotSettingsPut)
		r.With(s.requireBOSession, rootOnlyGate).Post("/bot/settings/{restaurantId}/preview", s.handleBOBotSettingsPreview)
		r.With(s.requireBOSession, ajustesGate).Get("/branding", s.handleBOBrandingGet)
		r.With(s.requireBOSession, ajustesGate).Post("/branding", s.handleBOBrandingSet)
		r.With(s.requireBOSession, ajustesGate).Post("/branding/logo", s.handleBOBrandingLogoUpload)
		r.With(s.requireBOSession, ajustesGate).Get("/website", s.handleBOPremiumWebsiteGet)
		r.With(s.requireBOSession, ajustesGate).Put("/website", s.handleBOPremiumWebsiteUpsert)
		r.With(s.requireBOSession, ajustesGate).Post("/website", s.handleBOPremiumWebsiteUpsert)
		r.With(s.requireBOSession, ajustesGate).Get("/website/templates", s.handleBOPremiumWebsiteTemplates)
		r.With(s.requireBOSession, ajustesGate).Get("/website/menu-templates", s.handleBOPremiumWebsiteMenuTemplatesGet)
		r.With(s.requireBOSession, ajustesGate).Put("/website/menu-templates", s.handleBOPremiumWebsiteMenuTemplatesUpsert)
		r.With(s.requireBOSession, ajustesGate).Get("/restaurant/pages/visibility", s.handleGetPageVisibility)
		r.With(s.requireBOSession, ajustesGate).Patch("/restaurant/pages/visibility", s.handlePageVisibilityPatch)
		r.With(s.requireBOSession, ajustesGate).Post("/website/ai-generate", s.handleBOPremiumWebsiteAIGenerate)
		r.With(s.requireBOSession, ajustesGate).Group(func(r chi.Router) {
			websiteBuilder.RegisterRoutes(r)
		})
		// Site builder routes (new visual editor)
		r.With(s.requireBOSession, ajustesGate).Group(func(r chi.Router) {
			RegisterSiteBuilderRoutes(r, s.db)
		})
		// Site builder realtime CRUD over WS.
		r.With(s.requireBOSession, ajustesGate).Get("/site-builder/ws", s.handleBOSiteBuilderWS)
		// Domain purchase billing (Stripe Checkout).
		r.With(s.requireBOSession, ajustesGate).Post("/site-builder/billing/checkout", s.handleBillingCheckout)
		// Cloudflare Registrar: domain search + purchase.
		r.With(s.requireBOSession, ajustesGate).Get("/site-builder/domains/search", s.handleRegistrarSearch)
		r.With(s.requireBOSession, ajustesGate).Post("/site-builder/domains/register", s.handleRegistrarRegister)
		r.With(s.requireBOSession, ajustesGate).Post("/site-builder/domains/provision", s.handleRegistrarProvision)
		// Instatic website-generator instance management (ensure/seed/publish/status)
		r.With(s.requireBOSession, ajustesGate).Group(func(r chi.Router) {
			s.instatic.RegisterInstaticRoutes(r)
		})
		r.With(s.requireBOSession, ajustesGate).Get("/domains/search", s.handleBOPremiumDomainsSearch)
		r.With(s.requireBOSession, ajustesGate).Post("/domains/quote", s.handleBOPremiumDomainsQuote)
		r.With(s.requireBOSession, ajustesGate).Post("/domains/register", s.handleBOPremiumDomainsRegister)
		r.With(s.requireBOSession, ajustesGate).Post("/domains/verify", s.handleBOPremiumDomainsVerify)

		// Tables premium endpoints.
		r.With(s.requireBOSession, reservasGate).Get("/tables", s.handleBOPremiumTablesList)
		r.With(s.requireBOSession, reservasGate).Post("/tables", s.handleBOPremiumTablesCreate)
		r.With(s.requireBOSession, reservasGate).Put("/tables", s.handleBOPremiumTablesUpdate)
		r.With(s.requireBOSession, reservasGate).Delete("/tables/{id}", s.handleBOPremiumTablesDelete)
		r.With(s.requireBOSession, reservasGate).Post("/tables/{id}/texture-image", s.handleBOPremiumTablesTextureImageUpload)
		r.With(s.requireBOSession, reservasGate).Get("/tables/ws", s.handleBOPremiumTablesWS)
		// Layout template (cross-day). Owns limit_area_template_points and
		// draw_elements_template for the given floor.
		r.With(s.requireBOSession, reservasGate).Get("/tables/template/{floorNumber}", s.handleBOPremiumTablesTemplateGet)
		r.With(s.requireBOSession, reservasGate).Post("/tables/template/{floorNumber}", s.handleBOPremiumTablesTemplateSave)
		r.With(s.requireBOSession, reservasGate).Delete("/tables/template/{floorNumber}", s.handleBOPremiumTablesTemplateDelete)

		// Members and role administration.
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members", s.handleBOMembersList)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members", s.handleBOMemberCreate)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}", s.handleBOMemberGet)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Patch("/members/{id}", s.handleBOMemberPatch)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Get("/members/{id}/compensations", s.handleBOMemberCompensationsList)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/compensations", s.handleBOMemberCompensationCreate)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Patch("/members/{id}/compensations/{compensationId}", s.handleBOMemberCompensationPatch)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Delete("/members/{id}/compensations/{compensationId}", s.handleBOMemberCompensationDelete)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Put("/members/{id}/phone", s.handleBOMemberPhonePut)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/whatsapp/verification/send", s.handleBOMemberWhatsAppVerificationSend)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/whatsapp/verification/confirm", s.handleBOMemberWhatsAppVerificationConfirm)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/phone/verification/send", s.handleBOMemberWhatsAppVerificationSend)
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/{id}/phone/verification/confirm", s.handleBOMemberWhatsAppVerificationConfirm)
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
		r.With(s.requireBOSession, miembrosGate, rolesAdminGate).Post("/members/whatsapp/send", s.handleBOMembersWhatsAppSend)
		r.With(s.requireBOSession, rootOnlyGate).Post("/members/whatsapp/subscribe", s.handleBOMembersWhatsAppSubscribe)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Post("/members/whatsapp/connect", s.handleBOMembersWhatsAppConnect)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Get("/members/whatsapp/connection", s.handleBOMembersWhatsAppConnectionStatus)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Get("/members/whatsapp/ws", s.handleBOMembersWhatsAppWS)
		r.With(s.requireBOSession, ajustesGate, rolesAdminGate).Post("/members/whatsapp/disconnect", s.handleBOMembersWhatsAppDisconnect)
		r.With(s.requireBOSession, rootOnlyGate).Post("/members/whatsapp/cancel", s.handleBOMembersWhatsAppCancel)

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
		r.With(s.requireBOSession, fichajeGate, rolesAdminGate).Get("/fichaje/labour-cost", s.handleBOFichajeLabourCost)
		r.With(s.requireBOSession, fichajeGate, rolesAdminGate).Get("/fichaje/hourly-costs", s.handleBOFichajeHourlyCosts)

		r.With(s.requireBOSession, horariosGate).Get("/horarios", s.handleBOHorariosList)
		r.With(s.requireBOSession, horariosGate).Post("/horarios", s.handleBOHorariosAssign)
		r.With(s.requireBOSession, horariosGate).Put("/horarios/{id}", s.handleBOHorariosUpdate)
		r.With(s.requireBOSession, horariosGate).Delete("/horarios/{id}", s.handleBOHorariosDelete)
		r.With(s.requireBOSession, horariosGate).Get("/horarios/month", s.handleBOHorariosMonth)
		r.With(s.requireBOSession, horariosGate).Get("/horarios/calendar", s.handleBOHorariosCalendar)
		r.With(s.requireBOSession, horariosGate).Get("/horarios/member-range", s.handleBOHorariosMemberRange)
		r.With(s.requireBOSession, fichajeGate).Get("/horarios/my-schedule", s.handleBOHorariosMySchedule)

		// Forky AI assistant (WebSocket chat, any logged-in user).
		r.With(s.requireBOSession).Get("/assistant/ws", s.handleBOAssistantWS)

		// Invoices management
		r.With(s.requireBOSession, facturasGate).Get("/invoices", s.handleBOInvoicesList)
		r.With(s.requireBOSession, facturasGate).Get("/invoices/{id}", s.handleBOInvoiceGet)
		r.With(s.requireBOSession, facturasGate).Post("/invoices", s.handleBOInvoiceCreate)
		r.With(s.requireBOSession, facturasGate).Put("/invoices/{id}", s.handleBOInvoiceUpdate)
		r.With(s.requireBOSession, facturasGate).Delete("/invoices/{id}", s.handleBOInvoiceDelete)
		r.With(s.requireBOSession, facturasGate).Post("/invoices/{id}/send", s.handleBOInvoiceSend)
		r.With(s.requireBOSession, facturasGate).Get("/invoices/{id}/pdf", s.handleBOInvoicePdf)
		r.With(s.requireBOSession, facturasGate).Post("/invoices/{id}/upload-image", s.handleBOInvoiceUploadImage)
		r.With(s.requireBOSession, facturasGate).Get("/invoices/search-reservation", s.handleBOInvoicesSearchReservation)
		r.With(s.requireBOSession, facturasGate).Get("/invoices/{id}/comments", s.handleBOInvoiceCommentsList)
		r.With(s.requireBOSession, facturasGate).Post("/invoices/{id}/comments", s.handleBOInvoiceCommentCreate)
		r.With(s.requireBOSession, facturasGate).Put("/invoices/{id}/comments/{commentId}", s.handleBOInvoiceCommentUpdate)
		r.With(s.requireBOSession, facturasGate).Delete("/invoices/{id}/comments/{commentId}", s.handleBOInvoiceCommentDelete)

		// Analytics V1 uses an idempotent selected-range rebuild; it has no outbox.
		r.With(s.requireBOSession, statisticsGate).Get("/analytics/overview", s.handleBOAnalyticsOverview)
		r.With(s.requireBOSession, statisticsGate).Post("/analytics/refresh", s.handleBOAnalyticsRefresh)

		// Platform (superadmin) endpoints — cross-tenant management.
		// All gated by requireBOSuperadmin (is_superadmin=1 only).
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/dashboard", s.handlePlatformDashboard)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/restaurants", s.handlePlatformRestaurantsList)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/restaurants", s.handlePlatformRestaurantCreate)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Patch("/platform/restaurants/{id}", s.handlePlatformRestaurantPatch)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/restaurants/{id}/deactivate", s.handlePlatformRestaurantDeactivate)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/restaurants/{id}/activate", s.handlePlatformRestaurantActivate)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/users", s.handlePlatformUsersList)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/users", s.handlePlatformUserCreate)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Patch("/platform/users/{id}", s.handlePlatformUserPatch)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/users/{id}/password", s.handlePlatformUserPasswordReset)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/users/{id}/revoke-sessions", s.handlePlatformUserRevokeSessions)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/users/{id}/assign", s.handlePlatformUserAssign)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Delete("/platform/users/{id}/assign/{restaurantId}", s.handlePlatformUserUnassign)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/subscriptions", s.handlePlatformSubscriptionsList)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/subscriptions/{id}/toggle", s.handlePlatformSubscriptionToggle)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/whatsapp", s.handlePlatformWhatsAppList)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/whatsapp/{id}/renew-qr", s.handlePlatformWhatsAppRenewQR)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/whatsapp/{id}/disconnect", s.handlePlatformWhatsAppDisconnect)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/uazapi-servers", s.handlePlatformUAZAPIServersList)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/domains", s.handlePlatformDomainsList)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Get("/platform/stripe/payments", s.handlePlatformStripePaymentsList)
		r.With(s.requireBOSession, s.requireBOSuperadmin).Post("/platform/stripe/refund", s.handlePlatformStripeRefund)
	})

	r.Get("/public/website-builder/render/{kind}", s.handleWebsiteBuilderRenderFragment)

	// Stripe webhook (signature-authenticated, not session).
	r.Post("/stripe/webhook", s.handleStripeWebhook)

	// Public booking JSON API — uses own tenant resolution via DEFAULT_RESTAURANT_ID fallback.
	r.Get("/public/booking", s.handlePublicBookingGet)
	r.Post("/public/booking/confirm", s.handlePublicBookingConfirm)
	r.Post("/public/booking/cancel", s.handlePublicBookingCancel)
	r.Post("/public/booking/rice", s.handlePublicBookingRice)
	r.Get("/public/booking-policies", s.handlePublicBookingPolicies)
	r.Get("/public/legal-page", s.handlePublicLegalPageGet)

	// Embeddable booking widget API under /widget/* — accepts ?restaurant_id=.
	// Same-origin on published restaurant sites (instatic proxy passes /widget/*
	// through to here), so the page CSP needs no cross-origin connect-src.
	r.Route("/widget", func(r chi.Router) {
		s.RegisterWidgetRoutes(r)
	})

	// Multi-tenant WhatsApp bot webhook (UAZAPI). Tenant resolved by the
	// instance token in the payload, not by Host header.
	r.Post("/bot/webhook", s.handleBotWebhook)
	r.Post("/bot/webhook/evolution/{secret}", s.handleBotWebhookEvolution)

	// Everything below is restaurant-scoped.
	r.Group(func(r chi.Router) {
		r.Use(s.withRestaurant)

		// Public endpoints (used by the Preact client).
		r.Get("/menu-visibility", s.handleMenuVisibility)
		r.Get("/reservations/closed-days", s.handleReservationsClosedDays)
		r.Get("/reservations/rice-types", s.handleReservationsRiceTypes)
		r.Get("/reservations/month-availability", s.handleReservationsMonthAvailability)
		r.Get("/reservations/two-top-availability", s.handleFetchMesasDeDos)
		r.Get("/reservations/hour-data", s.handleGetHourData)
		r.Get("/reservations/day-context", s.handleGetReservationDayContext)
		r.Get("/reservations/mandatory-menus", s.handlePublicMandatoryMenus)
		r.With(s.requireAdmin).Post("/menu-visibility", s.handleMenuVisibilityToggle)
		r.Get("/menus/public", s.handlePublicMenus)
		r.Get("/menus/sidebar", s.handlePublicMenusSidebar)
		r.Get("/menus/home", s.handlePublicMenusHome)
		r.Get("/menus/{menuID}", s.handlePublicMenuByRouteID)
		r.Get("/menus/dia", s.handleMenuDia)
		r.Get("/menus/finde", s.handleMenuFinde)
		r.Get("/postres", s.handlePostres)
		r.Get("/vinos", s.handleVinos)
		// Forky AI assistant for the public site (anonymous, session_token + rate limit).
		r.Get("/assistant/ws", s.handlePublicAssistantWS)
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
			r.With(s.requireAdmin).Post("/toggleMenuVisibility.php", s.handleToggleMenuVisibilityLegacy)
		})

		// Legacy group menus backend.
		r.Route("/menuDeGruposBackend", func(r chi.Router) {
			r.Get("/getAllMenus.php", s.handleGetAllGroupMenus)
			r.Get("/getMenu.php", s.handleGetGroupMenu)
			r.Get("/getActiveMenusForDisplay", s.handleGetActiveGroupMenusForDisplayRich)
			r.Get("/getActiveMenusForDisplay.php", s.handleGetActiveGroupMenusForDisplay)
			r.With(s.requireAdmin).Post("/addMenu.php", s.handleAddGroupMenu)
			r.With(s.requireAdmin).Post("/updateMenu.php", s.handleUpdateGroupMenu)
			r.With(s.requireAdmin).Put("/updateMenu.php", s.handleUpdateGroupMenu)
			r.With(s.requireAdmin).Post("/toggleActive.php", s.handleToggleGroupMenuActive)
			r.With(s.requireAdmin).Post("/deleteMenu.php", s.handleDeleteGroupMenu)
			r.With(s.requireAdmin).Delete("/deleteMenu.php", s.handleDeleteGroupMenu)
		})

		// Public availability helpers used by reservas.php.
		r.Post("/fetch_daily_limit.php", s.handleFetchDailyLimit)
		r.Post("/fetch_mesas_de_dos.php", s.handleFetchMesasDeDos)
		r.Get("/get_reservation_day_context.php", s.handleGetReservationDayContext)

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
		r.Get("/reservations/group-menus", s.handleGetValidMenusForPartySize)

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

		// Public booking creation (canonical route + legacy alias).
		r.Post("/bookings/front", s.handleInsertBookingFront)
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

func (s *Server) resolveAllowedOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}

	configured := strings.TrimSpace(s.cfg.CORSAllowOrigins)
	if configured == "" || configured == "*" {
		return "*"
	}

	for _, candidate := range strings.Split(configured, ",") {
		allowed := strings.TrimSpace(candidate)
		if allowed == "" {
			continue
		}
		if allowed == "*" {
			return "*"
		}
		if strings.EqualFold(allowed, origin) {
			return origin
		}
	}
	return ""
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
	Descripcion        string   `json:"descripcion"`
	Alergenos          []string `json:"alergenos"`
	DescripcionEnglish string   `json:"descripcion_english,omitempty"`
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

	q := "SELECT NUM, DESCRIPCION, alergenos FROM " + table + " WHERE restaurant_id = ? AND TIPO = ? AND active = 1 ORDER BY NUM ASC"
	rows, err := s.db.QueryContext(r.Context(), q, restaurantID, dishType)
	if err != nil {
		return nil, errors.New("Error consultando " + table)
	}
	defer rows.Close()

	var dishes []Dish
	var ids []int64
	for rows.Next() {
		var num int64
		var descripcion string
		var alergenosRaw sql.NullString
		if err := rows.Scan(&num, &descripcion, &alergenosRaw); err != nil {
			return nil, errors.New("Error leyendo " + table)
		}
		dishes = append(dishes, Dish{
			Descripcion: descripcion,
			Alergenos:   parseAlergenos(alergenosRaw),
		})
		ids = append(ids, num)
	}
	if all, err := s.loadTranslations(r.Context(), restaurantID, table, ids, translationLang); err == nil {
		for i := range dishes {
			if en := translationOr(all[ids[i]], "descripcion"); en != "" {
				dishes[i].DescripcionEnglish = en
			}
		}
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

	rows, err := s.db.QueryContext(r.Context(), "SELECT NUM, DESCRIPCION, alergenos FROM POSTRES WHERE restaurant_id = ? AND active = 1 ORDER BY NUM ASC", restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando POSTRES")
		return
	}
	defer rows.Close()

	var postres []Dish
	var ids []int64
	for rows.Next() {
		var num int64
		var descripcion string
		var alergenosRaw sql.NullString
		if err := rows.Scan(&num, &descripcion, &alergenosRaw); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo POSTRES")
			return
		}
		postres = append(postres, Dish{
			Descripcion: descripcion,
			Alergenos:   parseAlergenos(alergenosRaw),
		})
		ids = append(ids, num)
	}

	if all, err := s.loadTranslations(r.Context(), restaurantID, entityPostres, ids, translationLang); err == nil {
		for i := range postres {
			if en := translationOr(all[ids[i]], "descripcion"); en != "" {
				postres[i].DescripcionEnglish = en
			}
		}
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
		Num                       int     `json:"num"`
		Nombre                    string  `json:"nombre"`
		Precio                    float64 `json:"precio"`
		Descripcion               string  `json:"descripcion"`
		Bodega                    string  `json:"bodega"`
		DenominacionOrigen        string  `json:"denominacion_origen"`
		Tipo                      string  `json:"tipo"`
		Graduacion                float64 `json:"graduacion"`
		Anyo                      string  `json:"anyo"`
		Active                    int     `json:"active"`
		HasFoto                   bool    `json:"has_foto"`
		FotoURL                   *string `json:"foto_url,omitempty"`
		NombreEnglish             string  `json:"nombre_english,omitempty"`
		DescripcionEnglish        string  `json:"descripcion_english,omitempty"`
		BodegaEnglish             string  `json:"bodega_english,omitempty"`
		DenominacionOrigenEnglish string  `json:"denominacion_origen_english,omitempty"`
		TipoEnglish               string  `json:"tipo_english,omitempty"`
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
				u := s.bunnyPullURL(r.Context(), restaurantID, fotoPath.String)
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

	if len(vinos) > 0 {
		ids := make([]int64, 0, len(vinos))
		for _, v := range vinos {
			ids = append(ids, int64(v.Num))
		}
		if all, err := s.loadTranslations(r.Context(), restaurantID, entityVinos, ids, translationLang); err == nil {
			for i := range vinos {
				m := all[int64(vinos[i].Num)]
				if m == nil {
					continue
				}
				vinos[i].NombreEnglish = translationOr(m, "nombre")
				vinos[i].DescripcionEnglish = translationOr(m, "descripcion")
				vinos[i].BodegaEnglish = translationOr(m, "bodega")
				vinos[i].DenominacionOrigenEnglish = translationOr(m, "denominacion_origen")
				vinos[i].TipoEnglish = translationOr(m, "tipo")
			}
		}
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
