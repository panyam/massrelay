package services

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

// drainCh reads all pending messages from a channel (non-blocking).
func drainCh(ch chan *pb.CollabEvent) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func joinAsOwner(svc *CollabService, ctx context.Context, sessionId, username, browserId string) (string, error) {
	action := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: sessionId,
				Username:  username,
				IsOwner:   true,
				BrowserId: browserId,
			},
		},
	}
	event, err := svc.HandleAction(ctx, action)
	if err != nil {
		return "", err
	}
	return event.GetRoomJoined().GetClientId(), nil
}

func joinAsFollower(svc *CollabService, ctx context.Context, sessionId, username, browserId string) (string, error) {
	action := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: sessionId,
				Username:  username,
				IsOwner:   false,
				BrowserId: browserId,
			},
		},
	}
	event, err := svc.HandleAction(ctx, action)
	if err != nil {
		return "", err
	}
	return event.GetRoomJoined().GetClientId(), nil
}

func leaveRoom(svc *CollabService, ctx context.Context, clientId string) error {
	_, err := svc.HandleAction(ctx, &pb.CollabAction{
		ClientId: clientId,
		Action: &pb.CollabAction_Leave{
			Leave: &pb.LeaveRoom{Reason: "disconnect"},
		},
	})
	return err
}

// setupTwoClients creates a room with two clients and drains PeerJoined.
func setupTwoClients(t *testing.T, svc *CollabService, sessionId string, metadata map[string]string) (string, string) {
	t.Helper()
	ctx := context.Background()
	join := func(name string) string {
		event, err := svc.HandleAction(ctx, &pb.CollabAction{
			Action: &pb.CollabAction_Join{
				Join: &pb.JoinRoom{SessionId: sessionId, Username: name, Metadata: metadata},
			},
		})
		if err != nil {
			t.Fatalf("join error: %v", err)
		}
		return event.GetRoomJoined().GetClientId()
	}
	c1 := join("Alice")
	c2 := join("Bob")
	drainCh(svc.GetRoomByID(sessionId).Clients[c1].SendCh)
	return c1, c2
}

// captureLog redirects slog output to a buffer for the duration of fn.
func captureLog(fn func()) string {
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))
	defer slog.SetDefault(orig)
	fn()
	return buf.String()
}
