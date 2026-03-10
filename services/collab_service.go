package services

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CollabClient represents a connected peer in a room. Each WebSocket
// connection produces one CollabClient instance that lives for the
// duration of the connection.
//
// Wire-visible peer state (ClientId, Username, AvatarUrl, ClientType,
// IsActive, IsOwner) is embedded via *pb.PeerInfo so that GetPeerInfo()
// and ToProto() can reuse it directly without manual field copying.
// Server-only fields (SessionId, Metadata, BrowserId, SendCh) remain
// as separate struct fields.
type CollabClient struct {
	*pb.PeerInfo
	// SessionId is the room this client belongs to (server-only; redundant
	// on the wire since all peers in a room share the same sessionId).
	SessionId string
	// BrowserId is a localStorage UUID shared across tabs in the same browser,
	// used for same-browser ownership transfer (server-only; not exposed on wire).
	BrowserId string
	// SendCh is the buffered channel (cap 64) for delivering CollabEvents to
	// this client's WebSocket. Broadcasts use non-blocking sends — events are
	// dropped if the channel is full.
	SendCh chan *pb.CollabEvent
}

// CollabService manages rooms and peer lifecycle.
type CollabService struct {
	rooms           map[string]*CollabRoom
	hintIndex       map[string]string // client_hint → sessionId (for session reuse)
	mu              sync.RWMutex
	MaxPeersPerRoom int   // 0 = unlimited, default 10
	ProtocolVersion int32 // relay protocol version, default 2
	// LogPayloads enables logging of first N chars of content payloads (SceneUpdate, TextUpdate, SceneInitResponse).
	// Useful for verifying E2EE — encrypted data appears as base64, plaintext as JSON.
	// 0 = disabled (default), >0 = number of chars to log.
	LogPayloads int
	// Metrics callbacks (set by server layer, nil-safe)
	OnRoomCreated  func()
	OnRoomRemoved  func()
	OnPeerJoined   func()
	OnPeerLeft     func()
	OnMessageRelay func(actionType string)
}

// NewCollabService creates a new CollabService.
func NewCollabService() *CollabService {
	return &CollabService{
		rooms:           make(map[string]*CollabRoom),
		hintIndex:       make(map[string]string),
		MaxPeersPerRoom: 10,
		ProtocolVersion: 2,
	}
}

// GetOrCreateRoom returns the room for sessionId, creating it if needed.
// The second return value indicates whether the room was newly created.
func (s *CollabService) GetOrCreateRoom(sessionId string) (*CollabRoom, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, ok := s.rooms[sessionId]
	if !ok {
		room = NewCollabRoom(sessionId)
		s.rooms[sessionId] = room
		return room, true
	}
	return room, false
}

// GetRoom returns the room for sessionId without creating.
// Returns nil if the room does not exist.
func (s *CollabService) GetRoomByID(sessionId string) *CollabRoom {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rooms[sessionId]
}

// removeRoom removes an empty room and cleans up hint index. Caller must NOT hold s.mu.
func (s *CollabService) removeRoom(sessionId string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if room, ok := s.rooms[sessionId]; ok && room.IsEmpty() {
		delete(s.rooms, sessionId)
		// Clean up hint index entries pointing to this session
		for hint, sid := range s.hintIndex {
			if sid == sessionId {
				delete(s.hintIndex, hint)
			}
		}
		if s.OnRoomRemoved != nil {
			s.OnRoomRemoved()
		}
	}
}

// findRoomForClient locates the room containing clientId.
// Returns nil if the client is not in any room.
func (s *CollabService) findRoomForClient(clientId string) *CollabRoom {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.rooms {
		r.mu.RLock()
		_, ok := r.Clients[clientId]
		r.mu.RUnlock()
		if ok {
			return r
		}
	}
	return nil
}

// FindSessionByHint looks up a sessionId by client hint. Returns empty string if not found.
func (s *CollabService) FindSessionByHint(hint string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hintIndex[hint]
}

// GetClientSendCh returns the broadcast channel for a client in a room.
// Returns nil if the room or client is not found.
func (s *CollabService) GetClientSendCh(sessionId, clientId string) chan *pb.CollabEvent {
	s.mu.RLock()
	room, ok := s.rooms[sessionId]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	room.mu.RLock()
	defer room.mu.RUnlock()
	if c, ok := room.Clients[clientId]; ok {
		return c.SendCh
	}
	return nil
}

