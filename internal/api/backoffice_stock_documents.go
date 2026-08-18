package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"preactvillacarmen/internal/httpx"
)

const stockDocumentMaxBytes = 10 << 20

type stockExtractedLine struct {
	Description string  `json:"description"`
	Code        string  `json:"code"`
	Quantity    float64 `json:"quantity"`
	Unit        string  `json:"unit"`
	UnitPrice   float64 `json:"unitPrice"`
	Total       float64 `json:"total"`
	WastePct    float64 `json:"wastePct"`
	Confidence  float64 `json:"confidence"`
}

type stockDocumentExtraction struct {
	SupplierName   string               `json:"supplierName"`
	DocumentNumber string               `json:"documentNumber"`
	DocumentDate   string               `json:"documentDate"`
	Name           string               `json:"name"`
	YieldQuantity  float64              `json:"yieldQuantity"`
	YieldUnit      string               `json:"yieldUnit"`
	Confidence     float64              `json:"confidence"`
	Lines          []stockExtractedLine `json:"lines"`
	Components     []stockExtractedLine `json:"components"`
}

func stockDocumentPrompt(documentType string) (string, string) {
	if documentType == "RECIPE" {
		return "Extract restaurant technical recipe sheets accurately. Never invent missing values.", `Read every page and return strict JSON only: {"name":"","yieldQuantity":0,"yieldUnit":"","confidence":0,"components":[{"description":"","code":"","quantity":0,"unit":"","wastePct":0,"confidence":0}]}. Use decimal numbers. confidence is 0..1. Keep uncertain text in description.`
	}
	return "Extract restaurant supplier invoices accurately. Never invent missing values. Ignore tax summary rows unless they are purchased products.", `Read every page and return strict JSON only: {"supplierName":"","documentNumber":"","documentDate":"YYYY-MM-DD or empty","confidence":0,"lines":[{"description":"","code":"","quantity":0,"unit":"","unitPrice":0,"total":0,"confidence":0}]}. Use decimal numbers. confidence is 0..1. Keep uncertain text in description.`
}

func normalizeStockDocumentType(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	return value, value == "INVOICE" || value == "RECIPE"
}

func stockDocumentMediaType(payload []byte) (string, bool) {
	mediaType := http.DetectContentType(payload)
	switch mediaType {
	case "image/jpeg", "image/png", "image/webp", "application/pdf":
		return mediaType, true
	default:
		return mediaType, false
	}
}

func stockAliasDescription(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 255 {
		value = value[:255]
	}
	return value
}

func stockDocumentObjectPath(restaurantID int, filename, mediaType string) string {
	ext := ".bin"
	switch mediaType {
	case "application/pdf":
		ext = ".pdf"
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	}
	_ = filename // ponytail: original name is metadata; never use it as object identity.
	return path.Join("stock-documents", strconv.Itoa(restaurantID), uuid.NewString()+ext)
}

var stockDocumentFilenameUnsafe = regexp.MustCompile(`[^[:alnum:]. _-]+`)

func stockDocumentFilename(value string) string {
	value = stockDocumentFilenameUnsafe.ReplaceAllString(path.Base(strings.TrimSpace(value)), "_")
	if value == "" || value == "." {
		return "document"
	}
	if len(value) > 255 {
		value = value[:255]
	}
	return value
}

func stockDocumentHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func stockDocumentLines(extraction stockDocumentExtraction) []stockExtractedLine {
	if len(extraction.Lines) > 0 {
		return extraction.Lines
	}
	return extraction.Components
}

func (s *Server) saveBOStockDocumentExtraction(r *http.Request, restaurantID, userID int, documentType, source, fileHash, rawText string, extraction stockDocumentExtraction) (int64, error) {
	return s.saveBOStockDocumentExtractionWithFile(r, restaurantID, userID, documentType, source, fileHash, rawText, extraction, "", "", 0, "", nil)
}

func (s *Server) saveBOStockDocumentExtractionWithFile(r *http.Request, restaurantID, userID int, documentType, source, fileHash, rawText string, extraction stockDocumentExtraction, objectPath, contentType string, size int64, filename string, retentionUntil any) (int64, error) {
	return s.saveBOStockDocumentExtractionWithFileModel(r, restaurantID, userID, documentType, source, fileHash, rawText, extraction, s.resolveMiniMaxModel(r.Context(), restaurantID, ""), objectPath, contentType, size, filename, retentionUntil)
}

