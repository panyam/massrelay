package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
	"github.com/panyam/massrelay/services"
	"github.com/panyam/massrelay/web/middleware"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// StreamConfig controls per-client stream limits.
type StreamConfig struct {
	MaxMessageRate float64 // messages/sec per client (0 = unlimited, default 30)
	MaxMessageSize int     // max message payload in bytes (0 = unlimited, default 1MB)

	// WriteTimeout bounds each WebSocket write. A client that stops reading
	// fills its TCP window, its writes time out, and servicekit closes the
	// socket, which unblocks an eviction stuck behind that client's queue.
	// 0 means no deadline, leaving it to the 5 min pong timeout.
	WriteTimeout time.Duration

	// Auth checks the token in a ReAuthAction. Nil refuses every ReAuthAction,
	// and RoomJoined then leaves token_expires_at unset, so clients don't try.
	Auth *middleware.RelayAuthenticator
}

// DefaultStreamConfig returns sensible defaults.
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		MaxMessageRate: 30,
		MaxMessageSize: 1 << 20, // 1MB
		WriteTimeout:   10 * time.Second,
	}
}

// CollabBidiStream implements grpcws.BidiStream for the in-process CollabService.
// It bridges the servicekit WebSocket handler to our CollabService.HandleAction.
type CollabBidiStream struct {
	ctx            context.Context
	cancel         context.CancelFunc
	service        *services.CollabService
	sendCh         chan *pb.CollabEvent // events from service → WebSocket client
	ended          chan struct{}        // closed once the relay ends this stream (eviction, token expiry)
	endOnce        sync.Once
	auth           *middleware.RelayAuthenticator
	claims         *middleware.RelayClaims // the current token's; nil for an anonymous connection
	expiry         *time.Timer             // ends the stream at the token's exp; nil without one
	sessionId      string                  // set after first JoinRoom action
	clientId       string                  // set after first JoinRoom action
	mu             sync.Mutex
	messageLimiter *rate.Limiter // per-client message rate limiter
}

// NewCollabBidiStream creates a new bidi stream for the collab service. ctx is
// the upgrade request's context; if the auth middleware validated a token on
// it, the stream's joins carry that identity (see services.Principal), and
// the stream ends with SessionEnded{token_expired} when the token's exp
// passes, unless a ReAuthAction has moved it to a fresh token's exp.
func NewCollabBidiStream(ctx context.Context, svc *services.CollabService, cfg StreamConfig) *CollabBidiStream {
	claims := middleware.GetRelayClaimsFromContext(ctx)
	if claims != nil {
		ctx = services.WithPrincipal(ctx, &services.Principal{
			Subject:  claims.Subject,
			ClientID: claims.ClientID,
			Rooms:    claims.Rooms,
		})
	}
	ctx, cancel := context.WithCancel(ctx)
	var msgLimiter *rate.Limiter
	if cfg.MaxMessageRate > 0 {
		msgLimiter = rate.NewLimiter(rate.Limit(cfg.MaxMessageRate), int(cfg.MaxMessageRate))
	}
	s := &CollabBidiStream{
		ctx:            ctx,
		cancel:         cancel,
		service:        svc,
		sendCh:         make(chan *pb.CollabEvent, 64),
		ended:          make(chan struct{}),
		messageLimiter: msgLimiter,
		auth:           cfg.Auth,
		claims:         claims,
	}
	if claims != nil && !claims.ExpiresAt.IsZero() {
		s.expiry = time.AfterFunc(time.Until(claims.ExpiresAt), s.expire)
	}
	// Watch for context cancellation (WebSocket close) and clean up the client.
	// BidiStreamConn.OnClose() cancels the context but does NOT call CloseSend(),
	// so ungraceful disconnects (browser refresh, network drop) would leave zombie
	// clients in the room without this goroutine.
	go s.watchClose()
	return s
}

