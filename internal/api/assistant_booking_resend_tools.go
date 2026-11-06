package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Coordination id: booking_confirmation_resend_v1 (Forky half)
//
// Forky tool over the shared core (booking_confirmation_resend.go): the read
// answers the exact preview, the send waits for the one-shot confirmation
// token and then replays the panel POST /admin/bookings/{id}/resend-confirmation
// so panel, assistant and bot share one core and one audit path.

func init() {
	assistantToolRegistry = append(assistantToolRegistry,
		assistantTool{
			Name: "booking_confirmation_resend",
			Description: "Reenvía al cliente el WhatsApp de confirmación de una reserva existente: el resumen al teléfono ACTUAL de la reserva y, si es de fecha especial, su QR; si es prereserva de fecha festiva con abonos registrados, regenera y envía también el PDF de comprobante de pago del adelanto. " +
				"Úsalo cuando el cliente no recibió la confirmación (creada por WhatsApp, teléfono cambiado después, envío fallido). Primero confirmed=false (devuelve la vista previa exacta de lo que se enviará) y, cuando el usuario confirme, confirmed=true con los mismos datos y el confirmation_token. La reserva no cambia.",
			Write: true, Confirm: true, BackofficeOnly: true, Section: "reservas",
			Schema: json.RawMessage(`{"type":"object","properties":{"booking_id":{"type":"integer","description":"ID de la reserva"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["booking_id","confirmed"]}`),
			Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
				return s.assistantBookingConfirmationResend(ctx, rid, input)
			},
		},
	)
}

func (s *Server) assistantBookingConfirmationResend(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		BookingID         int64  `json:"booking_id"`
		Confirmed         bool   `json:"confirmed"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.BookingID <= 0 {
		return "", errors.New("booking_id inválido")
	}
	if !in.Confirmed {
		preview, _, err := s.bookingResendConfirmationPreview(ctx, rid, in.BookingID)
		if err != nil {
			return "", err
		}
		tok, err := s.assistantRequireConfirmation(rid, "booking_confirmation_resend", input)
		if err != nil {
			return "", err
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(tok), &out)
		out["preview"] = preview
		return botJSON(out), nil
	}
	if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, "booking_confirmation_resend", input); err != nil {
		return "", err
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOBookingResendConfirmation, assistantHandlerInput{
		Method:   http.MethodPost,
		URLParam: map[string]string{"id": strconv.FormatInt(in.BookingID, 10)},
	})
	if err != nil {
		return "", assistantHandlerError("booking_confirmation_resend", body, code)
	}
	return botHandlerResponse("booking_confirmation_resend", body)
}
