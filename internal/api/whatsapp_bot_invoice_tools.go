package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Coordination id: wa_bot_invoice_v1
//
// Invoices from the privileged staff chat: create a DRAFT from the data of a
// bill (and the client's fiscal data), get its PDF back in the chat, correct
// it, and send it to the client by email and/or WhatsApp.
//
// Every write runs through the panel's own handlers (handleBOInvoiceCreate,
// handleBOInvoiceUpdate, handleBOInvoiceSend, handleBOInvoiceSendWhatsApp), so
// numbering, validation, PDF and the send system are exactly the panel's.
// Before this tool the bot could only reach them through the generic
// meta-catalogue (admin_catalog -> admin_describe -> admin_call) and spent its
// whole turn discovering the API (prod 2026-10-27 21:08: 7 iterations, 150s,
// no invoice).
//
// The writes go through the proposal flow (botProposalPersist): applied at
// once when the chat has direct writes, behind an OK code otherwise.

// botInvoiceDefaultIVA is the IVA of a restaurant bill in Spain (hostelería).
const botInvoiceDefaultIVA = 10.0

func botToolDefsInvoice() []botToolDef {
	return []botToolDef{{
		Name: "manage_invoice",
		Description: "Facturas del restaurante (las de /app/facturas: misma numeración, mismo PDF, mismo sistema de envío). " +
			"create_draft: crea un BORRADOR con el cliente y las líneas y manda su PDF a este chat para revisarlo. " +
			"update_draft: corrige un borrador (manda solo lo que cambia; si mandas lines, sustituyen a todas); NO crees otro borrador para corregir, cada uno gasta un número de la serie. " +
			"send_pdf: vuelve a mandar a este chat el PDF de una factura. get: lee una factura. " +
			"send_to_client: envía la factura AL CLIENTE por email, whatsapp o both, y queda marcada como enviada; úsalo SOLO cuando te lo pidan expresamente. " +
			"Los precios de un ticket de restaurante llevan el IVA incluido: pásalos tal cual (prices_include_iva=true, el valor por defecto) con su iva_rate (10 por defecto) y manda expected_total con el total del ticket para comprobarlo. " +
			"El nombre o razón social y el email del cliente son obligatorios. NUNCA inventes ni completes datos fiscales: si no los tienes leídos con seguridad o escritos en el chat, pídelos.",
		InputSchema: botSchema(`{"type":"object","properties":{
			"action":{"type":"string","enum":["create_draft","update_draft","send_pdf","get","send_to_client"]},
			"invoice_id":{"type":"integer","description":"id de la factura"},
			"invoice_number":{"type":"string","description":"número de la factura (p. ej. F-2026-0011); vale en lugar de invoice_id en todas las acciones menos create_draft"},
			"customer":{"type":"object","description":"datos fiscales del cliente","properties":{
				"name":{"type":"string","description":"nombre o razón social"},
				"email":{"type":"string"},
				"tax_id":{"type":"string","description":"CIF o NIF"},
				"tax_id_type":{"type":"string","enum":["cif","dni"]},
				"phone":{"type":"string"},
				"phone_country_code":{"type":"string","description":"prefijo sin +, p. ej. 34"},
				"street":{"type":"string"},"number":{"type":"string"},"postal_code":{"type":"string"},
				"city":{"type":"string"},"province":{"type":"string"},"country":{"type":"string"}}},
			"lines":{"type":"array","items":{"type":"object","properties":{
				"description":{"type":"string"},
				"quantity":{"type":"number"},
				"unit_price":{"type":"number","description":"precio por unidad tal como aparece en el ticket"},
				"iva_rate":{"type":"number","description":"IVA en % de esta línea"}},
				"required":["description","quantity","unit_price"]}},
			"prices_include_iva":{"type":"boolean","description":"true (por defecto): los precios llevan el IVA incluido, como en un ticket"},
			"iva_rate":{"type":"number","description":"IVA en % de las líneas que no lo indican (10 por defecto)"},
			"expected_total":{"type":"number","description":"total del ticket, para comprobar que las líneas cuadran"},
			"invoice_date":{"type":"string","description":"YYYY-MM-DD (hoy por defecto)"},
			"payment_method":{"type":"string","enum":["efectivo","tarjeta","transferencia","bizum","cheque"]},
			"reservation_id":{"type":"integer","description":"solo si te piden asociarla a una reserva"},
			"notes":{"type":"string","description":"nota impresa en la factura"},
			"channel":{"type":"string","enum":["email","whatsapp","both"],"description":"solo send_to_client"},
			"message":{"type":"string","description":"solo send_to_client: texto propio para el cliente"}
		},"required":["action"]}`),
	}}
}