// HandleAction processes a single CollabAction from a client stream.
// It routes the action to the appropriate handler based on the oneof type:
//
//   - JoinRoom → handleJoin (returns RoomJoined or ErrorEvent)
//   - LeaveRoom → handleLeave (returns PeerLeft, may trigger SessionEnded)
//   - PresenceUpdate → handlePresence (broadcasts, returns nil)
//   - SceneUpdate, CursorUpdate, TextUpdate, SceneInitRequest/Response,
//     CredentialsChanged, TitleChanged → handleBroadcast (broadcasts, returns nil)
//
// For join/leave actions, the returned event is sent directly to the acting client.
// For broadcast actions, the return is nil (events are fanned out via BroadcastExcept).
// Returns an error for unknown action types or if the client is not in any room.
func (s *CollabService) HandleAction(ctx context.Context, action *pb.CollabAction) (*pb.CollabEvent, error) {
	if action == nil {
		return nil, fmt.Errorf("nil action")
	}
	slog.Debug("HandleAction", "component", "relay", "client", action.GetClientId(), "type", fmt.Sprintf("%T", action.Action))
	switch action.Action.(type) {
	case *pb.CollabAction_Join:
		return s.handleJoin(ctx, action)
	case *pb.CollabAction_Leave:
		return s.handleLeave(ctx, action)
	case *pb.CollabAction_Presence:
		return s.handlePresence(ctx, action)
	case *pb.CollabAction_SceneUpdate,
		*pb.CollabAction_CursorUpdate,
		*pb.CollabAction_TextUpdate,
		*pb.CollabAction_SceneInitRequest,
		*pb.CollabAction_SceneInitResponse,
		*pb.CollabAction_CredentialsChanged,
		*pb.CollabAction_TitleChanged:
		return s.handleBroadcast(ctx, action)
	default:
		return nil, fmt.Errorf("unknown or empty action type")
	}
}

// RoomAndPeerCount returns the number of active rooms and total connected peers.
func (s *CollabService) RoomAndPeerCount() (rooms, peers int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rooms = len(s.rooms)
	for _, r := range s.rooms {
		peers += r.ClientCount()
	}
	return
}

// GetRoom returns information about a room identified by req.SessionId.
// Returns a GetRoomResponse with peers, owner, metadata, encryption status, and title.
// Returns an error if the room does not exist.
func (s *CollabService) GetRoom(ctx context.Context, req *pb.GetRoomRequest) (*pb.GetRoomResponse, error) {
	s.mu.RLock()
	room, ok := s.rooms[req.GetSessionId()]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("room not found: %s", req.GetSessionId())
	}
	return &pb.GetRoomResponse{
		Room: room.ToProto(),
	}, nil
}

// ListRooms returns a summary of all active rooms (sessionId, peer count,
// creation time). The response order is non-deterministic (Go map iteration).
// Note: this endpoint is intentionally not registered in HTTP routes to
// prevent session enumeration; it is available for programmatic/admin use.
func (s *CollabService) ListRooms(ctx context.Context, req *pb.ListRoomsRequest) (*pb.ListRoomsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rooms := make([]*pb.RoomSummary, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, &pb.RoomSummary{
			SessionId: room.SessionId,
			PeerCount: int32(room.ClientCount()),
			CreatedAt: timestamppb.New(room.Created),
		})
	}
	return &pb.ListRoomsResponse{Rooms: rooms}, nil
}

// ─── Internal handlers ──────────────────────────

