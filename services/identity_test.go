package services

import (
	"context"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

func joinWith(t *testing.T, svc *CollabService, ctx context.Context, sessionId string) *pb.CollabEvent {
	t.Helper()
	event, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{Join: &pb.JoinRoom{SessionId: sessionId, Username: "u"}},
	})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	return event
}

func TestJoinCarriesPrincipalIdentity(t *testing.T) {
	svc := NewCollabService()
	anon := joinWith(t, svc, context.Background(), "sessI").GetRoomJoined().GetClientId()

	ctx := WithPrincipal(context.Background(), &Principal{Subject: "user-42", ClientID: "design-app"})
	joined := joinWith(t, svc, ctx, "sessI").GetRoomJoined()
	authed := joined.GetClientId()

	if got := joined.GetRoom().GetPeers()[anon].GetIdentity(); got != nil {
		t.Fatalf("anonymous peer has identity %v", got)
	}

	room := svc.GetRoomByID("sessI")
	evt := <-room.Clients[anon].SendCh
	id := evt.GetPeerJoined().GetPeer().GetIdentity()
	if id.GetSubject() != "user-42" || id.GetClientId() != "design-app" {
		t.Fatalf("PeerJoined identity = %v", id)
	}
	if id := room.ToProto().GetPeers()[authed].GetIdentity(); id.GetSubject() != "user-42" {
		t.Fatalf("room snapshot identity = %v", id)
	}
}

func TestJoinRoomRestrictedByPrincipal(t *testing.T) {
	p := &Principal{Subject: "user-42", Rooms: []string{"design:abc", "review:*"}}
	ctx := WithPrincipal(context.Background(), p)

	cases := []struct {
		session string
		allowed bool
	}{
		{"design:abc", true},
		{"review:pr-7", true},
		{"design:xyz", false},
		{"design:abcd", false},
		{"", false}, // a restricted token cannot have the relay pick a session
	}
	for _, c := range cases {
		svc := NewCollabService()
		evt := joinWith(t, svc, ctx, c.session)
		if c.allowed {
			if evt.GetRoomJoined() == nil {
				t.Errorf("%q: expected RoomJoined, got %v", c.session, evt)
			}
			continue
		}
		if evt.GetError().GetCode() != "ROOM_FORBIDDEN" {
			t.Errorf("%q: expected ROOM_FORBIDDEN, got %v", c.session, evt)
		}
		if rooms, _ := svc.RoomAndPeerCount(); rooms != 0 {
			t.Errorf("%q: refused join left %d room(s) behind", c.session, rooms)
		}
	}
}

func TestJoinRoomRestrictedRejectsHint(t *testing.T) {
	svc := NewCollabService()
	ctx := WithPrincipal(context.Background(), &Principal{Subject: "u", Rooms: []string{"design:abc"}})
	evt, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{Join: &pb.JoinRoom{ClientHint: "user:doc", Username: "u"}},
	})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if evt.GetError().GetCode() != "ROOM_FORBIDDEN" {
		t.Fatalf("expected ROOM_FORBIDDEN, got %v", evt)
	}
	if sid := svc.FindSessionByHint("user:doc"); sid != "" {
		t.Fatalf("refused join indexed hint to %q", sid)
	}
}
