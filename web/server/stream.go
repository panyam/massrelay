package server

import (
	"context"
	"io"
	"log"
	"sync"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
	"github.com/panyam/massrelay/services"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// StreamConfig controls per-client stream limits.
type StreamConfig struct {
	MaxMessageRate float64 // messages/sec per client (0 = unlimited, default 30)
	MaxMessageSize int     // max message payload in bytes (0 = unlimited, default 1MB)
}

// DefaultStreamConfig returns sensible defaults.
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		MaxMessageRate: 30,
		MaxMessageSize: 1 << 20, // 1MB
	}
}

// CollabBidiStream implements grpcws.BidiStream for the in-process CollabService.
// It bridges the servicekit WebSocket handler to our CollabService.HandleAction.
type CollabBidiStream struct {
	ctx            context.Context
	cancel         context.CancelFunc
	service        *services.CollabService
	sendCh         chan *pb.CollabEvent // events from service → WebSocket client
	sessionId      string              // set after first JoinRoom action
	clientId       string              // set after first JoinRoom action
	mu             sync.Mutex
	messageLimiter *rate.Limiter // per-client message rate limiter
}

// NewCollabBidiStream creates a new bidi stream for the collab service.
func NewCollabBidiStream(ctx context.Context, svc *services.CollabService, cfg StreamConfig) *CollabBidiStream {
	ctx, cancel := context.WithCancel(ctx)
	var msgLimiter *rate.Limiter
	if cfg.MaxMessageRate > 0 {
		msgLimiter = rate.NewLimiter(rate.Limit(cfg.MaxMessageRate), int(cfg.MaxMessageRate))
	}
	return &CollabBidiStream{
		ctx:            ctx,
		cancel:         cancel,
		service:        svc,
		sendCh:         make(chan *pb.CollabEvent, 64),
		messageLimiter: msgLimiter,
	}
}

// Send processes a CollabAction from the client (WS → service).
func (s *CollabBidiStream) Send(action *pb.CollabAction) error {
	// Rate limit non-control messages (join/leave always allowed)
	if s.messageLimiter != nil {
		switch action.Action.(type) {
		case *pb.CollabAction_Join, *pb.CollabAction_Leave:
			// Always allow control messages
		default:
			if !s.messageLimiter.Allow() {
				log.Printf("[STREAM] Rate limited message from client %s", action.GetClientId())
				return nil // silently drop
			}
		}
	}

	resp, err := s.service.HandleAction(s.ctx, action)
	if err != nil {
		return err
	}

	// Store session/client info after join and start forwarding broadcast events
	if action.GetJoin() != nil && resp != nil {
		if rj := resp.GetRoomJoined(); rj != nil {
			s.mu.Lock()
			s.sessionId = rj.GetSessionId()
			s.clientId = rj.GetClientId()
			s.mu.Unlock()
			log.Printf("[STREAM] Client %s joined session %s, starting bridge goroutine", rj.GetClientId(), rj.GetSessionId())

			// Bridge: forward events from the service client's SendCh to the stream's sendCh
			if clientCh := s.service.GetClientSendCh(rj.GetSessionId(), rj.GetClientId()); clientCh != nil {
				go func() {
					for {
						select {
						case event, ok := <-clientCh:
							if !ok {
								log.Printf("[STREAM] Bridge channel closed for client %s", rj.GetClientId())
								return
							}
							log.Printf("[STREAM] Forwarding event %T to client %s", event.Event, rj.GetClientId())
							select {
							case s.sendCh <- event:
							case <-s.ctx.Done():
								return
							}
						case <-s.ctx.Done():
							return
						}
					}
				}()
			} else {
				log.Printf("[STREAM] WARNING: No client channel found for %s/%s", rj.GetSessionId(), rj.GetClientId())
			}
		}
	}

	// Send response event back to the client (if any)
	if resp != nil {
		select {
		case s.sendCh <- resp:
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	return nil
}

// Recv reads the next event to send to the WebSocket client (service → WS).
func (s *CollabBidiStream) Recv() (*pb.CollabEvent, error) {
	select {
	case event, ok := <-s.sendCh:
		if !ok {
			return nil, io.EOF
		}
		return event, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

// CloseSend signals that the client is done sending (half-close).
func (s *CollabBidiStream) CloseSend() error {
	// Trigger leave on half-close
	s.mu.Lock()
	sessionId := s.sessionId
	clientId := s.clientId
	s.mu.Unlock()

	if sessionId != "" && clientId != "" {
		s.service.HandleAction(s.ctx, &pb.CollabAction{
			ClientId: clientId,
			Action: &pb.CollabAction_Leave{
				Leave: &pb.LeaveRoom{Reason: "client closed"},
			},
		})
	}
	close(s.sendCh)
	return nil
}

// ─── grpc.ClientStream interface (required by servicekit) ───────

func (s *CollabBidiStream) Header() (metadata.MD, error) { return nil, nil }
func (s *CollabBidiStream) Trailer() metadata.MD          { return nil }
func (s *CollabBidiStream) CloseSend2() error              { return s.CloseSend() }
func (s *CollabBidiStream) Context() context.Context       { return s.ctx }
func (s *CollabBidiStream) SendMsg(m any) error { return nil }
func (s *CollabBidiStream) RecvMsg(m any) error { return nil }

// Verify CollabBidiStream satisfies the grpcws.BidiStream interface.
var _ grpc.ClientStream = (*CollabBidiStream)(nil)
