package services

import (
	"sync"
	"time"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

// CollabRoom holds the state for a single collaboration session.
// Each room is identified by a unique SessionId and contains zero or more
// connected peers (CollabClient). The room tracks ownership for lifecycle
// management — when the owner disconnects, ownership transfers to a
// same-browser tab or the session ends.
//
// Room state fields (SessionId, OwnerClientId, Metadata, Encrypted, Title)
// overlap with pb.Room but are NOT embedded because pb.Room also contains
// Peers (derived dynamically from the Clients map) and CreatedAt (int64 vs
// time.Time). Embedding would leave a confusing always-nil Peers field.
// Use ToProto() to produce a pb.Room snapshot when needed.
//
// All exported methods are thread-safe (guarded by an internal RWMutex).
type CollabRoom struct {
	SessionId      string
	Clients        map[string]*CollabClient
	Created        time.Time
	OwnerClientId  string
	OwnerBrowserId string            // server-only: ownership transfer
	Metadata       map[string]string // app-defined key-value pairs
	Title          string
	Encrypted      bool
	mu             sync.RWMutex
}

// NewCollabRoom creates a new room with the given session ID.
func NewCollabRoom(sessionId string) *CollabRoom {
	return &CollabRoom{
		SessionId: sessionId,
		Clients:   make(map[string]*CollabClient),
		Created:   time.Now(),
	}
}

// CloseAllClients closes all client send channels and removes them from the room.
func (r *CollabRoom) CloseAllClients() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.Clients {
		close(c.SendCh)
		delete(r.Clients, id)
	}
}

// FindByBrowserId returns any client with the given browser ID,
// excluding the specified client. Returns nil if none found.
func (r *CollabRoom) FindByBrowserId(browserId string, excludeClientId string) *CollabClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.Clients {
		if c.ClientId == excludeClientId {
			continue
		}
		if c.BrowserId == browserId {
			return c
		}
	}
	return nil
}

// AddClient adds a client to the room.
func (r *CollabRoom) AddClient(client *CollabClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Clients[client.ClientId] = client
}

// RemoveClient removes a client by ID. Returns the removed client or nil.
func (r *CollabRoom) RemoveClient(clientId string) *CollabClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.Clients[clientId]
	if !ok {
		return nil
	}
	delete(r.Clients, clientId)
	return c
}

// GetPeerInfo returns a snapshot of PeerInfo for all connected clients.
// Each entry is the embedded PeerInfo from the CollabClient.
// The returned slice is safe to use outside the room's lock.
func (r *CollabRoom) GetPeerInfo() []*pb.PeerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	peers := make([]*pb.PeerInfo, 0, len(r.Clients))
	for _, c := range r.Clients {
		peers = append(peers, c.PeerInfo)
	}
	return peers
}

// ToProto returns a protobuf Room snapshot of this room's current state.
// The returned Room is safe to use outside the room's lock.
func (r *CollabRoom) ToProto() *pb.Room {
	return &pb.Room{
		SessionId:     r.SessionId,
		Peers:         r.GetPeerInfo(),
		OwnerClientId: r.OwnerClientId,
		CreatedAt:     r.Created.Unix(),
		Metadata:      r.Metadata,
		Encrypted:     r.Encrypted,
		Title:         r.Title,
	}
}

// IsEmpty returns true if the room has no clients.
func (r *CollabRoom) IsEmpty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Clients) == 0
}

// ClientCount returns the number of connected clients.
func (r *CollabRoom) ClientCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Clients)
}

// BroadcastToAll sends an event to all clients (non-blocking).
func (r *CollabRoom) BroadcastToAll(event *pb.CollabEvent) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.Clients {
		select {
		case c.SendCh <- event:
		default:
			// drop if channel full — non-blocking
		}
	}
}

// BroadcastExcept sends an event to all clients except exceptClientId.
// Non-blocking: if a client's send channel is full, the event is silently dropped.
func (r *CollabRoom) BroadcastExcept(event *pb.CollabEvent, exceptClientId string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.Clients {
		if c.ClientId == exceptClientId {
			continue
		}
		select {
		case c.SendCh <- event:
		default:
		}
	}
}