// Send processes a CollabAction from the client (WS → service).
func (s *CollabBidiStream) Send(action *pb.CollabAction) error {
	if ra := action.GetReAuth(); ra != nil {
		return s.reply(&pb.CollabEvent{
			EventId: uuid.New().String(),
			Event:   &pb.CollabEvent_ReAuthResult{ReAuthResult: s.reAuth(ra.GetToken())},
		})
	}

	// Rate limit non-control messages (join/leave always allowed; ReAuth
	// returned above, since a dropped refresh would end the stream at exp)
	if s.messageLimiter != nil {
		switch action.Action.(type) {
		case *pb.CollabAction_Join, *pb.CollabAction_Leave:
			// Always allow control messages
		default:
			if !s.messageLimiter.Allow() {
				slog.Warn("Rate limited message", "component", "stream", "client", action.GetClientId())
				return s.reply(&pb.CollabEvent{
					EventId: uuid.New().String(),
					Event: &pb.CollabEvent_Error{Error: &pb.ErrorEvent{
						Code:    "RATE_LIMITED",
						Message: fmt.Sprintf("Dropped action %q: over %g messages/sec", action.GetActionId(), float64(s.messageLimiter.Limit())),
					}},
				})
			}
		}
	}

	// The sender is this connection's own client, whatever the action claims;
	// anything but a join before joining has no sender to be.
	s.mu.Lock()
	joinedAs := s.clientId
	s.mu.Unlock()
	if action.GetJoin() == nil {
		if joinedAs == "" {
			return fmt.Errorf("%T sent before joining a room", action.Action)
		}
		action.ClientId = joinedAs
	} else if joinedAs != "" {
		// One room per connection: a second client here would never be
		// cleaned up, since the stream only remembers one.
		return s.reply(&pb.CollabEvent{
			EventId: uuid.New().String(),
			Event: &pb.CollabEvent_Error{Error: &pb.ErrorEvent{
				Code:    "ALREADY_JOINED",
				Message: "This connection is already in a room; open another connection to join a second one",
			}},
		})
	}

	resp, err := s.service.HandleAction(s.ctx, action)
	if err != nil {
		return err
	}

	// Store session/client info after join and start forwarding broadcast events
	if action.GetJoin() != nil && resp != nil {
		if rj := resp.GetRoomJoined(); rj != nil {
			sessionId := rj.GetRoom().GetSessionId()
			s.mu.Lock()
			s.sessionId = sessionId
			s.clientId = rj.GetClientId()
			// Only a protocol 3 client knows the field. Older ones reject
			// unknown JSON keys, so they would never see their join.
			if s.auth != nil && s.claims != nil && !s.claims.ExpiresAt.IsZero() && action.GetJoin().GetProtocolVersion() >= 3 {
				rj.TokenExpiresAt = timestamppb.New(s.claims.ExpiresAt)
			}
			s.mu.Unlock()
			slog.Info("Client joined, starting bridge", "component", "stream", "client", rj.GetClientId(), "session", sessionId)

			// Bridge: forward events from the service client's SendCh to the
			// stream's sendCh, until the room marks the client slow.
			if client := s.service.GetClient(sessionId, rj.GetClientId()); client != nil {
				go s.bridge(client)
			} else {
				slog.Warn("No client channel found", "component", "stream", "session", sessionId, "client", rj.GetClientId())
			}
		}
	}

	// Send response event back to the client (if any)
	if resp != nil {
		return s.reply(resp)
	}
	return nil
}

// expire ends the stream when its token's exp passes.
func (s *CollabBidiStream) expire() {
	s.mu.Lock()
	sub := s.claims.Subject
	s.mu.Unlock()
	slog.Info("Token expired, ending stream", "component", "stream", "sub", sub)
	s.endSession("token_expired")
}