func (s *Server) saveBOStockDocumentExtractionWithFileModel(r *http.Request, restaurantID, userID int, documentType, source, fileHash, rawText string, extraction stockDocumentExtraction, model, objectPath, contentType string, size int64, filename string, retentionUntil any) (int64, error) {
	if extraction.DocumentDate != "" {
		if _, err := time.Parse("2006-01-02", extraction.DocumentDate); err != nil {
			extraction.DocumentDate = ""
		}
	}
	if extraction.Confidence < 0 {
		extraction.Confidence = 0
	}
	if extraction.Confidence > 1 {
		extraction.Confidence = 1
	}
	raw, err := json.Marshal(extraction)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_document_scans (restaurant_id,document_type,source,file_path,storage_provider,storage_bucket,content_type,size_bytes,original_filename,retention_until,file_hash,raw_text,status,supplier_name,document_number,document_date,raw_extraction,model,confidence,uploaded_by) VALUES (?,?,?,IF(?='',NULL,?),IF(?='',NULL,'BUNNY_PRIVATE'),IF(?='',NULL,?),?,?,?,?,?,?,'NEEDS_REVIEW',?,?,NULLIF(?,''),?,?,?,?)`, restaurantID, documentType, source, objectPath, stockNullableString(objectPath), objectPath, objectPath, stockNullableString(s.bunnyCreds(r.Context(), restaurantID).PrivateStorageZone), stockNullableString(contentType), stockNullableInt64(size), stockNullableString(filename), retentionUntil, stockNullableString(fileHash), stockNullableString(rawText), stockNullableString(extraction.SupplierName), stockNullableString(extraction.DocumentNumber), extraction.DocumentDate, raw, model, extraction.Confidence, userID)
	if err != nil {
		return 0, err
	}
	scanID, _ := res.LastInsertId()
	for index, line := range stockDocumentLines(extraction) {
		description := strings.TrimSpace(line.Description)
		if description == "" {
			continue
		}
		var matchedItemID, matchedUnitID sql.NullInt64
		if extraction.SupplierName != "" {
			_ = tx.QueryRowContext(r.Context(), `SELECT stock_item_id,stock_unit_id FROM stock_supplier_aliases WHERE restaurant_id=? AND supplier_name=? AND (supplier_code=? OR (?='' AND normalized_description=?)) ORDER BY supplier_code=? DESC LIMIT 1`, restaurantID, extraction.SupplierName, line.Code, line.Code, stockAliasDescription(description), line.Code).Scan(&matchedItemID, &matchedUnitID)
		}
		status := "NEEDS_MATCH"
		if matchedItemID.Valid && matchedUnitID.Valid {
			status = "OK"
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_document_lines (restaurant_id,document_scan_id,line_no,raw_description,raw_code,raw_qty,raw_unit,raw_unit_price,raw_total,matched_stock_item_id,matched_unit_id,match_confidence,status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, restaurantID, scanID, index+1, description, stockNullableString(line.Code), stockNullableFloat(line.Quantity), stockNullableString(line.Unit), stockNullableFloat(line.UnitPrice), stockNullableFloat(line.Total), matchedItemID, matchedUnitID, stockNullableFloat(line.Confidence), status)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return scanID, nil
}

func stockNullableInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func stockNullableFloat(value float64) any {
	if value == 0 {
		return nil
	}
	return value
}

func (s *Server) handleBOStockDocumentUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, stockDocumentMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(stockDocumentMaxBytes); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid multipart document")
		return
	}
	documentType, valid := normalizeStockDocumentType(r.FormValue("documentType"))
	if !valid {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid document type")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Document file is required")
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, stockDocumentMaxBytes+1))
	if err != nil || len(payload) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Document could not be read")
		return
	}
	if len(payload) > stockDocumentMaxBytes {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "Document exceeds 10 MB")
		return
	}
	mediaType, allowed := stockDocumentMediaType(payload)
	if !allowed {
		httpx.WriteError(w, http.StatusUnsupportedMediaType, "Use PDF, JPG, PNG or WebP")
		return
	}
	fileHash := stockDocumentHash(payload)
	var existingID int64
	err = s.db.QueryRowContext(r.Context(), `SELECT id FROM stock_document_scans WHERE restaurant_id=? AND file_hash=? LIMIT 1`, a.ActiveRestaurantID, fileHash).Scan(&existingID)
	if err == nil {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Document already uploaded", "id": existingID})
		return
	}
	if err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking document")
		return
	}
	objectPath := ""
	filename := stockDocumentFilename(header.Filename)
	var retentionUntil any
	if s.bunnyPrivateConfigured(r.Context(), a.ActiveRestaurantID) {
		objectPath = stockDocumentObjectPath(a.ActiveRestaurantID, filename, mediaType)
		if err = s.bunnyPrivatePut(r.Context(), a.ActiveRestaurantID, objectPath, payload, mediaType); err != nil {
			httpx.WriteError(w, http.StatusBadGateway, "Private document storage failed")
			return
		}
		retentionUntil = time.Now().AddDate(0, 0, s.cfg.StockDocumentRetentionDays).Format("2006-01-02")
	}
	system, prompt := stockDocumentPrompt(documentType)
	var extraction stockDocumentExtraction
	rawText := ""
	model := s.resolveMiniMaxModel(r.Context(), a.ActiveRestaurantID, "")
	provider := stockOCRProviderName(s.cfg.StockOCRProvider)
	if provider == "paddleocr" {
		result, extractErr := newPaddleOCRExtractor(s.cfg).Extract(r.Context(), documentType, mediaType, filename, payload)
		if extractErr != nil {
			if objectPath != "" {
				_ = s.bunnyPrivateDelete(r.Context(), a.ActiveRestaurantID, objectPath)
			}
			httpx.WriteError(w, http.StatusBadGateway, "PaddleOCR extraction failed")
			return
		}
		extraction = result.Extraction
		rawText = result.RawText
		model = result.Model
	} else if provider == "minimax" {
		if err := s.minimaxJSONContent(stockAIFeatureContext(r.Context(), "ocr_multimodal"), a.ActiveRestaurantID, system, minimaxDocumentContent(mediaType, payload, prompt), &extraction); err != nil {
			if objectPath != "" {
				_ = s.bunnyPrivateDelete(r.Context(), a.ActiveRestaurantID, objectPath)
			}
			httpx.WriteError(w, http.StatusBadGateway, "AI extraction failed")
			return
		}
	} else {
		if objectPath != "" {
			_ = s.bunnyPrivateDelete(r.Context(), a.ActiveRestaurantID, objectPath)
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Unsupported stock OCR provider")
		return
	}
	id, err := s.saveBOStockDocumentExtractionWithFileModel(r, a.ActiveRestaurantID, a.User.ID, documentType, "UPLOAD", fileHash, rawText, extraction, model, objectPath, mediaType, int64(len(payload)), filename, retentionUntil)
	if err != nil {
		if objectPath != "" {
			_ = s.bunnyPrivateDelete(r.Context(), a.ActiveRestaurantID, objectPath)
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving extraction")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id, "model": model, "extraction": extraction, "needsReview": true, "originalRetained": objectPath != ""})
}

func (s *Server) handleBOStockDocumentsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,document_type,source,status,COALESCE(supplier_name,''),COALESCE(document_number,''),COALESCE(DATE_FORMAT(document_date,'%Y-%m-%d'),''),COALESCE(confidence,0),COALESCE(model,''),created_at,file_path IS NOT NULL AND original_deleted_at IS NULL FROM stock_document_scans WHERE restaurant_id=? ORDER BY created_at DESC,id DESC LIMIT 100`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading documents")
		return
	}
	defer rows.Close()
	documents := []map[string]any{}
	for rows.Next() {
		var id int64
		var documentType, source, status, supplier, number, date, model string
		var confidence float64
		var createdAt any
		var originalAvailable int
		if err := rows.Scan(&id, &documentType, &source, &status, &supplier, &number, &date, &confidence, &model, &createdAt, &originalAvailable); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading documents")
			return
		}
		documents = append(documents, map[string]any{"id": id, "documentType": documentType, "source": source, "status": status, "supplierName": supplier, "documentNumber": number, "documentDate": date, "confidence": confidence, "model": model, "createdAt": createdAt, "originalAvailable": originalAvailable != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "documents": documents})
}

