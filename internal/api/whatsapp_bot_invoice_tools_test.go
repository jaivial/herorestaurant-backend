package api

import (
	"context"
	"math"
	"strings"
	"testing"
)

// Coordination id: wa_bot_invoice_v1

// botTestBillLines is the bill of 2026-10-25 (mesa 401) as printed: prices
// with IVA included, TOTAL 185,00 €.
func botTestBillLines() []invoiceToolLine {
	return []invoiceToolLine{
		{Description: "MENU FIN DE SEMANA", Quantity: 4, UnitPrice: 30},
		{Description: "PAELLA VALENCIANA", Quantity: 4, UnitPrice: 6},
		{Description: "Sangria", Quantity: 1, UnitPrice: 10},
		{Description: "AGUA 1 LITRO", Quantity: 1, UnitPrice: 4},
		{Description: "ALHAMBRA VERDE", Quantity: 1, UnitPrice: 4},
		{Description: "AGUA 1 LITRO", Quantity: 1, UnitPrice: 4},
		{Description: "Agua 0,5", Quantity: 1, UnitPrice: 3},
		{Description: "Sangria", Quantity: 1, UnitPrice: 10},
		{Description: "AGUA 1 LITRO", Quantity: 1, UnitPrice: 4},
		{Description: "CAFE COCO", Quantity: 1, UnitPrice: 2},
	}
}

func botTestCustomer() *invoiceToolCustomer {
	return &invoiceToolCustomer{
		Name: "Ricart Recambios S.L.U.", Email: "rrp1967@gmail.com", TaxID: "B98942279",
		Street: "Av. Reina Sofía", Number: "4-1-2", PostalCode: "46900", City: "Torrent", Phone: "650 49 33 35",
	}
}

// The invoice of a bill totals exactly the bill, and stays exact after the
// draft is saved again (the panel recomputes every line from the stored
// two-decimal unit price on each save).
func TestBotInvoiceBuildInput_TheBillAddsUpToItsTotal(t *testing.T) {
	total := 185.0
	in, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: botTestCustomer(), Lines: botTestBillLines(), ExpectedTotal: &total}, nil)
	if err != nil {
		t.Fatalf("the bill was refused: %v", err)
	}
	if got := botInvoiceComputedTotal(in); math.Abs(got-185) >= 0.005 {
		t.Fatalf("invoice total=%.2f, want the bill total 185,00", got)
	}
	if in.LineItems[0].Description != "4 × MENU FIN DE SEMANA" || in.LineItems[0].Quantity != 1 {
		t.Errorf("first line=%+v, want '4 × MENU FIN DE SEMANA' at quantity 1", in.LineItems[0])
	}
	// What a later save reads back: the unit price as the DECIMAL(10,2)
	// column stores it.
	stored := in
	stored.LineItems = append([]InvoiceLineItemInput(nil), in.LineItems...)
	for i := range stored.LineItems {
		stored.LineItems[i].UnitPrice = round2(stored.LineItems[i].UnitPrice)
	}
	if got := botInvoiceComputedTotal(stored); math.Abs(got-185) >= 0.005 {
		t.Errorf("total after a re-save=%.2f, want 185,00 (the stored prices drift)", got)
	}
}

// Whatever the bill, the invoice total is the bill total: an amount no
// two-decimal net reaches once the IVA of the line is rounded (1,15 € at
// 10 %) leaves its cent to another line instead of to the total.
func TestBotInvoiceFitNets_TheTotalIsAlwaysTheBill(t *testing.T) {
	for _, rate := range []float64{10, 21, 4} {
		for cents := 1; cents <= 3000; cents++ {
			small := float64(cents) / 100
			grosses := []float64{12, small, 3.5}
			nets := botInvoiceFitNets(grosses, []float64{rate, rate, rate})
			sum, target := 0.0, 0.0
			for i, c := range nets {
				if math.Abs(c-round2(c)) > 1e-9 {
					t.Fatalf("net %v has more than two decimals", c)
				}
				sum += botInvoiceLineGross(c, rate)
				target += grosses[i]
			}
			if math.Abs(round2(sum)-round2(target)) >= 0.005 {
				t.Fatalf("rate %v, lines %v: invoice %.2f vs bill %.2f", rate, grosses, sum, target)
			}
		}
	}
}