type invoiceToolCustomer struct {
	Name             string `json:"name,omitempty"`
	Email            string `json:"email,omitempty"`
	TaxID            string `json:"tax_id,omitempty"`
	TaxIDType        string `json:"tax_id_type,omitempty"`
	Phone            string `json:"phone,omitempty"`
	PhoneCountryCode string `json:"phone_country_code,omitempty"`
	Street           string `json:"street,omitempty"`
	Number           string `json:"number,omitempty"`
	PostalCode       string `json:"postal_code,omitempty"`
	City             string `json:"city,omitempty"`
	Province         string `json:"province,omitempty"`
	Country          string `json:"country,omitempty"`
}

type invoiceToolLine struct {
	Description string   `json:"description"`
	Quantity    float64  `json:"quantity"`
	UnitPrice   float64  `json:"unit_price"`
	IVARate     *float64 `json:"iva_rate,omitempty"`
}

// invoiceToolArgs is the tool input and, as stored in the proposal, exactly
// what the approved write runs.
type invoiceToolArgs struct {
	Action           string               `json:"action"`
	InvoiceID        int                  `json:"invoice_id,omitempty"`
	InvoiceNumber    string               `json:"invoice_number,omitempty"`
	Customer         *invoiceToolCustomer `json:"customer,omitempty"`
	Lines            []invoiceToolLine    `json:"lines,omitempty"`
	PricesIncludeIVA *bool                `json:"prices_include_iva,omitempty"`
	IVARate          *float64             `json:"iva_rate,omitempty"`
	ExpectedTotal    *float64             `json:"expected_total,omitempty"`
	InvoiceDate      string               `json:"invoice_date,omitempty"`
	PaymentMethod    string               `json:"payment_method,omitempty"`
	ReservationID    *int                 `json:"reservation_id,omitempty"`
	Notes            string               `json:"notes,omitempty"`
	Channel          string               `json:"channel,omitempty"`
	Message          string               `json:"message,omitempty"`
}

var botInvoiceWriteActions = map[string]bool{"create_draft": true, "update_draft": true, "send_to_client": true}

// botToolManageInvoice executes manage_invoice.
func (s *Server) botToolManageInvoice(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var a invoiceToolArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	actor, origin, ok := s.botPrivilegedControlAuthorize(ctx, restaurantID, msg)
	if !ok {
		botCheckpoint("wa_bot_invoice_v1", "restaurant_id=%d denied=actor action=%s", restaurantID, a.Action)
		return botJSON(map[string]any{"error": "no autorizado para gestionar facturas desde este chat", "terminal": true}), nil
	}
	// The id of a draft lives in the tool result of the turn that created it,
	// and the next turns do not see tool results: what the group (and the
	// model) keep is the NUMBER the bot told them. Resolved once here, so the
	// stored proposal always carries the id.
	if a.InvoiceID <= 0 && strings.TrimSpace(a.InvoiceNumber) != "" {
		id, err := s.botInvoiceIDByNumber(ctx, restaurantID, a.InvoiceNumber)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		a.InvoiceID = id
	}
	switch a.Action {
	case "get":
		inv, err := s.botInvoiceLoad(ctx, restaurantID, a.InvoiceID)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		out := botInvoiceSummary(inv)
		out["tool"] = "manage_invoice"
		return botJSON(out), nil
	case "send_pdf":
		inv, err := s.botInvoiceLoad(ctx, restaurantID, a.InvoiceID)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		if err := s.botInvoiceSendPDFToChat(ctx, restaurantID, msg, inv); err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		return botJSON(map[string]any{"ok": true, "tool": "manage_invoice", "status": "PDF de la factura " + botInvoiceLabel(inv) + " enviado a este chat."}), nil
	}
	if !botInvoiceWriteActions[a.Action] {
		return botJSON(map[string]any{"error": "acción no válida: usa create_draft, update_draft, send_pdf, get o send_to_client"}), nil
	}
	preview, err := s.botInvoicePreview(ctx, restaurantID, a)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el código de confirmación"}), nil
	}
	dedup := fmt.Sprintf("invoice:%s:%d", a.Action, a.InvoiceID)
	out, err := s.botProposalPersist(ctx, restaurantID, msg, "invoice", 0, a, dedup, actor, origin, "", "", "Propuesta: "+preview+". Nada está aplicado todavía.", code)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	botCheckpoint("wa_bot_invoice_v1", "restaurant_id=%d proposed action=%s invoice_id=%d", restaurantID, a.Action, a.InvoiceID)
	return out, nil
}

