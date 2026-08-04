package api

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

const stockImportMaxBytes = 8 << 20

type stockImportRow struct {
	Row             int      `json:"row"`
	Name            string   `json:"name"`
	SKU             string   `json:"sku,omitempty"`
	Category        string   `json:"category,omitempty"`
	Kind            string   `json:"kind"`
	Dimension       string   `json:"dimension"`
	UnitCode        string   `json:"unitCode"`
	UnitLabel       string   `json:"unitLabel"`
	UnitFactor      float64  `json:"unitFactor"`
	IsTracked       bool     `json:"isTracked"`
	DeductionSource string   `json:"deductionSource"`
	Errors          []string `json:"errors,omitempty"`
}

func normalizeStockImportHeader(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", " ", "_", "-", "_")
	value = replacer.Replace(value)
	aliases := map[string]string{
		"nombre": "name", "articulo": "name", "item": "name",
		"categoria": "category", "tipo": "kind", "dimension_base": "dimension",
		"unidad": "unit", "unidad_visible": "unit", "factor_base": "factor",
		"seguimiento": "tracked", "controlar_stock": "tracked", "descuento": "deduction_source",
	}
	if alias := aliases[value]; alias != "" {
		return alias
	}
	return value
}

func stockImportBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "si", "sí", "y", "s":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}

func stockImportUnit(dimension, rawUnit string, rawFactor float64) (string, string, float64) {
	unit := strings.ToLower(strings.TrimSpace(rawUnit))
	if rawFactor > 0 {
		return unit, rawUnit, rawFactor
	}
	switch dimension + ":" + unit {
	case "MASS:kg":
		return "kg", "kg", 1000
	case "MASS:g", "MASS:":
		return "g", "g", 1
	case "VOLUME:l", "VOLUME:litro", "VOLUME:litros":
		return "l", "l", 1000
	case "VOLUME:ml", "VOLUME:":
		return "ml", "ml", 1
	case "COUNT:docena", "COUNT:dozen":
		return "docena", "docena", 12
	case "COUNT:ud", "COUNT:unidad", "COUNT:unidades", "COUNT:":
		return "ud", "ud", 1
	default:
		return unit, rawUnit, rawFactor
	}
}

func normalizeStockImportRow(rowNo int, values map[string]string) (stockImportRow, []string) {
	row := stockImportRow{Row: rowNo, Name: strings.TrimSpace(values["name"]), SKU: strings.TrimSpace(values["sku"]), Category: strings.TrimSpace(values["category"]), Kind: strings.ToUpper(strings.TrimSpace(values["kind"])), Dimension: strings.ToUpper(strings.TrimSpace(values["dimension"])), IsTracked: stockImportBool(values["tracked"], true), DeductionSource: strings.ToUpper(strings.TrimSpace(values["deduction_source"]))}
	if row.Kind == "" {
		row.Kind = "RAW"
	}
	if row.DeductionSource == "" {
		row.DeductionSource = "BOTH_MANUAL"
	}
	factor, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(values["factor"]), ",", "."), 64)
	row.UnitCode, row.UnitLabel, row.UnitFactor = stockImportUnit(row.Dimension, values["unit"], factor)
	errs := []string{}
	if row.Name == "" {
		errs = append(errs, "name is required")
	}
	if _, ok := stockBaseUnitForDimension(row.Dimension); !ok {
		errs = append(errs, "dimension must be MASS, VOLUME or COUNT")
	}
	if !validStockItemKind(row.Kind) {
		errs = append(errs, "invalid kind")
	}
	if !validStockDeductionSource(row.DeductionSource) {
		errs = append(errs, "invalid deduction source")
	}
	if row.UnitFactor <= 0 || row.UnitCode == "" {
		errs = append(errs, "unit/factor is invalid")
	}
	row.Errors = errs
	return row, errs
}