// handleJoin processes a JoinRoom action. It resolves the session ID (from the
// action, a client hint, or a newly generated UUID), enforces participant limits
// and encryption/protocol checks, snapshots existing peers, adds the new client,
// sets room ownership and metadata, broadcasts PeerJoined to existing clients, and
// returns a RoomJoined event to the joining client. Returns an ErrorEvent (not a
// Go error) for ROOM_FULL or PROTOCOL_VERSION_TOO_OLD conditions.
func (s *CollabService) handleJoin(ctx context.Context, action *pb.CollabAction) (*pb.CollabEvent, error) {
	join := action.GetJoin()
	username := join.GetUsername()
	if username == "" {
		username = "Anonymous"
	}

	isOwner := join.GetIsOwner()
	browserId := join.GetBrowserId()
	clientHint := join.GetClientHint()
	sessionId := join.GetSessionId()

	// If no session_id provided, resolve via hint or generate a new one
	if sessionId == "" {
		if clientHint != "" {
			s.mu.RLock()
			existing, ok := s.hintIndex[clientHint]
			s.mu.RUnlock()
			if ok {
				sessionId = existing
			}
		}
		if sessionId == "" {
			sessionId = uuid.New().String()
		}
	}

	// Index the hint → sessionId mapping
	if clientHint != "" {
		s.mu.Lock()
		s.hintIndex[clientHint] = sessionId
		s.mu.Unlock()
	}

	room, isNew := s.GetOrCreateRoom(sessionId)
	if isNew && s.OnRoomCreated != nil {
		s.OnRoomCreated()
	}

	// Participant limit check — return graceful ErrorEvent, not a stream-killing error
	if s.MaxPeersPerRoom > 0 && room.ClientCount() >= s.MaxPeersPerRoom {
		return &pb.CollabEvent{
			EventId: uuid.New().String(),
			Event: &pb.CollabEvent_Error{
				Error: &pb.ErrorEvent{
					Code:    "ROOM_FULL",
					Message: fmt.Sprintf("Room is full (%d/%d)", room.ClientCount(), s.MaxPeersPerRoom),
				},
			},
		}, nil
	}

	// Owner validation
	if isOwner {
		room.mu.Lock()
		if room.OwnerClientId != "" && room.OwnerBrowserId != browserId {
			room.mu.Unlock()
			return nil, fmt.Errorf("room already has an owner from a different browser")
		}
		room.mu.Unlock()
	}

	// Encrypted room: owner declares it, old clients rejected
	if isOwner && join.GetEncrypted() {
		room.mu.Lock()
		room.Encrypted = true
		room.mu.Unlock()
	}
	room.mu.RLock()
	roomEncrypted := room.Encrypted
	room.mu.RUnlock()
	if roomEncrypted && join.GetProtocolVersion() < 2 {
		return &pb.CollabEvent{
			EventId: uuid.New().String(),
			Event: &pb.CollabEvent_Error{
				Error: &pb.ErrorEvent{
					Code:    "PROTOCOL_VERSION_TOO_OLD",
					Message: "This room requires encryption (protocol version >= 2)",
				},
			},
		}, nil
	}

	// Snapshot existing peers BEFORE adding the new client
	existingPeers := room.GetPeerInfo()

	clientId := uuid.New().String()
	client := &CollabClient{
		PeerInfo: &pb.PeerInfo{
			ClientId:   clientId,
			Username:   username,
			ClientType: join.GetClientType(),
			AvatarUrl:  join.GetAvatarUrl(),
			IsActive:   true,
			IsOwner:    isOwner,
			Metadata:   join.GetMetadata(),
		},
		SessionId: sessionId,
		BrowserId: browserId,
		SendCh:    make(chan *pb.CollabEvent, 64),
	}
	room.AddClient(client)
	if s.OnPeerJoined != nil {
		s.OnPeerJoined()
	}

	// Set room ownership and metadata
	room.mu.Lock()
	if isOwner {
		room.OwnerClientId = clientId
		room.OwnerBrowserId = browserId
	}
	if room.Metadata == nil {
		room.Metadata = join.GetMetadata()
	}
	if room.Title == "" {
		room.Title = join.GetTitle()
	}
	room.mu.Unlock()

	// Broadcast PeerJoined to existing clients
	room.BroadcastExcept(&pb.CollabEvent{
		EventId:      uuid.New().String(),
		FromClientId: clientId,
		Event: &pb.CollabEvent_PeerJoined{
			PeerJoined: &pb.PeerJoined{
				Peer: client.PeerInfo,
			},
		},
	}, clientId)

	// Return RoomJoined to the joining client
	return &pb.CollabEvent{
		EventId: uuid.New().String(),
		Event: &pb.CollabEvent_RoomJoined{
			RoomJoined: &pb.RoomJoined{
				ClientId: clientId,
				Room: &pb.Room{
					SessionId:     sessionId,
					Peers:         existingPeers,
					OwnerClientId: room.OwnerClientId,
					Encrypted:     roomEncrypted,
					Title:         room.Title,
					CreatedAt:     timestamppb.New(room.Created),
					Metadata:      room.Metadata,
				},
				MaxPeers:        int32(s.MaxPeersPerRoom),
				ProtocolVersion: s.ProtocolVersion,
			},
		},
	}, nil
}