// botInvoicePreview validates a write and describes it. The same validation
// runs again when the write is applied (the invoice can change in between).
func (s *Server) botInvoicePreview(ctx context.Context, rid int, a invoiceToolArgs) (string, error) {
	switch a.Action {
	case "create_draft":
		in, err := botInvoiceBuildInput(a, nil)
		if err != nil {
			return "", err
		}
		return "crear un borrador de factura para " + botInvoiceCustomerLabel(in) + ", " + botInvoiceTotalsText(in), nil
	case "update_draft":
		inv, err := s.botInvoiceLoad(ctx, rid, a.InvoiceID)
		if err != nil {
			return "", err
		}
		if inv.Status != "borrador" {
			return "", fmt.Errorf("la factura %s ya está %s: solo se corrigen borradores", botInvoiceLabel(inv), inv.Status)
		}
		in, err := botInvoiceBuildInput(a, inv)
		if err != nil {
			return "", err
		}
		return "corregir el borrador " + botInvoiceLabel(inv) + " (" + botInvoiceCustomerLabel(in) + ", " + botInvoiceTotalsText(in) + ")", nil
	case "send_to_client":
		inv, err := s.botInvoiceLoad(ctx, rid, a.InvoiceID)
		if err != nil {
			return "", err
		}
		emailOn, waOn, err := botInvoiceChannels(a.Channel)
		if err != nil {
			return "", err
		}
		var to []string
		if emailOn {
			to = append(to, "email a "+inv.CustomerEmail)
		}
		if waOn {
			phone := invoicePhoneDisplay(inv)
			if phone == "" {
				return "", errors.New("la factura no tiene teléfono del cliente: corrígela con update_draft (customer.phone) o envíala solo por email")
			}
			to = append(to, "WhatsApp a "+phone)
		}
		return "enviar la factura " + botInvoiceLabel(inv) + " (" + inv.CustomerName + ", total " + botEuro(deref64(inv.Total)) + ") por " + strings.Join(to, " y "), nil
	}
	return "", errors.New("acción no válida")
}

// botInvoiceExecute applies an approved invoice proposal. Dispatched by
// botSpecialAdminExecute for op "invoice".
//
// The claim is committed BEFORE the write, unlike the other ops: creating an
// invoice consumes a number of the series and sending it reaches the client,
// so a failed claim commit after the write would leave the code usable and
// the next OK would create a second invoice or send it twice. A failed write
// after the claim only means asking again.
func (s *Server) botInvoiceExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	var a invoiceToolArgs
	if err := json.Unmarshal([]byte(p.Args), &a); err != nil {
		return "", errors.New("propuesta ilegible")
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New("error interno")
	}
	out, err := s.botInvoiceApply(ctx, rid, msg, p.ID, a)
	if err != nil {
		botCheckpoint("wa_bot_invoice_v1", "restaurant_id=%d apply_failed action=%s invoice_id=%d err=%v", rid, a.Action, a.InvoiceID, err)
		return "", err
	}
	botCheckpoint("wa_bot_invoice_v1", "restaurant_id=%d applied action=%s invoice_id=%d", rid, a.Action, a.InvoiceID)
	return out, nil
}

