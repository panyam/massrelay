package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"

	"github.com/panyam/servicekit/grpcws"
	gohttp "github.com/panyam/servicekit/http"
)

// ApiHandler handles HTTP requests for the relay API.
type ApiHandler struct {
	app *RelayApp
}

// NewApiHandler creates a new ApiHandler.
func NewApiHandler(app *RelayApp) *ApiHandler {
	return &ApiHandler{app: app}
}

// SetupRoutes registers all API routes on the app's mux.
func (h *ApiHandler) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("GET /api/v1/rooms/{session_id}", h.HandleGetRoom)

	// WebSocket bidi endpoint using servicekit grpcws
	streamCfg := DefaultStreamConfig()
	wsHandler := grpcws.NewBidiStreamHandler(
		func(ctx context.Context) (*CollabBidiStream, error) {
			return NewCollabBidiStream(ctx, h.app.Service, streamCfg), nil
		},
		func() *pb.CollabAction { return &pb.CollabAction{} },
	)
	// WebSocket handler with metrics, wrapped by Guard (origin + rate limit + conn limit)
	rawWSHandler := gohttp.WSServe(wsHandler, nil)
	metrics := h.app.Metrics
	wsHandlerFunc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.ConnectionsTotal.Add(r.Context(), 1)
		rawWSHandler(w, r)
	})

	// Guard wraps: origin check → rate limit → connection limit → handler
	mux.Handle("/ws/v1/{session_id}/sync", h.app.Guard.Wrap(wsHandlerFunc))
	slog.Info("Registered WebSocket handler", "path", "/ws/v1/{session_id}/sync")

	// Admin endpoints (token-gated)
	if h.app.AdminToken != "" {
		mux.HandleFunc("GET /admin/rooms", h.requireAdmin(h.HandleAdminListRooms))
		mux.HandleFunc("GET /admin/rooms/{session_id}", h.requireAdmin(h.HandleAdminGetRoom))
		mux.HandleFunc("GET /admin/status", h.requireAdmin(h.HandleAdminStatus))
	}
}

// startTime is set when the API handler is created, for uptime reporting.
var startTime = time.Now()

// HandleHealth returns a health check response with relay stats.
func (h *ApiHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	svc := h.app.Service
	rooms, peers := svc.RoomAndPeerCount()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"uptime_seconds": int(time.Since(startTime).Seconds()),
		"rooms":          rooms,
		"peers":          peers,
		"goroutines":     runtime.NumGoroutine(),
	})
}

// HandleListRooms returns all active rooms.
func (h *ApiHandler) HandleListRooms(w http.ResponseWriter, r *http.Request) {
	resp, err := h.app.Service.ListRooms(r.Context(), &pb.ListRoomsRequest{})
	w.Header().Set("Content-Type", "application/json")
	if err != nil || resp == nil {
		json.NewEncoder(w).Encode(map[string]any{"rooms": []any{}})
		return
	}
	// Proto omitempty drops empty slices, so wrap to ensure "rooms" key is always present
	json.NewEncoder(w).Encode(map[string]any{"rooms": resp.GetRooms()})
}

// HandleGetRoom returns info about a specific room.
func (h *ApiHandler) HandleGetRoom(w http.ResponseWriter, r *http.Request) {
	sessionId := r.PathValue("session_id")
	resp, err := h.app.Service.GetRoom(r.Context(), &pb.GetRoomRequest{SessionId: sessionId})
	if err != nil || resp == nil {
		http.Error(w, `{"error":"room not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// requireAdmin returns a middleware that checks for a valid Bearer token.
func (h *ApiHandler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == auth || subtle.ConstantTimeCompare([]byte(token), []byte(h.app.AdminToken)) != 1 {
			slog.Warn("Admin auth failed", "component", "http", "ip", r.RemoteAddr, "path", r.URL.Path)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// HandleAdminStatus returns a full relay status overview.
func (h *ApiHandler) HandleAdminStatus(w http.ResponseWriter, r *http.Request) {
	svc := h.app.Service
	rooms, peers := svc.RoomAndPeerCount()

	// Build per-room summaries
	listResp, _ := svc.ListRooms(r.Context(), &pb.ListRoomsRequest{})
	var roomSummaries []map[string]any
	if listResp != nil {
		for _, summary := range listResp.GetRooms() {
			entry := map[string]any{
				"sessionId": summary.GetSessionId(),
				"peers":     summary.GetPeerCount(),
				"createdAt": summary.GetCreatedAt().AsTime().Format(time.RFC3339),
			}
			// Enrich with full room data if available
			if resp, err := svc.GetRoom(r.Context(), &pb.GetRoomRequest{SessionId: summary.GetSessionId()}); err == nil && resp != nil {
				room := resp.GetRoom()
				entry["title"] = room.GetTitle()
				entry["encrypted"] = room.GetEncrypted()
				entry["ownerClientId"] = room.GetOwnerClientId()
			}
			roomSummaries = append(roomSummaries, entry)
		}
	}
	if roomSummaries == nil {
		roomSummaries = []map[string]any{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"uptime_seconds": int(time.Since(startTime).Seconds()),
		"rooms":          rooms,
		"peers":          peers,
		"goroutines":     runtime.NumGoroutine(),
		"room_details":   roomSummaries,
	})
}

// HandleAdminListRooms returns all active rooms with peer details.
func (h *ApiHandler) HandleAdminListRooms(w http.ResponseWriter, r *http.Request) {
	h.HandleListRooms(w, r)
}

// HandleAdminGetRoom returns detailed info about a specific room.
func (h *ApiHandler) HandleAdminGetRoom(w http.ResponseWriter, r *http.Request) {
	sessionId := r.PathValue("session_id")
	resp, err := h.app.Service.GetRoom(r.Context(), &pb.GetRoomRequest{SessionId: sessionId})
	if err != nil || resp == nil {
		http.Error(w, `{"error":"room not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleSessionByHint looks up an active session by client hint.
// GET /api/v1/session-by-hint?hint=<browserId:drawingId>
func (h *ApiHandler) HandleSessionByHint(w http.ResponseWriter, r *http.Request) {
	hint := r.URL.Query().Get("hint")
	if hint == "" {
		http.Error(w, `{"error":"hint parameter required"}`, http.StatusBadRequest)
		return
	}
	sessionId := h.app.Service.FindSessionByHint(hint)
	if sessionId == "" {
		http.Error(w, `{"error":"no session for hint"}`, http.StatusNotFound)
		return
	}
	// Return the room info for this session
	resp, err := h.app.Service.GetRoom(r.Context(), &pb.GetRoomRequest{SessionId: sessionId})
	if err != nil || resp == nil {
		http.Error(w, `{"error":"session expired"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