// Fiscal data is never made up: without the client's name or email the draft
// is refused with what is missing, so the model asks the group.
func TestBotInvoiceBuildInput_RefusesWithoutTheClientData(t *testing.T) {
	c := botTestCustomer()
	c.Email = ""
	_, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: c, Lines: botTestBillLines()}, nil)
	if err == nil || !strings.Contains(err.Error(), "email del cliente") || !strings.Contains(err.Error(), "no los inventes") {
		t.Fatalf("a draft without the email must be refused asking for it, got %v", err)
	}
	c = botTestCustomer()
	c.Email = "rrp1967 gmail.com"
	if _, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: c, Lines: botTestBillLines()}, nil); err == nil {
		t.Fatal("a malformed email must be refused")
	}
	if _, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: botTestCustomer()}, nil); err == nil || !strings.Contains(err.Error(), "líneas") {
		t.Fatalf("a draft without lines must be refused, got %v", err)
	}
}

// A line misread from the photo shows up as a total that does not match the
// bill, and the draft is refused before it consumes a number of the series.
func TestBotInvoiceBuildInput_RefusesLinesThatDoNotAddUpToTheBill(t *testing.T) {
	lines := botTestBillLines()
	lines[1].UnitPrice = 8 // 4 × 8 instead of 4 × 6
	total := 185.0
	_, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: botTestCustomer(), Lines: lines, ExpectedTotal: &total}, nil)
	if err == nil || !strings.Contains(err.Error(), "193,00 €") || !strings.Contains(err.Error(), "185,00 €") {
		t.Fatalf("lines that do not add up must be refused with both totals, got %v", err)
	}
}

// A CIF read off a photo can be one digit off: the one of the prod note
// (B98942270) fails its check digit and is asked for instead of printed. A
// foreign VAT number is not held to the Spanish formats.
func TestBotInvoiceBuildInput_RefusesACIFThatFailsItsCheckDigit(t *testing.T) {
	c := botTestCustomer()
	c.TaxID = "B 98942270"
	_, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: c, Lines: botTestBillLines()}, nil)
	if err == nil || !strings.Contains(err.Error(), "B98942270 no es válido") {
		t.Fatalf("a CIF with a wrong check digit must be refused, got %v", err)
	}
	for _, ok := range []struct{ id, country string }{{"ESB98942279", "España"}, {"12345678Z", ""}, {"FR40303265045", "Francia"}} {
		c = botTestCustomer()
		c.TaxID, c.Country = ok.id, ok.country
		if _, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: c, Lines: botTestBillLines()}, nil); err != nil {
			t.Errorf("tax id %s (%q) must be accepted, got %v", ok.id, ok.country, err)
		}
	}
}