func (s *Server) handleBOStockDocumentGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var documentType, source, status, supplier, number, date, model string
	var confidence float64
	var extraction json.RawMessage
	var originalAvailable int
	err := s.db.QueryRowContext(r.Context(), `SELECT document_type,source,status,COALESCE(supplier_name,''),COALESCE(document_number,''),COALESCE(DATE_FORMAT(document_date,'%Y-%m-%d'),''),COALESCE(confidence,0),COALESCE(model,''),COALESCE(raw_extraction,JSON_OBJECT()),file_path IS NOT NULL AND original_deleted_at IS NULL FROM stock_document_scans WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&documentType, &source, &status, &supplier, &number, &date, &confidence, &model, &extraction, &originalAvailable)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Document not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading document")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT l.id,l.line_no,l.raw_description,COALESCE(l.raw_code,''),COALESCE(l.raw_qty,0),COALESCE(l.raw_unit,''),COALESCE(l.raw_unit_price,0),COALESCE(l.raw_total,0),l.matched_stock_item_id,l.matched_unit_id,COALESCE(l.match_confidence,0),l.status,COALESCE(i.name,'') FROM stock_document_lines l LEFT JOIN stock_items i ON i.restaurant_id=l.restaurant_id AND i.id=l.matched_stock_item_id WHERE l.restaurant_id=? AND l.document_scan_id=? ORDER BY l.line_no`, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading document lines")
		return
	}
	defer rows.Close()
	lines := []map[string]any{}
	for rows.Next() {
		var lineID int64
		var lineNo int
		var description, code, unit, lineStatus, itemName string
		var quantity, unitPrice, total, lineConfidence float64
		var itemID, unitID sql.NullInt64
		if err := rows.Scan(&lineID, &lineNo, &description, &code, &quantity, &unit, &unitPrice, &total, &itemID, &unitID, &lineConfidence, &lineStatus, &itemName); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading document lines")
			return
		}
		lines = append(lines, map[string]any{"id": lineID, "lineNo": lineNo, "description": description, "code": code, "quantity": quantity, "unit": unit, "unitPrice": unitPrice, "total": total, "matchedStockItemId": stockNullableDBInt(itemID), "matchedUnitId": stockNullableDBInt(unitID), "matchedStockItemName": itemName, "confidence": lineConfidence, "status": lineStatus})
	}
	var raw any
	_ = json.Unmarshal(extraction, &raw)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "document": map[string]any{"id": id, "documentType": documentType, "source": source, "status": status, "supplierName": supplier, "documentNumber": number, "documentDate": date, "confidence": confidence, "model": model, "extraction": raw, "lines": lines, "originalAvailable": originalAvailable != 0}})
}

