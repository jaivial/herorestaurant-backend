package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// BookingHub manages WebSocket connections for real-time booking updates
type bookingHub struct {
	clients    map[*bookingClient]bool
	broadcast  chan []byte
	register   chan *bookingClient
	unregister chan *bookingClient
	mu         sync.RWMutex
	upgrader   websocket.Upgrader
}

type bookingClient struct {
	hub       *bookingHub
	conn      *websocket.Conn
	send      chan []byte
	restaurantID int
}

// BookingEvent represents a SignalR-style message
type BookingEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func newBookingHub() *bookingHub {
	return &bookingHub{
		clients:    make(map[*bookingClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *bookingClient),
		unregister: make(chan *bookingClient),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (h *bookingHub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("Booking WebSocket client connected for restaurant %d", client.restaurantID)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("Booking WebSocket client disconnected for restaurant %d", client.restaurantID)

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *bookingHub) BroadcastToRestaurant(restaurantID int, eventType string, payload interface{}) {
	msg := BookingEvent{
		Type:    eventType,
		Payload: bookingMustJSON(payload),
	}
	data, _ := json.Marshal(msg)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if client.restaurantID == restaurantID {
			select {
			case client.send <- data:
			default:
			}
		}
	}
}

func bookingMustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// Server-side booking hub instance
var bookingsHub = newBookingHub()

func init() {
	go bookingsHub.run()
}

// handleBookingWebSocket handles the WebSocket connection for booking updates
func (s *Server) handleBookingWebSocket(w http.ResponseWriter, r *http.Request) {
	// Get restaurant ID from session
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	restaurantID := a.ActiveRestaurantID

	conn, err := bookingsHub.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &bookingClient{
		hub:           bookingsHub,
		conn:          conn,
		send:          make(chan []byte, 256),
		restaurantID: restaurantID,
	}

	bookingsHub.register <- client

	// Send welcome message
	welcomeMsg, _ := json.Marshal(BookingEvent{
		Type:    "hello",
		Payload: bookingMustJSON(map[string]interface{}{"restaurantId": restaurantID}),
	})
	client.send <- welcomeMsg

	go client.writePump()
	go client.readPump()
}

func (c *bookingClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		// Handle incoming messages (e.g., join restaurant group)
		var msg BookingEvent
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "JoinRestaurantGroup":
			var payload map[string]string
			if json.Unmarshal(msg.Payload, &payload) == nil {
				// Update client's restaurant ID (payload["restaurantId"])
				_ = payload["restaurantId"]
			}
		}
	}
}

func (c *bookingClient) writePump() {
	defer c.conn.Close()
	for {
		message, ok := <-c.send
		if !ok {
			c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}

		if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			break
		}
	}
}

// Routes for WebSocket
func (s *Server) bookingWebSocketRoutes(r chi.Router) {
	r.Get("/bookings/ws", s.handleBookingWebSocket)
}
