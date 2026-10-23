package services

import (
	"context"
	"errors"
	"strings"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

// ErrPeerNotFound is returned by DeliverAppMessage when to_client_id names a
// client that is not in the room (it left, or it is in a different room).
var ErrPeerNotFound = errors.New("peer not found in room")

// ErrRoomNotFound is returned by DeliverAppMessage when the session has no room.
var ErrRoomNotFound = errors.New("room not found")

// Principal is what a connection authenticated as. The transport layer builds
// it from a validated token and attaches it with WithPrincipal; CollabService
// reads it on JoinRoom. A context without one is an anonymous connection.
type Principal struct {
	// Subject is the token's "sub". Copied to PeerInfo.identity.subject.
	Subject string
	// ClientID is the token's "client_id" (the app it was minted for), if any.
	// Copied to PeerInfo.identity.client_id.
	ClientID string
	// Rooms, when non-empty, limits which sessions this connection may join.
	// Each entry is an exact session id, or a prefix ending in "*". Empty means
	// any session.
	Rooms []string
}

type principalKey struct{}

// WithPrincipal returns a context carrying p. Pass the result to HandleAction
// so the join records p's identity and enforces p.Rooms.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns the Principal attached by WithPrincipal, or nil
// for an anonymous connection.
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// AllowsRoom reports whether p may join sessionId. A nil Principal or one with
// no Rooms allows every session; a restricted one never allows the empty
// session id, since the relay would pick the session for it.
func (p *Principal) AllowsRoom(sessionId string) bool {
	if p == nil || len(p.Rooms) == 0 {
		return true
	}
	if sessionId == "" {
		return false
	}
	for _, r := range p.Rooms {
		if prefix, ok := strings.CutSuffix(r, "*"); ok {
			if strings.HasPrefix(sessionId, prefix) {
				return true
			}
		} else if r == sessionId {
			return true
		}
	}
	return false
}

func (p *Principal) identity() *pb.PeerIdentity {
	if p == nil || p.Subject == "" {
		return nil
	}
	return &pb.PeerIdentity{Subject: p.Subject, ClientId: p.ClientID}
}
