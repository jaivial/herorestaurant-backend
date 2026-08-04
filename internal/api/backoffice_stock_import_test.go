package api

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestParseStockImportCSV(t *testing.T) {
	rows, err := parseStockImportFile("items.csv", []byte("nombre,sku,dimension,unidad,factor,seguimiento\nHarina,HAR,MASS,kg,1000,si\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "Harina" || rows[0].UnitFactor != 1000 || !rows[0].IsTracked {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestParseStockImportXLSX(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Items" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row><c t="inlineStr"><is><t>name</t></is></c><c t="inlineStr"><is><t>dimension</t></is></c><c t="inlineStr"><is><t>unit</t></is></c></row><row><c t="inlineStr"><is><t>Leche</t></is></c><c t="inlineStr"><is><t>VOLUME</t></is></c><c t="inlineStr"><is><t>l</t></is></c></row></sheetData></worksheet>`,
	}
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	rows, err := parseStockImportFile("items.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "Leche" || rows[0].UnitFactor != 1000 {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestParseStockImportXLSXPreservesSparseColumns(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row><c r="A1" t="inlineStr"><is><t>name</t></is></c><c r="C1" t="inlineStr"><is><t>dimension</t></is></c><c r="D1" t="inlineStr"><is><t>unit</t></is></c></row><row><c r="A2" t="inlineStr"><is><t>Leche</t></is></c><c r="C2" t="inlineStr"><is><t>VOLUME</t></is></c><c r="D2" t="inlineStr"><is><t>l</t></is></c></row></sheetData></worksheet>`,
	}
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	rows, err := parseStockImportFile("items.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Dimension != "VOLUME" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestNormalizeStockImportRowRejectsBadDimension(t *testing.T) {
	_, errs := normalizeStockImportRow(3, map[string]string{"name": "Foo", "dimension": "box"})
	if len(errs) == 0 {
		t.Fatal("accepted bad dimension")
	}
}