// reAuth moves this stream onto the token raw and reports how that went. The
// token must pass the checks the upgrade's token did, have the same sub and
// client_id, and allow the room the stream is in. On success the stream ends
// at the new token's exp instead of the old one's. A refusal changes nothing,
// so the stream still ends at the old exp.
func (s *CollabBidiStream) reAuth(raw string) *pb.ReAuthResult {
	refuse := func(why string) *pb.ReAuthResult {
		slog.Info("ReAuth refused", "component", "stream", "reason", why)
		return &pb.ReAuthResult{Error: why}
	}
	s.mu.Lock()
	sessionId, old := s.sessionId, s.claims
	s.mu.Unlock()
	if sessionId == "" {
		return refuse("join a room before re-authenticating")
	}
	if s.auth == nil || old == nil {
		return refuse("this connection has no token to refresh")
	}
	claims, err := s.auth.ValidateToken(s.ctx, raw)
	if err != nil {
		return refuse(err.Error())
	}
	if claims.Subject != old.Subject || claims.ClientID != old.ClientID {
		return refuse("the token is for a different sub or client_id")
	}
	if !(&services.Principal{Rooms: claims.Rooms}).AllowsRoom(sessionId) {
		return refuse("the token does not allow this room")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiry != nil && !s.expiry.Stop() {
		return refuse("the old token has already expired")
	}
	s.claims = claims
	s.expiry = nil
	result := &pb.ReAuthResult{Success: true}
	if !claims.ExpiresAt.IsZero() {
		s.expiry = time.AfterFunc(time.Until(claims.ExpiresAt), s.expire)
		result.ExpiresAt = timestamppb.New(claims.ExpiresAt)
	}
	slog.Info("ReAuth accepted", "component", "stream", "sub", claims.Subject, "exp", claims.ExpiresAt)
	return result
}

// bridge forwards client's room events to this connection in order. When the
// room marks the client slow, it stops, takes the client out of the room and
// ends its session with reason "slow_consumer": the client has missed a
// message, and everything after it would arrive out of context.
func (s *CollabBidiStream) bridge(client *services.CollabClient) {
	slow := client.Slow()
	for {
		select {
		case event, ok := <-client.SendCh:
			if !ok {
				slog.Debug("Bridge channel closed", "component", "stream", "client", client.ClientId)
				return
			}
			slog.Debug("Forwarding event", "component", "stream", "type", fmt.Sprintf("%T", event.Event), "client", client.ClientId)
			select {
			case s.sendCh <- event:
			case <-slow:
				s.evictSlow(client, event)
				return
			case <-s.ctx.Done():
				return
			}
		case <-slow:
			s.evictSlow(client, nil)
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// evictSlow passes on what the client already had queued, starting with the
// event the bridge was holding (if any), as far as this connection's queue
// has room, then ends its session. Those events came before the one the room
// dropped, so they are still an unbroken prefix; without this, a bridge that
// hadn't run yet would evict with nothing sent. It stops at the first event
// that doesn't fit, so nothing after a skipped one goes out.
func (s *CollabBidiStream) evictSlow(client *services.CollabClient, held *pb.CollabEvent) {
	slog.Warn("Evicting slow consumer", "component", "stream", "client", client.ClientId)
forward:
	for event := held; ; event = nil {
		if event == nil {
			select {
			case e, ok := <-client.SendCh:
				if !ok {
					break forward
				}
				event = e
			default:
				break forward
			}
		}
		select {
		case s.sendCh <- event:
		default:
			break forward
		}
	}
	s.endSession("slow_consumer")
}

// endSession takes this connection's client out of its room, queues
// SessionEnded{reason} after whatever is already queued, and ends the stream
// so Recv drains and returns io.EOF. Only the first call does anything, so an
// eviction and an expiry racing send one SessionEnded.
func (s *CollabBidiStream) endSession(reason string) {
	s.endOnce.Do(func() {
		s.leaveAndCleanup(reason)
		s.reply(&pb.CollabEvent{
			EventId: uuid.New().String(),
			Event:   &pb.CollabEvent_SessionEnded{SessionEnded: &pb.SessionEnded{Reason: reason}},
		})
		close(s.ended)
	})
}

// reply queues an event for this connection's client.
func (s *CollabBidiStream) reply(event *pb.CollabEvent) error {
	select {
	case s.sendCh <- event:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// Recv reads the next event to send to the WebSocket client (service → WS).
// Once the relay has ended the stream it drains what is queued, SessionEnded
// last, then returns io.EOF so servicekit closes the socket.
func (s *CollabBidiStream) Recv() (*pb.CollabEvent, error) {
	select {
	case event, ok := <-s.sendCh:
		if !ok {
			return nil, io.EOF
		}
		return event, nil
	case <-s.ended:
		select {
		case event, ok := <-s.sendCh:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, io.EOF
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

// leaveAndCleanup sends a LeaveRoom action for this client if still joined.
// Safe to call multiple times — clears sessionId/clientId on first call.
func (s *CollabBidiStream) leaveAndCleanup(reason string) {
	s.mu.Lock()
	sessionId := s.sessionId
	clientId := s.clientId
	s.sessionId = ""
	s.clientId = ""
	s.mu.Unlock()

	if sessionId != "" && clientId != "" {
		slog.Info("Cleanup: leaving session", "component", "stream", "session", sessionId, "client", clientId, "reason", reason)
		// Use Background context — the original ctx may already be cancelled
		s.service.HandleAction(context.Background(), &pb.CollabAction{
			ClientId: clientId,
			Action: &pb.CollabAction_Leave{
				Leave: &pb.LeaveRoom{Reason: reason},
			},
		})
	}
}

// watchClose waits for context cancellation (WebSocket drop) and cleans up.
func (s *CollabBidiStream) watchClose() {
	<-s.ctx.Done()
	s.mu.Lock()
	if s.expiry != nil {
		s.expiry.Stop()
	}
	s.mu.Unlock()
	s.leaveAndCleanup("connection closed")
}

// CloseSend signals that the client is done sending (half-close).
func (s *CollabBidiStream) CloseSend() error {
	s.leaveAndCleanup("client closed")
	close(s.sendCh)
	return nil
}

// ─── grpc.ClientStream interface (required by servicekit) ───────
//
// servicekit drives the stream through Send/Recv/CloseSend only. These exist
// to satisfy grpc.ClientStream and do nothing: no headers or trailers, and
// SendMsg/RecvMsg are never called.

func (s *CollabBidiStream) Header() (metadata.MD, error) { return nil, nil }
func (s *CollabBidiStream) Trailer() metadata.MD         { return nil }
func (s *CollabBidiStream) CloseSend2() error            { return s.CloseSend() }
func (s *CollabBidiStream) Context() context.Context     { return s.ctx }
func (s *CollabBidiStream) SendMsg(m any) error          { return nil }
func (s *CollabBidiStream) RecvMsg(m any) error          { return nil }

// Verify CollabBidiStream satisfies the grpcws.BidiStream interface.
var _ grpc.ClientStream = (*CollabBidiStream)(nil)