func (s *Server) botInvoiceApply(ctx context.Context, rid int, msg botWebhookMessage, proposalID int64, a invoiceToolArgs) (string, error) {
	hctx := botInvoiceHandlerContext(ctx, rid)
	switch a.Action {
	case "create_draft":
		in, err := botInvoiceBuildInput(a, nil)
		if err != nil {
			return "", err
		}
		body, code, err := s.assistantCallHandler(hctx, s.handleBOInvoiceCreate, assistantHandlerInput{Method: http.MethodPost, Body: botInvoiceBody(in)})
		if err != nil {
			return "", err
		}
		var res struct {
			Success bool   `json:"success"`
			ID      int    `json:"id"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &res)
		if code != http.StatusOK || !res.Success || res.ID <= 0 {
			return "", fmt.Errorf("el sistema de facturas no la ha creado: %s", botInvoiceHandlerMessage(res.Message, code))
		}
		return s.botInvoiceDraftResult(ctx, rid, msg, res.ID, "Borrador creado")
	case "update_draft":
		inv, err := s.botInvoiceLoad(ctx, rid, a.InvoiceID)
		if err != nil {
			return "", err
		}
		if inv.Status != "borrador" {
			return "", fmt.Errorf("la factura %s ya está %s: solo se corrigen borradores", botInvoiceLabel(inv), inv.Status)
		}
		in, err := botInvoiceBuildInput(a, inv)
		if err != nil {
			return "", err
		}
		body, code, err := s.assistantCallHandler(hctx, s.handleBOInvoiceUpdate, assistantHandlerInput{
			Method: http.MethodPut, Body: botInvoiceBody(in), URLParam: map[string]string{"id": strconv.Itoa(inv.ID)},
		})
		if err != nil {
			return "", err
		}
		var res struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &res)
		if code != http.StatusOK || !res.Success {
			return "", fmt.Errorf("el sistema de facturas no la ha guardado: %s", botInvoiceHandlerMessage(res.Message, code))
		}
		return s.botInvoiceDraftResult(ctx, rid, msg, inv.ID, "Borrador corregido")
	case "send_to_client":
		return s.botInvoiceSendToClient(hctx, rid, proposalID, a)
	}
	return "", errors.New("acción no válida")
}

// botInvoiceDraftResult reloads the saved draft, sends its PDF to the chat and
// describes the result, with the id the next actions need.
func (s *Server) botInvoiceDraftResult(ctx context.Context, rid int, msg botWebhookMessage, invoiceID int, what string) (string, error) {
	inv, err := s.botInvoiceLoad(ctx, rid, invoiceID)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("%s: factura %s (invoice_id=%d) para %s, %d líneas, base %s, IVA %s, total %s.",
		what, botInvoiceLabel(inv), inv.ID, inv.CustomerName, len(inv.LineItems), botEuro(deref64(inv.Subtotal)), botEuro(deref64(inv.IVAAmount)), botEuro(deref64(inv.Total)))
	if err := s.botInvoiceSendPDFToChat(ctx, rid, msg, inv); err != nil {
		return out + " El PDF NO se ha podido mandar a este chat: " + err.Error() + ".", nil
	}
	return out + " PDF del borrador enviado a este chat. No se ha enviado nada al cliente.", nil
}

// botInvoiceSendToClient sends the invoice through the panel's send system:
// email (handleBOInvoiceSend) and/or WhatsApp (handleBOInvoiceSendWhatsApp).
// Each channel reports on its own; the call fails only when none was sent.
func (s *Server) botInvoiceSendToClient(hctx context.Context, rid int, proposalID int64, a invoiceToolArgs) (string, error) {
	emailOn, waOn, err := botInvoiceChannels(a.Channel)
	if err != nil {
		return "", err
	}
	inv, err := s.botInvoiceLoad(hctx, rid, a.InvoiceID)
	if err != nil {
		return "", err
	}
	idParam := map[string]string{"id": strconv.Itoa(inv.ID)}
	var parts []string
	sent := false
	if emailOn {
		body := map[string]any{}
		if m := strings.TrimSpace(a.Message); m != "" {
			body["message"] = m
		}
		raw, code, err := s.assistantCallHandler(hctx, s.handleBOInvoiceSend, assistantHandlerInput{Method: http.MethodPost, Body: body, URLParam: idParam})
		var res struct {
			Success       bool     `json:"success"`
			Message       string   `json:"message"`
			PdfAttached   bool     `json:"pdf_attached"`
			MissingFields []string `json:"missingFields"`
		}
		if err == nil {
			_ = json.Unmarshal(raw, &res)
		}
		switch {
		case err == nil && code == http.StatusOK && res.Success:
			sent = true
			line := "Email enviado a " + inv.CustomerEmail
			if res.PdfAttached {
				line += " con el PDF adjunto"
			}
			parts = append(parts, line)
		case len(res.MissingFields) > 0:
			parts = append(parts, "Email NO enviado: falta configurar el correo del restaurante ("+strings.Join(res.MissingFields, ", ")+")")
		default:
			parts = append(parts, "Email NO enviado: "+botInvoiceHandlerMessage(res.Message, code))
		}
	}
	if waOn {
		body := map[string]any{
			// One key per approved proposal: a retry of the same send resumes
			// it (no duplicate), a new request is a new send.
			"idempotency_key": fmt.Sprintf("wabot-%d-%d", inv.ID, proposalID),
		}
		if m := strings.TrimSpace(a.Message); m != "" {
			body["message"] = m
		}
		raw, code, err := s.assistantCallHandler(hctx, s.handleBOInvoiceSendWhatsApp, assistantHandlerInput{Method: http.MethodPost, Body: body, URLParam: idParam})
		var res struct {
			Success         bool   `json:"success"`
			Code            string `json:"code"`
			Message         string `json:"message"`
			PdfAttached     bool   `json:"pdf_attached"`
			PhoneNormalized string `json:"phone_normalized"`
		}
		if err == nil {
			_ = json.Unmarshal(raw, &res)
		}
		switch {
		case err == nil && code == http.StatusOK && res.Success:
			sent = true
			line := "WhatsApp enviado a " + res.PhoneNormalized
			if res.PdfAttached {
				line += " con el PDF"
			} else {
				line += " (solo el texto: el PDF no se ha podido adjuntar)"
			}
			parts = append(parts, line)
		case res.Code == "phone_confirm_needed":
			parts = append(parts, "WhatsApp NO enviado: el teléfono de la factura no tiene prefijo de país y hay que confirmar si es "+res.PhoneNormalized+"; si es correcto, corrígelo con update_draft (customer.phone y customer.phone_country_code) y vuelve a enviarla")
		default:
			parts = append(parts, "WhatsApp NO enviado: "+botInvoiceHandlerMessage(res.Message, code))
		}
	}
	summary := "Factura " + botInvoiceLabel(inv) + ": " + strings.Join(parts, ". ") + "."
	if !sent {
		return "", errors.New(summary)
	}
	return summary, nil
}

// botInvoiceSendPDFToChat renders the invoice PDF and sends it to this chat.
// Each copy gets its own CDN object: the path the client send uses is
// overwritten on every send, and a cached older copy must never be the one
// the staff reviews.
func (s *Server) botInvoiceSendPDFToChat(ctx context.Context, rid int, msg botWebhookMessage, inv *Invoice) error {
	if s.botStoredTenantConfig(ctx, rid).DisableAttachments {
		return errors.New("los adjuntos están desactivados para este restaurante")
	}
	if !s.bunnyConfigured(ctx, rid) {
		return errors.New("no hay almacenamiento (BunnyCDN) configurado para mandar el PDF por WhatsApp")
	}
	info, _ := s.loadRestaurantInfo(ctx, rid)
	branding, _ := s.loadRestaurantBranding(ctx, rid)
	branding.LogoURL = s.resolveInvoiceLogoURL(ctx, rid, branding)
	pdf, err := s.generateInvoicePDF(ctx, inv, info, branding)
	if err != nil || len(pdf) == 0 {
		return fmt.Errorf("no se ha podido generar el PDF: %v", err)
	}
	objectPath := fmt.Sprintf("%d/facturas/whatsapp/factura_%d_%d.pdf", rid, inv.ID, time.Now().UnixNano())
	if err := s.bunnyPut(ctx, rid, objectPath, pdf, "application/pdf"); err != nil {
		return errors.New("no se ha podido subir el PDF al CDN del restaurante")
	}
	gw, ok := s.botGatewayFor(ctx, rid)
	if !ok {
		return errors.New("whatsapp no configurado")
	}
	caption := "Factura " + botInvoiceLabel(inv)
	if inv.Status == "borrador" {
		caption = "Borrador de factura " + botInvoiceLabel(inv)
	}
	caption += " · " + inv.CustomerName + " · total " + botEuro(deref64(inv.Total))
	if err := gw.SendMedia(ctx, msg.Sender, waMedia{
		Kind: "document", URL: s.bunnyPullURL(ctx, rid, objectPath), Filename: invoicePDFFilename(deref(inv.InvoiceNumber)), Caption: caption,
	}); err != nil {
		return fmt.Errorf("whatsapp no ha aceptado el PDF: %v", err)
	}
	s.botRecordConversationMessage(ctx, rid, msg.Sender, "assistant", "["+caption+" (PDF)]", "manage_invoice", "agent")
	botCheckpoint("wa_bot_invoice_v1", "restaurant_id=%d pdf_sent invoice_id=%d status=%s", rid, inv.ID, inv.Status)
	return nil
}

// botInvoiceHandlerContext is the auth the panel's invoice handlers read: the
// privileged chat acts as root on THIS restaurant (the bot's synthetic root
// carries no restaurant, and the handlers scope every query to it).
func botInvoiceHandlerContext(ctx context.Context, rid int) context.Context {
	return withBOAuth(ctx, boAuth{Role: "root", User: boUser{Role: "root"}, ActiveRestaurantID: rid})
}

func (s *Server) botInvoiceLoad(ctx context.Context, rid, invoiceID int) (*Invoice, error) {
	if invoiceID <= 0 {
		return nil, errors.New("falta invoice_id o invoice_number")
	}
	inv, err := s.loadFullInvoice(ctx, rid, invoiceID)
	if err != nil {
		return nil, errors.New("no se ha podido leer la factura")
	}
	if inv == nil {
		return nil, fmt.Errorf("no existe la factura %d en este restaurante", invoiceID)
	}
	return inv, nil
}

// botInvoiceIDByNumber resolves an invoice number of this restaurant ("F-2026-0011").
func (s *Server) botInvoiceIDByNumber(ctx context.Context, rid int, number string) (int, error) {
	number = strings.TrimSpace(number)
	var id int
	err := s.db.QueryRowContext(ctx, `SELECT id FROM invoices WHERE restaurant_id = ? AND invoice_number = ? ORDER BY id DESC LIMIT 1`, rid, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("no existe la factura %s en este restaurante", number)
	}
	if err != nil {
		return 0, errors.New("no se ha podido buscar la factura")
	}
	return id, nil
}

// botInvoiceBuildInput turns the tool arguments into the panel's payload. For
// update_draft it starts from the stored draft (number, template and every
// field kept) and applies only what the arguments carry.
func botInvoiceBuildInput(a invoiceToolArgs, stored *Invoice) (InvoiceInput, error) {
	var in InvoiceInput
	if stored != nil {
		raw, err := json.Marshal(stored)
		if err != nil {
			return in, errors.New("no se ha podido leer la factura")
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return in, errors.New("no se ha podido leer la factura")
		}
	} else {
		in.Status = "borrador"
		in.InvoiceDate = time.Now().In(affluenceMadridLocation()).Format("2006-01-02")
	}
	if c := a.Customer; c != nil {
		set := func(dst **string, v string) {
			if v = strings.TrimSpace(v); v != "" {
				*dst = &v
			}
		}
		if v := strings.TrimSpace(c.Name); v != "" {
			in.CustomerName = v
		}
		if v := strings.TrimSpace(c.Email); v != "" {
			in.CustomerEmail = v
		}
		if v := strings.TrimSpace(c.TaxID); v != "" {
			id := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", ".", "").Replace(v))
			kind := botInvoiceTaxIDType(id, c.TaxIDType)
			in.CustomerDniCif, in.CustomerDniCifType = &id, &kind
		}
		if v := strings.TrimSpace(c.Phone); v != "" {
			in.CustomerPhone = &v
			cc := onlyDigits(c.PhoneCountryCode)
			if cc == "" && !strings.HasPrefix(v, "+") && !strings.HasPrefix(v, "00") {
				cc = "34"
			}
			if cc != "" {
				in.CustomerPhoneCountryCode = &cc
			} else {
				in.CustomerPhoneCountryCode = nil
			}
		}
		set(&in.CustomerAddressStreet, c.Street)
		set(&in.CustomerAddressNumber, c.Number)
		set(&in.CustomerAddressPostalCode, c.PostalCode)
		set(&in.CustomerAddressCity, c.City)
		set(&in.CustomerAddressProvince, c.Province)
		set(&in.CustomerAddressCountry, c.Country)
	}
	if v := strings.TrimSpace(a.InvoiceDate); v != "" {
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return in, errors.New("invoice_date debe ser YYYY-MM-DD")
		}
		in.InvoiceDate = v
	}
	if v := strings.TrimSpace(a.PaymentMethod); v != "" {
		switch v {
		case "efectivo", "tarjeta", "transferencia", "bizum", "cheque":
			in.PaymentMethod = &v
		default:
			return in, errors.New("payment_method no válido")
		}
	}
	if a.ReservationID != nil && *a.ReservationID > 0 {
		id := *a.ReservationID
		in.IsReservation, in.ReservationID = true, &id
	}
	if v := strings.TrimSpace(a.Notes); v != "" {
		in.InternalNotes = &v
	}
	if len(a.Lines) > 0 {
		includeIVA := a.PricesIncludeIVA == nil || *a.PricesIncludeIVA
		rate := botInvoiceDefaultIVA
		if a.IVARate != nil {
			rate = *a.IVARate
		}
		lines, err := botInvoiceLines(a.Lines, includeIVA, rate)
		if err != nil {
			return in, err
		}
		in.LineItems = lines
	}

	var missing []string
	if strings.TrimSpace(in.CustomerName) == "" {
		missing = append(missing, "nombre o razón social del cliente")
	}
	if strings.TrimSpace(in.CustomerEmail) == "" {
		missing = append(missing, "email del cliente")
	} else if addr, err := mail.ParseAddress(in.CustomerEmail); err != nil || addr.Address != in.CustomerEmail {
		return in, fmt.Errorf("el email del cliente no es válido: %q", in.CustomerEmail)
	}
	if len(in.LineItems) == 0 {
		missing = append(missing, "líneas de la factura")
	}
	if len(missing) > 0 {
		return in, errors.New("faltan datos obligatorios: " + strings.Join(missing, ", ") + ". Pídelos en el grupo; no los inventes")
	}
	if a.ExpectedTotal != nil {
		total := botInvoiceComputedTotal(in)
		if math.Abs(total-*a.ExpectedTotal) >= 0.005 {
			return in, fmt.Errorf("las líneas suman %s y el total del ticket es %s: revisa cantidades y precios antes de crearla", botEuro(total), botEuro(*a.ExpectedTotal))
		}
	}
	return in, nil
}

// botInvoiceLines converts the lines of a bill into the panel's line items,
// whose unit_price is NET (the panel adds the IVA of each line).
//
// A bill prints prices with IVA included. Its line is stored as quantity 1 at
// the exact net amount of the line ("4 × MENU FIN DE SEMANA", 109,09 € + IVA =
// 120,00 €): a net unit price (27,2727) is stored with two decimals, and every
// later save recomputes 4 × 27,27 = 109,08 and drifts the total. The nets are
// fitted so the invoice total is exactly the bill total (botInvoiceFitNets).
func botInvoiceLines(lines []invoiceToolLine, includeIVA bool, defaultRate float64) ([]InvoiceLineItemInput, error) {
	out := make([]InvoiceLineItemInput, 0, len(lines))
	var grossIdx []int
	var grosses, rates []float64
	for i, l := range lines {
		desc := strings.TrimSpace(l.Description)
		if desc == "" {
			return nil, fmt.Errorf("la línea %d no tiene descripción", i+1)
		}
		if l.Quantity <= 0 || l.UnitPrice < 0 {
			return nil, fmt.Errorf("la línea %d (%s) tiene una cantidad o un precio no válidos", i+1, desc)
		}
		rate := defaultRate
		if l.IVARate != nil {
			rate = *l.IVARate
		}
		if rate < 0 || rate > 100 {
			return nil, fmt.Errorf("la línea %d (%s) tiene un IVA no válido", i+1, desc)
		}
		if !includeIVA {
			out = append(out, InvoiceLineItemInput{Description: desc, Quantity: l.Quantity, UnitPrice: l.UnitPrice, IVARate: rate})
			continue
		}
		if l.Quantity != 1 {
			desc = strconv.FormatFloat(l.Quantity, 'f', -1, 64) + " × " + desc
		}
		grossIdx = append(grossIdx, len(out))
		grosses = append(grosses, round2(l.Quantity*l.UnitPrice))
		rates = append(rates, rate)
		out = append(out, InvoiceLineItemInput{Description: desc, Quantity: 1, IVARate: rate})
	}
	for k, net := range botInvoiceFitNets(grosses, rates) {
		out[grossIdx[k]].UnitPrice = net
	}
	return out, nil
}

// botInvoiceLineGross is what the panel adds up for a line of net c: the net
// plus its rounded IVA (deriveBillingValues sums both rounded amounts).
func botInvoiceLineGross(c, rate float64) float64 {
	return round2(c + round2(c*rate/100))
}

// botInvoiceFitNets returns two-decimal nets whose panel totals add up exactly
// to the bill: each line hits its printed amount when a net can, and the
// cent left over by an amount no net reaches (1,15 € at 10 %: 1,04 → 1,14 and
// 1,05 → 1,16) moves to the largest line that can take it.
func botInvoiceFitNets(grosses, rates []float64) []float64 {
	nets := make([]float64, len(grosses))
	target, sum := 0.0, 0.0
	for i, g := range grosses {
		nets[i] = botInvoiceNetForGross(g, rates[i])
		target += g
		sum += botInvoiceLineGross(nets[i], rates[i])
	}
	order := make([]int, len(grosses))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return grosses[order[a]] > grosses[order[b]] })
	for iter := 0; iter < 4*len(grosses) && math.Abs(round2(target)-round2(sum)) >= 0.005; iter++ {
		step := 0.01
		if target < sum {
			step = -0.01
		}
		moved := false
		for _, i := range order {
			c := round2(nets[i] + step)
			if c < 0 {
				continue
			}
			if delta := botInvoiceLineGross(c, rates[i]) - botInvoiceLineGross(nets[i], rates[i]); math.Abs(delta-step) < 0.005 {
				sum += delta
				nets[i] = c
				moved = true
				break
			}
		}
		if !moved {
			break
		}
	}
	return nets
}

// botInvoiceNetForGross returns the two-decimal net whose panel total is
// exactly gross (the plain division is a cent off on some amounts), or the
// closest one when no net reaches it.
func botInvoiceNetForGross(gross, rate float64) float64 {
	if rate == 0 {
		return gross
	}
	base := round2(gross / (1 + rate/100))
	for _, d := range []float64{0, -0.01, 0.01, -0.02, 0.02} {
		if c := round2(base + d); math.Abs(botInvoiceLineGross(c, rate)-gross) < 0.005 {
			return c
		}
	}
	return base
}

// botInvoiceComputedTotal is the total the panel will store for the input.
func botInvoiceComputedTotal(in InvoiceInput) float64 {
	bv := deriveBillingValues(&in, computeLineItems(in.LineItems))
	return deref64(bv.Total)
}

func botInvoiceTotalsText(in InvoiceInput) string {
	bv := deriveBillingValues(&in, computeLineItems(in.LineItems))
	return fmt.Sprintf("%d líneas, base %s, IVA %s, total %s", len(in.LineItems), botEuro(deref64(bv.Subtotal)), botEuro(deref64(bv.IVAAmount)), botEuro(deref64(bv.Total)))
}

func botInvoiceCustomerLabel(in InvoiceInput) string {
	label := in.CustomerName
	if in.CustomerDniCif != nil && *in.CustomerDniCif != "" {
		label += " (" + strings.ToUpper(deref(in.CustomerDniCifType)) + " " + *in.CustomerDniCif + ")"
	}
	return label + ", email " + in.CustomerEmail
}

// botInvoiceTaxIDType is the panel's NIF/CIF label: a Spanish company id
// starts with one of the CIF organisation letters; anything else (DNI, NIE)
// prints as NIF.
func botInvoiceTaxIDType(id, given string) string {
	switch strings.ToLower(strings.TrimSpace(given)) {
	case "cif":
		return "cif"
	case "dni":
		return "dni"
	}
	if id != "" && strings.ContainsRune("ABCDEFGHJNPQRSUVW", rune(id[0])) {
		return "cif"
	}
	return "dni"
}

func botInvoiceChannels(channel string) (email, whatsapp bool, err error) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "email":
		return true, false, nil
	case "whatsapp":
		return false, true, nil
	case "both", "ambos":
		return true, true, nil
	}
	return false, false, errors.New("channel debe ser email, whatsapp o both")
}

// botInvoiceBody is the JSON body of the panel's create/update handlers.
func botInvoiceBody(in InvoiceInput) map[string]any {
	raw, _ := json.Marshal(in)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return body
}

func botInvoiceHandlerMessage(msg string, code int) string {
	if m := strings.TrimSpace(msg); m != "" {
		return m
	}
	return fmt.Sprintf("error %d", code)
}

func botInvoiceLabel(inv *Invoice) string {
	if n := strings.TrimSpace(deref(inv.InvoiceNumber)); n != "" {
		return n
	}
	return fmt.Sprintf("#%d", inv.ID)
}

// botInvoiceSummary is what get answers.
func botInvoiceSummary(inv *Invoice) map[string]any {
	lines := make([]map[string]any, 0, len(inv.LineItems))
	for _, l := range inv.LineItems {
		lines = append(lines, map[string]any{"description": l.Description, "quantity": l.Quantity, "unit_price_net": l.UnitPrice, "iva_rate": l.IVARate, "total": l.Total})
	}
	return map[string]any{
		"invoice_id": inv.ID, "number": botInvoiceLabel(inv), "status": inv.Status, "invoice_date": inv.InvoiceDate,
		"customer": map[string]any{
			"name": inv.CustomerName, "email": inv.CustomerEmail, "tax_id": deref(inv.CustomerDniCif), "phone": invoicePhoneDisplay(inv),
			"street": deref(inv.CustomerAddressStreet), "number": deref(inv.CustomerAddressNumber), "postal_code": deref(inv.CustomerAddressPostalCode),
			"city": deref(inv.CustomerAddressCity), "province": deref(inv.CustomerAddressProvince), "country": deref(inv.CustomerAddressCountry),
		},
		"lines": lines, "subtotal": deref64(inv.Subtotal), "iva": deref64(inv.IVAAmount), "total": deref64(inv.Total),
	}
}

func botEuro(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1) + " €"
}

func deref64(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