func stockImportRows(table [][]string) ([]stockImportRow, error) {
	if len(table) < 2 {
		return nil, errors.New("file needs header and data rows")
	}
	headers := make([]string, len(table[0]))
	for i, header := range table[0] {
		headers[i] = normalizeStockImportHeader(header)
	}
	rows := []stockImportRow{}
	for index, record := range table[1:] {
		values := map[string]string{}
		empty := true
		for i, value := range record {
			if strings.TrimSpace(value) != "" {
				empty = false
			}
			if i < len(headers) && headers[i] != "" {
				values[headers[i]] = value
			}
		}
		if empty {
			continue
		}
		row, _ := normalizeStockImportRow(index+2, values)
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("file has no data rows")
	}
	return rows, nil
}

func parseStockImportCSV(payload []byte) ([][]string, error) {
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(payload, []byte("\xef\xbb\xbf"))))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err == nil && len(rows) > 0 && len(rows[0]) == 1 && strings.Contains(rows[0][0], ";") {
		reader = csv.NewReader(bytes.NewReader(payload))
		reader.Comma = ';'
		reader.FieldsPerRecord = -1
		rows, err = reader.ReadAll()
	}
	return rows, err
}

type xlsxRelationship struct {
	ID     string `xml:"Id,attr"`
	Target string `xml:"Target,attr"`
}
type xlsxRelationships struct {
	Items []xlsxRelationship `xml:"Relationship"`
}
type xlsxCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Value  string `xml:"v"`
	Inline string `xml:"is>t"`
}
type xlsxRow struct {
	Cells []xlsxCell `xml:"c"`
}
type xlsxSheet struct {
	Rows []xlsxRow `xml:"sheetData>row"`
}
type xlsxSharedStrings struct {
	Items []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

func readZipXML(files map[string]*zip.File, name string, target any) error {
	file := files[path.Clean(name)]
	if file == nil {
		return fmt.Errorf("xlsx part missing: %s", name)
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	return xml.NewDecoder(io.LimitReader(reader, stockImportMaxBytes)).Decode(target)
}

func xlsxColumnIndex(ref string) int {
	index := 0
	for _, char := range ref {
		if char < 'A' || char > 'Z' {
			break
		}
		index = index*26 + int(char-'A'+1)
	}
	if index == 0 {
		return 0
	}
	return index - 1
}

func parseStockImportXLSX(payload []byte) ([][]string, error) {
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, err
	}
	files := map[string]*zip.File{}
	for _, file := range archive.File {
		files[file.Name] = file
	}
	var rels xlsxRelationships
	if err := readZipXML(files, "xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, err
	}
	sheetPath := ""
	for _, rel := range rels.Items {
		if strings.Contains(rel.Target, "worksheets/") {
			sheetPath = "xl/" + strings.TrimPrefix(rel.Target, "/")
			break
		}
	}
	if sheetPath == "" {
		return nil, errors.New("xlsx has no worksheet")
	}
	shared := []string{}
	if files["xl/sharedStrings.xml"] != nil {
		var stringsXML xlsxSharedStrings
		if err := readZipXML(files, "xl/sharedStrings.xml", &stringsXML); err != nil {
			return nil, err
		}
		for _, item := range stringsXML.Items {
			text := item.Text
			for _, run := range item.Runs {
				text += run.Text
			}
			shared = append(shared, text)
		}
	}
	var sheet xlsxSheet
	if err := readZipXML(files, sheetPath, &sheet); err != nil {
		return nil, err
	}
	table := make([][]string, 0, len(sheet.Rows))
	for _, row := range sheet.Rows {
		values := []string{}
		for fallbackIndex, cell := range row.Cells {
			column := fallbackIndex
			if cell.Ref != "" {
				column = xlsxColumnIndex(cell.Ref)
			}
			for len(values) <= column {
				values = append(values, "")
			}
			value := cell.Value
			if cell.Type == "inlineStr" {
				value = cell.Inline
			}
			if cell.Type == "s" {
				index, _ := strconv.Atoi(value)
				if index >= 0 && index < len(shared) {
					value = shared[index]
				}
			}
			values[column] = value
		}
		table = append(table, values)
	}
	return table, nil
}

func parseStockImportFile(filename string, payload []byte) ([]stockImportRow, error) {
	var table [][]string
	var err error
	switch strings.ToLower(path.Ext(filename)) {
	case ".csv", ".txt":
		table, err = parseStockImportCSV(payload)
	case ".xlsx":
		table, err = parseStockImportXLSX(payload)
	default:
		return nil, errors.New("use CSV or XLSX")
	}
	if err != nil {
		return nil, err
	}
	return stockImportRows(table)
}

func (s *Server) handleBOStockItemsImport(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, stockImportMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(stockImportMaxBytes); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid import file")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Import file is required")
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, stockImportMaxBytes+1))
	if err != nil || len(payload) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Import file could not be read")
		return
	}
	if len(payload) > stockImportMaxBytes {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "Import exceeds 8 MB")
		return
	}
	rows, err := parseStockImportFile(header.Filename, payload)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	seen := map[string]bool{}
	valid := 0
	for index := range rows {
		key := strings.ToLower(rows[index].SKU)
		if key == "" {
			key = "name:" + strings.ToLower(rows[index].Name)
		}
		if seen[key] {
			rows[index].Errors = append(rows[index].Errors, "duplicate in file")
		} else {
			seen[key] = true
		}
		var exists int
		if rows[index].SKU != "" {
			_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_items WHERE restaurant_id=? AND sku=? AND deleted_at IS NULL)`, a.ActiveRestaurantID, rows[index].SKU).Scan(&exists)
		} else {
			_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_items WHERE restaurant_id=? AND LOWER(name)=LOWER(?) AND deleted_at IS NULL)`, a.ActiveRestaurantID, rows[index].Name).Scan(&exists)
		}
		if exists != 0 {
			rows[index].Errors = append(rows[index].Errors, "item already exists")
		}
		if len(rows[index].Errors) == 0 {
			valid++
		}
	}
	if r.FormValue("confirm") != "1" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "preview": true, "rows": rows, "validRows": valid, "invalidRows": len(rows) - valid})
		return
	}
	if valid == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "No valid rows to import")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error starting import")
		return
	}
	defer tx.Rollback()
	created := 0
	for _, row := range rows {
		if len(row.Errors) != 0 {
			continue
		}
		baseUnit, _ := stockBaseUnitForDimension(row.Dimension)
		var categoryID any
		if row.Category != "" {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_categories (restaurant_id,name) VALUES (?,?) ON DUPLICATE KEY UPDATE name=VALUES(name)`, a.ActiveRestaurantID, row.Category)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "Category import failed")
				return
			}
			var id int64
			if err = tx.QueryRowContext(r.Context(), `SELECT id FROM stock_categories WHERE restaurant_id=? AND name=?`, a.ActiveRestaurantID, row.Category).Scan(&id); err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "Category import failed")
				return
			}
			categoryID = id
		}
		res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_items (restaurant_id,category_id,sku,name,kind,base_dimension,base_unit,is_tracked,deduction_source) VALUES (?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, categoryID, stockNullableString(row.SKU), row.Name, row.Kind, row.Dimension, baseUnit, stockBoolInt(row.IsTracked), row.DeductionSource)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Item import failed")
			return
		}
		itemID, _ := res.LastInsertId()
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO stock_item_units (restaurant_id,stock_item_id,code,label,factor_to_base,is_default_purchase,is_default_display,can_purchase,can_recipe,can_count) VALUES (?,?,?,?,?,1,1,1,1,1)`, a.ActiveRestaurantID, itemID, row.UnitCode, row.UnitLabel, row.UnitFactor); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Unit import failed")
			return
		}
		created++
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Import failed")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "created": created, "skipped": len(rows) - created})
}