// handleLeave removes a client from its room and manages ownership transfer.
// If the leaving client is the owner and other peers remain:
//   - If a same-browser tab exists (matching BrowserId), ownership transfers
//     and OwnerChanged is broadcast.
//   - Otherwise, SessionEnded is broadcast, all client channels are closed,
//     and the room is deleted.
//
// Non-owner leaves simply broadcast PeerLeft. Empty rooms are cleaned up.
func (s *CollabService) handleLeave(ctx context.Context, action *pb.CollabAction) (*pb.CollabEvent, error) {
	clientId := action.GetClientId()

	room := s.findRoomForClient(clientId)
	if room == nil {
		return nil, fmt.Errorf("client %s not found in any room", clientId)
	}

	// Check if the leaving client is the owner
	room.mu.RLock()
	leavingClient := room.Clients[clientId]
	isOwner := leavingClient != nil && leavingClient.IsOwner
	browserId := ""
	if leavingClient != nil {
		browserId = leavingClient.BrowserId
	}
	room.mu.RUnlock()

	room.RemoveClient(clientId)
	if s.OnPeerLeft != nil {
		s.OnPeerLeft()
	}
	remainingCount := room.ClientCount()

	if isOwner && remainingCount > 0 {
		// Try to transfer ownership to a same-browser tab
		var successor *CollabClient
		if browserId != "" {
			successor = room.FindByBrowserId(browserId, clientId)
		}

		if successor != nil {
			// Transfer ownership
			room.mu.Lock()
			successor.IsOwner = true
			room.OwnerClientId = successor.ClientId
			room.mu.Unlock()

			room.BroadcastToAll(&pb.CollabEvent{
				EventId:      uuid.New().String(),
				FromClientId: clientId,
				Event: &pb.CollabEvent_OwnerChanged{
					OwnerChanged: &pb.OwnerChanged{
						NewOwnerClientId: successor.ClientId,
					},
				},
			})
		} else {
			// No same-browser successor — session dies
			room.BroadcastToAll(&pb.CollabEvent{
				EventId:      uuid.New().String(),
				FromClientId: clientId,
				Event: &pb.CollabEvent_SessionEnded{
					SessionEnded: &pb.SessionEnded{
						Reason: "owner_disconnected",
					},
				},
			})
			room.CloseAllClients()
			s.removeRoom(room.SessionId)
			return &pb.CollabEvent{
				EventId: uuid.New().String(),
				Event: &pb.CollabEvent_PeerLeft{
					PeerLeft: &pb.PeerLeft{
						ClientId:  clientId,
						PeerCount: 0,
					},
				},
			}, nil
		}
	}

	// Broadcast PeerLeft to remaining clients
	room.BroadcastToAll(&pb.CollabEvent{
		EventId:      uuid.New().String(),
		FromClientId: clientId,
		Event: &pb.CollabEvent_PeerLeft{
			PeerLeft: &pb.PeerLeft{
				ClientId:  clientId,
				Reason:    action.GetLeave().GetReason(),
				PeerCount: int32(remainingCount),
			},
		},
	})

	// Clean up empty rooms
	if remainingCount == 0 {
		s.removeRoom(room.SessionId)
	}

	return &pb.CollabEvent{
		EventId: uuid.New().String(),
		Event: &pb.CollabEvent_PeerLeft{
			PeerLeft: &pb.PeerLeft{
				ClientId:  clientId,
				PeerCount: int32(remainingCount),
			},
		},
	}, nil
}