// The client's fiscal fields land where the PDF and the send system read them.
func TestBotInvoiceBuildInput_MapsTheClient(t *testing.T) {
	in, err := botInvoiceBuildInput(invoiceToolArgs{Action: "create_draft", Customer: botTestCustomer(), Lines: botTestBillLines()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if deref(in.CustomerDniCif) != "B98942279" || deref(in.CustomerDniCifType) != "cif" {
		t.Errorf("tax id=%q type=%q, want B98942279 as CIF", deref(in.CustomerDniCif), deref(in.CustomerDniCifType))
	}
	// A national number gets the Spanish code: WhatsApp sends then go out
	// without the "confirm the number" stop.
	if deref(in.CustomerPhoneCountryCode) != "34" {
		t.Errorf("phone country code=%q, want 34", deref(in.CustomerPhoneCountryCode))
	}
	if in.Status != "borrador" || in.InvoiceDate == "" {
		t.Errorf("status=%q date=%q, want a dated draft", in.Status, in.InvoiceDate)
	}
	for id, want := range map[string]string{"12345678Z": "dni", "X1234567L": "dni", "B98942279": "cif"} {
		if got := botInvoiceTaxIDType(id, ""); got != want {
			t.Errorf("tax id %s: type %q, want %q", id, got, want)
		}
	}
}

// Correcting a draft keeps its number and every field it does not mention.
func TestBotInvoiceBuildInput_UpdateKeepsTheDraft(t *testing.T) {
	number, total, sub, iva := "F-2026-0011", 120.0, 109.09, 10.91
	stored := &Invoice{
		ID: 7, InvoiceNumber: &number, Status: "borrador", InvoiceDate: "2026-10-27",
		CustomerName: "Ricart Recambios S.L.U.", CustomerEmail: "old@example.com",
		Subtotal: &sub, IVAAmount: &iva, Total: &total,
		LineItems: []InvoiceLineItem{{Description: "4 × MENU FIN DE SEMANA", Quantity: 1, UnitPrice: 109.09, IVARate: 10}},
	}
	in, err := botInvoiceBuildInput(invoiceToolArgs{Action: "update_draft", InvoiceID: 7, Customer: &invoiceToolCustomer{Email: "rrp1967@gmail.com"}}, stored)
	if err != nil {
		t.Fatal(err)
	}
	if deref(in.InvoiceNumber) != number || in.CustomerName != stored.CustomerName || in.CustomerEmail != "rrp1967@gmail.com" {
		t.Errorf("update lost the draft: number=%q name=%q email=%q", deref(in.InvoiceNumber), in.CustomerName, in.CustomerEmail)
	}
	if len(in.LineItems) != 1 || math.Abs(botInvoiceComputedTotal(in)-120) >= 0.005 {
		t.Errorf("update changed the lines: %+v", in.LineItems)
	}
}

// End to end against the real schema and the panel's own handlers: the bill
// becomes a numbered draft totalling 185,00 €, and correcting it keeps the
// number and the total.
func TestBotInvoiceApply_CreateAndCorrectTheDraft_DB(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rid, cleanup := seedRestaurant(t, db, "wa-bot-invoice-test")
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE li FROM invoice_line_items li JOIN invoices i ON i.id = li.invoice_id WHERE i.restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM invoices WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM restaurant_invoice_settings WHERE restaurant_id = ?`, rid)
		cleanup()
	})
	// The test DB is a copy of production data: no background loop may run
	// (reminders, outbox) and try to send anything (bo_dev_workers_gate_v1).
	t.Setenv("BO_DISABLE_BACKGROUND_WORKERS", "1")
	s := newTestServer(t, db)
	msg := botWebhookMessage{Sender: "120363430262328476@g.us", ChatJID: "120363430262328476@g.us", PrivilegedGroup: true}
	total := 185.0

	out, err := s.botInvoiceApply(ctx, rid, msg, 1, invoiceToolArgs{Action: "create_draft", Customer: botTestCustomer(), Lines: botTestBillLines(), ExpectedTotal: &total})
	if err != nil {
		t.Fatalf("create_draft: %v", err)
	}
	if !strings.Contains(out, "Borrador creado") || !strings.Contains(out, "total 185,00 €") {
		t.Errorf("create result=%q", out)
	}
	var id int
	var number, status, cif, cc string
	var stTotal float64
	if err := db.QueryRow(`SELECT id, invoice_number, status, total, customer_dni_cif, customer_phone_country_code FROM invoices WHERE restaurant_id = ?`, rid).
		Scan(&id, &number, &status, &stTotal, &cif, &cc); err != nil {
		t.Fatalf("the draft was not stored: %v", err)
	}
	if status != "borrador" || number == "" || math.Abs(stTotal-185) >= 0.005 || cif != "B98942279" || cc != "34" {
		t.Fatalf("stored draft: number=%q status=%q total=%.2f cif=%q cc=%q", number, status, stTotal, cif, cc)
	}

	out, err = s.botInvoiceApply(ctx, rid, msg, 2, invoiceToolArgs{Action: "update_draft", InvoiceID: id, Customer: &invoiceToolCustomer{Email: "facturas@ricart.example"}})
	if err != nil {
		t.Fatalf("update_draft: %v", err)
	}
	var number2, email string
	if err := db.QueryRow(`SELECT invoice_number, customer_email, total FROM invoices WHERE id = ?`, id).Scan(&number2, &email, &stTotal); err != nil {
		t.Fatal(err)
	}
	if number2 != number || email != "facturas@ricart.example" || math.Abs(stTotal-185) >= 0.005 {
		t.Errorf("corrected draft: number %q→%q email=%q total=%.2f (%s)", number, number2, email, stTotal, out)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM invoices WHERE restaurant_id = ?`, rid).Scan(&n)
	if n != 1 {
		t.Errorf("a correction created %d invoices, want the same one", n)
	}
}
