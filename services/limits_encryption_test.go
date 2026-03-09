package services

import (
	"context"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

func TestJoinRoom_RoomFull(t *testing.T) {
	svc := NewCollabService()
	svc.MaxPeersPerRoom = 3
	ctx := context.Background()

	// Join 3 clients (fill the room)
	for i := 0; i < 3; i++ {
		action := &pb.CollabAction{
			Action: &pb.CollabAction_Join{
				Join: &pb.JoinRoom{
					SessionId: "sess-full",
					Username:  "User",
					Metadata:  map[string]string{"tool": "whiteboard"},
				},
			},
		}
		event, err := svc.HandleAction(ctx, action)
		if err != nil {
			t.Fatalf("join %d: unexpected error: %v", i, err)
		}
		if event.GetRoomJoined() == nil {
			t.Fatalf("join %d: expected RoomJoined event", i)
		}
	}

	// 4th client should be rejected with ROOM_FULL ErrorEvent (not an error)
	action := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: "sess-full",
				Username:  "Overflow",
				Metadata:  map[string]string{"tool": "whiteboard"},
			},
		},
	}
	event, err := svc.HandleAction(ctx, action)
	if err != nil {
		t.Fatalf("expected no stream error, got: %v", err)
	}
	if event == nil {
		t.Fatal("expected ErrorEvent, got nil")
	}
	errEvent := event.GetError()
	if errEvent == nil {
		t.Fatalf("expected ErrorEvent, got %T", event.Event)
	}
	if errEvent.Code != "ROOM_FULL" {
		t.Fatalf("expected code ROOM_FULL, got %s", errEvent.Code)
	}
}

func TestRoomJoined_IncludesMaxPeers(t *testing.T) {
	svc := NewCollabService()
	svc.MaxPeersPerRoom = 25
	ctx := context.Background()

	event, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: "sess-info",
				Username:  "Alice",
				Metadata:  map[string]string{"tool": "whiteboard"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rj := event.GetRoomJoined()
	if rj == nil {
		t.Fatal("expected RoomJoined")
	}
	if rj.MaxPeers != 25 {
		t.Fatalf("expected max_peers=25, got %d", rj.MaxPeers)
	}
	if rj.ProtocolVersion != svc.ProtocolVersion {
		t.Fatalf("expected protocol_version=%d, got %d", svc.ProtocolVersion, rj.ProtocolVersion)
	}
}

func TestJoinRoom_EncryptedRoom(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Owner creates encrypted room
	event, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:       "sess-enc",
				Username:        "Owner",
				Metadata:        map[string]string{"tool": "whiteboard"},
				IsOwner:         true,
				Encrypted:       true,
				ProtocolVersion: 2,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rj := event.GetRoomJoined()
	if rj == nil {
		t.Fatal("expected RoomJoined")
	}
	if !rj.GetRoom().GetEncrypted() {
		t.Fatal("expected encrypted=true in RoomJoined")
	}

	// v2 client joins — should succeed
	event2, err2 := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:       "sess-enc",
				Username:        "ModernClient",
				Metadata:        map[string]string{"tool": "whiteboard"},
				ProtocolVersion: 2,
			},
		},
	})
	if err2 != nil {
		t.Fatal(err2)
	}
	if event2.GetRoomJoined() == nil {
		t.Fatal("expected v2 client to join successfully")
	}
}

func TestJoinRoom_OldProtocolRejected(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Owner creates encrypted room
	svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:       "sess-enc2",
				Username:        "Owner",
				Metadata:        map[string]string{"tool": "whiteboard"},
				IsOwner:         true,
				Encrypted:       true,
				ProtocolVersion: 2,
			},
		},
	})

	// Old client (no protocol_version = 0) tries to join encrypted room
	event, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: "sess-enc2",
				Username:  "OldClient",
				Metadata:  map[string]string{"tool": "whiteboard"},
				// ProtocolVersion intentionally omitted (defaults to 0)
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	errEvent := event.GetError()
	if errEvent == nil {
		t.Fatal("expected PROTOCOL_VERSION_TOO_OLD ErrorEvent")
	}
	if errEvent.Code != "PROTOCOL_VERSION_TOO_OLD" {
		t.Fatalf("expected code PROTOCOL_VERSION_TOO_OLD, got %s", errEvent.Code)
	}
}