// handlePresence broadcasts a PresenceUpdate to all peers except the sender.
// Returns nil event (broadcast-only, no direct response to the sender).
func (s *CollabService) handlePresence(ctx context.Context, action *pb.CollabAction) (*pb.CollabEvent, error) {
	clientId := action.GetClientId()

	room := s.findRoomForClient(clientId)
	if room == nil {
		return nil, fmt.Errorf("client %s not found in any room", clientId)
	}

	// Broadcast presence to everyone except sender
	room.BroadcastExcept(&pb.CollabEvent{
		EventId:      uuid.New().String(),
		FromClientId: clientId,
		Event: &pb.CollabEvent_Presence{
			Presence: action.GetPresence(),
		},
	}, clientId)

	return nil, nil
}

// handleBroadcast is a generic handler for relay-only messages (no server-side
// state, just fan-out). Converts the action oneof to the corresponding event
// oneof and broadcasts to all peers except the sender.
func (s *CollabService) handleBroadcast(ctx context.Context, action *pb.CollabAction) (*pb.CollabEvent, error) {
	clientId := action.GetClientId()

	room := s.findRoomForClient(clientId)
	if room == nil {
		return nil, fmt.Errorf("client %s not found in any room", clientId)
	}

	event := &pb.CollabEvent{
		EventId:      uuid.New().String(),
		FromClientId: clientId,
	}

	// Convert action oneof → event oneof
	switch a := action.Action.(type) {
	case *pb.CollabAction_SceneUpdate:
		event.Event = &pb.CollabEvent_SceneUpdate{SceneUpdate: a.SceneUpdate}
		if s.LogPayloads > 0 {
			for i, el := range a.SceneUpdate.GetElements() {
				data := el.GetData()
				if len(data) > s.LogPayloads {
					data = data[:s.LogPayloads]
				}
				slog.Debug("SceneUpdate element", "index", i, "id", el.GetId(), "data", data)
				if i >= 2 {
					break
				}
			}
		}
	case *pb.CollabAction_CursorUpdate:
		event.Event = &pb.CollabEvent_CursorUpdate{CursorUpdate: a.CursorUpdate}
	case *pb.CollabAction_TextUpdate:
		event.Event = &pb.CollabEvent_TextUpdate{TextUpdate: a.TextUpdate}
		if s.LogPayloads > 0 {
			text := a.TextUpdate.GetText()
			if len(text) > s.LogPayloads {
				text = text[:s.LogPayloads]
			}
			slog.Debug("TextUpdate", "text", text)
		}
	case *pb.CollabAction_SceneInitRequest:
		event.Event = &pb.CollabEvent_SceneInitRequest{SceneInitRequest: a.SceneInitRequest}
	case *pb.CollabAction_SceneInitResponse:
		event.Event = &pb.CollabEvent_SceneInitResponse{SceneInitResponse: a.SceneInitResponse}
		if s.LogPayloads > 0 {
			payload := a.SceneInitResponse.GetPayload()
			if len(payload) > s.LogPayloads {
				payload = payload[:s.LogPayloads]
			}
			slog.Debug("SceneInitResponse", "payload", payload)
		}
	case *pb.CollabAction_CredentialsChanged:
		// Update room encrypted state based on reason
		if a.CredentialsChanged.GetReason() == "password_removed" {
			room.mu.Lock()
			room.Encrypted = false
			room.mu.Unlock()
		}
		event.Event = &pb.CollabEvent_CredentialsChanged{CredentialsChanged: a.CredentialsChanged}
	case *pb.CollabAction_TitleChanged:
		// Update room title
		room.mu.Lock()
		room.Title = a.TitleChanged.GetTitle()
		room.mu.Unlock()
		event.Event = &pb.CollabEvent_TitleChanged{TitleChanged: a.TitleChanged}
	default:
		return nil, fmt.Errorf("unsupported broadcast action type")
	}

	targetCount := room.ClientCount() - 1
	slog.Debug("Broadcasting", "component", "relay", "type", fmt.Sprintf("%T", event.Event), "client", clientId, "peers", targetCount, "room", room.SessionId)
	room.BroadcastExcept(event, clientId)

	if s.OnMessageRelay != nil {
		s.OnMessageRelay(fmt.Sprintf("%T", action.Action))
	}

	return nil, nil
}