func (s *Server) handleBOStockDocumentReview(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		SupplierName   string `json:"supplierName"`
		DocumentNumber string `json:"documentNumber"`
		DocumentDate   string `json:"documentDate"`
		Lines          []struct {
			ID                 int64   `json:"id"`
			Description        string  `json:"description"`
			Code               string  `json:"code"`
			Quantity           float64 `json:"quantity"`
			Unit               string  `json:"unit"`
			UnitPrice          float64 `json:"unitPrice"`
			Total              float64 `json:"total"`
			MatchedStockItemID *int64  `json:"matchedStockItemId"`
			MatchedUnitID      *int64  `json:"matchedUnitId"`
			Status             string  `json:"status"`
		} `json:"lines"`
	}
	if id <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid document review")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving review")
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), `UPDATE stock_document_scans SET supplier_name=?,document_number=?,document_date=NULLIF(?,''),status='NEEDS_REVIEW' WHERE restaurant_id=? AND id=? AND status='NEEDS_REVIEW'`, stockNullableString(in.SupplierName), stockNullableString(in.DocumentNumber), in.DocumentDate, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Review could not be saved")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusConflict, "Document is not reviewable")
		return
	}
	for _, line := range in.Lines {
		status := strings.ToUpper(strings.TrimSpace(line.Status))
		if status != "OK" && status != "NEEDS_MATCH" && status != "IGNORED" {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid line status")
			return
		}
		if status == "OK" && (line.MatchedStockItemID == nil || line.MatchedUnitID == nil || line.Quantity <= 0 || line.UnitPrice < 0 || line.Total < 0) {
			httpx.WriteError(w, http.StatusBadRequest, "Matched lines require item, unit and quantity")
			return
		}
		result, err := tx.ExecContext(r.Context(), `UPDATE stock_document_lines SET raw_description=?,raw_code=?,raw_qty=?,raw_unit=?,raw_unit_price=?,raw_total=?,matched_stock_item_id=?,matched_unit_id=?,status=? WHERE restaurant_id=? AND document_scan_id=? AND id=?`, strings.TrimSpace(line.Description), stockNullableString(line.Code), stockNullableFloat(line.Quantity), stockNullableString(line.Unit), stockNullableFloat(line.UnitPrice), stockNullableFloat(line.Total), line.MatchedStockItemID, line.MatchedUnitID, status, a.ActiveRestaurantID, id, line.ID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Document line could not be saved")
			return
		}
		updated, _ := result.RowsAffected()
		if updated == 0 {
			httpx.WriteError(w, http.StatusNotFound, "Document line not found")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving review")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockDocumentReject(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_document_scans SET status='REJECTED' WHERE restaurant_id=? AND id=? AND status='NEEDS_REVIEW'`, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error rejecting document")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusConflict, "Document is not reviewable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockRecipeDocumentConfirm(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	scanID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Name           string  `json:"name"`
		OutputItemID   int64   `json:"outputItemId"`
		OutputQuantity float64 `json:"outputQuantity"`
		OutputUnitID   int64   `json:"outputUnitId"`
	}
	if scanID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || in.OutputItemID <= 0 || in.OutputUnitID <= 0 || in.OutputQuantity <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid recipe confirmation")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error confirming recipe")
		return
	}
	defer tx.Rollback()
	var status, documentType string
	var extractionRaw json.RawMessage
	if err := tx.QueryRowContext(r.Context(), `SELECT status,document_type,COALESCE(raw_extraction,JSON_OBJECT()) FROM stock_document_scans WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, scanID).Scan(&status, &documentType, &extractionRaw); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Document not found")
		return
	}
	if status != "NEEDS_REVIEW" || documentType != "RECIPE" {
		httpx.WriteError(w, http.StatusConflict, "Recipe document is not confirmable")
		return
	}
	var outputFactor float64
	if err := tx.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND id=?`, a.ActiveRestaurantID, in.OutputItemID, in.OutputUnitID).Scan(&outputFactor); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid output unit")
		return
	}
	var pending int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stock_document_lines WHERE restaurant_id=? AND document_scan_id=? AND status='NEEDS_MATCH'`, a.ActiveRestaurantID, scanID).Scan(&pending); err != nil || pending > 0 {
		httpx.WriteError(w, http.StatusConflict, "Review every recipe component first")
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_recipes (restaurant_id,name,output_item_id,output_qty_base,source) VALUES (?,?,?,?,'OCR')`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), in.OutputItemID, in.OutputQuantity*outputFactor)
	if err != nil {
		httpx.WriteError(w, http.StatusConflict, "Output item already has an active recipe")
		return
	}
	recipeID, _ := res.LastInsertId()
	var extraction stockDocumentExtraction
	_ = json.Unmarshal(extractionRaw, &extraction)
	wasteByLine := map[int]float64{}
	for index, line := range extraction.Components {
		wasteByLine[index+1] = line.WastePct
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT line_no,raw_description,raw_qty,matched_stock_item_id,matched_unit_id FROM stock_document_lines WHERE restaurant_id=? AND document_scan_id=? AND status='OK' ORDER BY line_no`, a.ActiveRestaurantID, scanID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading recipe lines")
		return
	}
	type component struct {
		lineNo         int
		description    string
		quantity       float64
		itemID, unitID int64
	}
	components := []component{}
	for rows.Next() {
		var x component
		if err := rows.Scan(&x.lineNo, &x.description, &x.quantity, &x.itemID, &x.unitID); err != nil {
			rows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading recipe lines")
			return
		}
		components = append(components, x)
	}
	rows.Close()
	if len(components) == 0 {
		httpx.WriteError(w, http.StatusConflict, "Recipe has no accepted components")
		return
	}
	for index, component := range components {
		var factor float64
		if component.itemID == in.OutputItemID {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe output cannot be its own component")
			return
		}
		if err := tx.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND id=?`, a.ActiveRestaurantID, component.itemID, component.unitID).Scan(&factor); err != nil || component.quantity <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe component mapping is invalid")
			return
		}
		waste := wasteByLine[component.lineNo]
		if waste < 0 || waste >= 100 {
			waste = 0
		}
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO stock_recipe_components (restaurant_id,recipe_id,stock_item_id,entered_qty,entered_unit_id,qty_base,waste_pct,notes,sort_order) VALUES (?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, recipeID, component.itemID, component.quantity, component.unitID, component.quantity*factor, waste, stockNullableString("OCR: "+component.description), index); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe component could not be created")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE stock_document_scans SET status='CONFIRMED' WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, scanID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error confirming recipe")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error confirming recipe")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "recipeId": recipeID})
}

func (s *Server) handleBOStockInvoiceConfirm(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	scanID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		WarehouseID    int64  `json:"warehouseId"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if scanID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.WarehouseID <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid confirmation")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error confirming invoice")
		return
	}
	defer tx.Rollback()
	var status, documentType, supplier string
	if err := tx.QueryRowContext(r.Context(), `SELECT status,document_type,COALESCE(supplier_name,'') FROM stock_document_scans WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, scanID).Scan(&status, &documentType, &supplier); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Document not found")
		return
	}
	if status == "CONFIRMED" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true})
		return
	}
	if status != "NEEDS_REVIEW" || documentType != "INVOICE" {
		httpx.WriteError(w, http.StatusConflict, "Invoice is not confirmable")
		return
	}
	var pending int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stock_document_lines WHERE restaurant_id=? AND document_scan_id=? AND status='NEEDS_MATCH'`, a.ActiveRestaurantID, scanID).Scan(&pending); err != nil || pending > 0 {
		httpx.WriteError(w, http.StatusConflict, "Review every invoice line first")
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT id,raw_description,COALESCE(raw_code,''),raw_qty,COALESCE(raw_unit_price,0),matched_stock_item_id,matched_unit_id FROM stock_document_lines WHERE restaurant_id=? AND document_scan_id=? AND status='OK' ORDER BY line_no`, a.ActiveRestaurantID, scanID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading invoice lines")
		return
	}
	type invoiceLine struct {
		id, itemID, unitID  int64
		description, code   string
		quantity, unitPrice float64
	}
	lines := []invoiceLine{}
	for rows.Next() {
		var line invoiceLine
		if err := rows.Scan(&line.id, &line.description, &line.code, &line.quantity, &line.unitPrice, &line.itemID, &line.unitID); err != nil {
			rows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading invoice lines")
			return
		}
		lines = append(lines, line)
	}
	rows.Close()
	if len(lines) == 0 {
		httpx.WriteError(w, http.StatusConflict, "Invoice has no accepted lines")
		return
	}
	for _, line := range lines {
		var factor float64
		if err := tx.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND id=?`, a.ActiveRestaurantID, line.itemID, line.unitID).Scan(&factor); err != nil || factor <= 0 || line.quantity <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Invoice line mapping is invalid")
			return
		}
		qtyBase := line.quantity * factor
		unitCostBase := 0.0
		if line.unitPrice > 0 {
			unitCostBase = line.unitPrice / factor
		}
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, a.ActiveRestaurantID, line.itemID, in.WarehouseID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid warehouse or item")
			return
		}
		var currentQty, currentCost float64
		if err := tx.QueryRowContext(r.Context(), `SELECT qty_base,avg_unit_cost FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=? FOR UPDATE`, a.ActiveRestaurantID, line.itemID, in.WarehouseID).Scan(&currentQty, &currentCost); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error locking stock")
			return
		}
		newCost := currentCost
		if unitCostBase > 0 {
			valuedCurrent := currentQty
			if valuedCurrent < 0 {
				valuedCurrent = 0
			}
			newCost = (valuedCurrent*currentCost + qtyBase*unitCostBase) / (valuedCurrent + qtyBase)
		}
		key := fmt.Sprintf("%s-line-%d", strings.TrimSpace(in.IdempotencyKey), line.id)
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,entered_qty,entered_unit_id,unit_cost,total_cost,ref_type,ref_id,idempotency_key,actor_user_id) VALUES (?,?,?,?,'PURCHASE',?,?,?,?, 'stock_document',?,?,?)`, a.ActiveRestaurantID, line.itemID, in.WarehouseID, qtyBase, line.quantity, line.unitID, stockNullableFloat(unitCostBase), stockNullableFloat(line.quantity*line.unitPrice), scanID, key, a.User.ID); err != nil {
			httpx.WriteError(w, http.StatusConflict, "Invoice was already applied")
			return
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE stock_levels SET qty_base=qty_base+?,avg_unit_cost=?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, qtyBase, newCost, a.ActiveRestaurantID, line.itemID, in.WarehouseID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error updating stock")
			return
		}
		if unitCostBase > 0 {
			_, _ = tx.ExecContext(r.Context(), `INSERT INTO stock_item_prices (restaurant_id,stock_item_id,supplier_name,unit_cost_base,source) VALUES (?,?,?,?,'OCR')`, a.ActiveRestaurantID, line.itemID, stockNullableString(supplier), unitCostBase)
		}
		_, _ = tx.ExecContext(r.Context(), `INSERT INTO stock_supplier_aliases (restaurant_id,supplier_name,supplier_code,normalized_description,stock_item_id,stock_unit_id) VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id),stock_unit_id=VALUES(stock_unit_id)`, a.ActiveRestaurantID, supplier, line.code, stockAliasDescription(line.description), line.itemID, line.unitID)
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE stock_document_scans SET status='CONFIRMED' WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, scanID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error confirming invoice")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error confirming invoice")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "linesApplied": len(lines)})
}

func (s *Server) handleBOStockDocumentOriginalGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var objectPath, contentType, filename string
	if err := s.db.QueryRowContext(r.Context(), `SELECT COALESCE(file_path,''),COALESCE(content_type,'application/octet-stream'),COALESCE(original_filename,'document') FROM stock_document_scans WHERE restaurant_id=? AND id=? AND file_path IS NOT NULL AND original_deleted_at IS NULL`, a.ActiveRestaurantID, id).Scan(&objectPath, &contentType, &filename); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Original document not found")
		return
	}
	payload, storedType, err := s.bunnyPrivateGet(r.Context(), a.ActiveRestaurantID, objectPath)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "Original document unavailable")
		return
	}
	if storedType != "" {
		contentType = storedType
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO stock_document_access_audit (restaurant_id,document_scan_id,action,actor_user_id) VALUES (?,?,'DOWNLOAD',?)`, a.ActiveRestaurantID, id, a.User.ID)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filename, `"`, ``)+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (s *Server) handleBOStockDocumentOriginalDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var objectPath string
	if err := s.db.QueryRowContext(r.Context(), `SELECT COALESCE(file_path,'') FROM stock_document_scans WHERE restaurant_id=? AND id=? AND file_path IS NOT NULL AND original_deleted_at IS NULL`, a.ActiveRestaurantID, id).Scan(&objectPath); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Original document not found")
		return
	}
	if err := s.bunnyPrivateDelete(r.Context(), a.ActiveRestaurantID, objectPath); err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "Original document delete failed")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting original document")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `UPDATE stock_document_scans SET original_deleted_at=NOW(),file_path=NULL WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, 500, "Error deleting original document")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO stock_document_access_audit (restaurant_id,document_scan_id,action,actor_user_id) VALUES (?,?,'DELETE',?)`, a.ActiveRestaurantID, id, a.User.ID); err != nil {
		httpx.WriteError(w, 500, "Error auditing original document")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error deleting original document")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
