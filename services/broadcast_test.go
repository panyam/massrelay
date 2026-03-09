package services

import (
	"context"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

func TestBroadcastSceneUpdate(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Two clients join
	joinAction := func(name string) string {
		action := &pb.CollabAction{
			Action: &pb.CollabAction_Join{
				Join: &pb.JoinRoom{SessionId: "sess1", Username: name, Metadata: map[string]string{"tool": "whiteboard"}},
			},
		}
		event, _ := svc.HandleAction(ctx, action)
		return event.GetRoomJoined().GetClientId()
	}
	clientId1 := joinAction("Alice")
	clientId2 := joinAction("Bob")

	// Drain PeerJoined from client1
	<-svc.GetOrCreateRoom("sess1").Clients[clientId1].SendCh

	// Client2 sends SceneUpdate
	sceneAction := &pb.CollabAction{
		ClientId: clientId2,
		Action: &pb.CollabAction_SceneUpdate{
			SceneUpdate: &pb.SceneUpdate{
				Elements: []*pb.ElementUpdate{
					{Id: "el-1", Version: 1, VersionNonce: 100, Data: `{"type":"rect"}`, Deleted: false},
				},
			},
		},
	}
	result, err := svc.HandleAction(ctx, sceneAction)
	if err != nil {
		t.Fatalf("broadcast error: %v", err)
	}
	// handleBroadcast returns nil event (no direct response)
	if result != nil {
		t.Fatal("expected nil response for broadcast action")
	}

	// Client1 should receive the SceneUpdate event
	room := svc.GetOrCreateRoom("sess1")
	select {
	case evt := <-room.Clients[clientId1].SendCh:
		su := evt.GetSceneUpdate()
		if su == nil {
			t.Fatal("expected SceneUpdate event")
		}
		if len(su.Elements) != 1 {
			t.Fatalf("expected 1 element, got %d", len(su.Elements))
		}
		if su.Elements[0].Id != "el-1" {
			t.Fatalf("expected element id el-1, got %s", su.Elements[0].Id)
		}
		if evt.FromClientId != clientId2 {
			t.Fatalf("expected fromClientId %s, got %s", clientId2, evt.FromClientId)
		}
	default:
		t.Fatal("client1 should have received SceneUpdate broadcast")
	}

	// Client2 should NOT receive its own broadcast
	select {
	case <-room.Clients[clientId2].SendCh:
		t.Fatal("client2 should NOT receive its own broadcast")
	default:
		// OK
	}
}

func TestBroadcastTextUpdate(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	joinAction := func(name string) string {
		action := &pb.CollabAction{
			Action: &pb.CollabAction_Join{
				Join: &pb.JoinRoom{SessionId: "sess1", Username: name, Metadata: map[string]string{"tool": "text-editor"}},
			},
		}
		event, _ := svc.HandleAction(ctx, action)
		return event.GetRoomJoined().GetClientId()
	}
	clientId1 := joinAction("Alice")
	clientId2 := joinAction("Bob")

	<-svc.GetOrCreateRoom("sess1").Clients[clientId1].SendCh

	textAction := &pb.CollabAction{
		ClientId: clientId2,
		Action: &pb.CollabAction_TextUpdate{
			TextUpdate: &pb.TextUpdate{Text: "flowchart TD", Version: 1},
		},
	}
	_, err := svc.HandleAction(ctx, textAction)
	if err != nil {
		t.Fatalf("broadcast error: %v", err)
	}

	room := svc.GetOrCreateRoom("sess1")
	select {
	case evt := <-room.Clients[clientId1].SendCh:
		tu := evt.GetTextUpdate()
		if tu == nil {
			t.Fatal("expected TextUpdate event")
		}
		if tu.Text != "flowchart TD" {
			t.Fatalf("expected text 'flowchart TD', got %s", tu.Text)
		}
	default:
		t.Fatal("client1 should have received TextUpdate broadcast")
	}
}

func TestBroadcastSceneInitRequest(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	joinAction := func(name string) string {
		action := &pb.CollabAction{
			Action: &pb.CollabAction_Join{
				Join: &pb.JoinRoom{SessionId: "sess1", Username: name, Metadata: map[string]string{"tool": "whiteboard"}},
			},
		}
		event, _ := svc.HandleAction(ctx, action)
		return event.GetRoomJoined().GetClientId()
	}
	clientId1 := joinAction("Alice")
	clientId2 := joinAction("Bob")

	<-svc.GetOrCreateRoom("sess1").Clients[clientId1].SendCh

	initReqAction := &pb.CollabAction{
		ClientId: clientId2,
		Action: &pb.CollabAction_SceneInitRequest{
			SceneInitRequest: &pb.SceneInitRequest{},
		},
	}
	_, err := svc.HandleAction(ctx, initReqAction)
	if err != nil {
		t.Fatalf("broadcast error: %v", err)
	}

	room := svc.GetOrCreateRoom("sess1")
	select {
	case evt := <-room.Clients[clientId1].SendCh:
		if evt.GetSceneInitRequest() == nil {
			t.Fatal("expected SceneInitRequest event")
		}
	default:
		t.Fatal("client1 should have received SceneInitRequest broadcast")
	}
}

func TestBroadcast_NonexistentClient(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Join so room exists
	svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{SessionId: "sess1", Username: "Alice", Metadata: map[string]string{"tool": "whiteboard"}},
		},
	})

	// Try to broadcast from unknown client
	action := &pb.CollabAction{
		ClientId: "ghost",
		Action: &pb.CollabAction_SceneUpdate{
			SceneUpdate: &pb.SceneUpdate{},
		},
	}
	_, err := svc.HandleAction(ctx, action)
	if err == nil {
		t.Fatal("expected error for nonexistent client broadcast")
	}
}

func TestCredentialsChanged_Broadcast(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Two clients join
	event1, _ := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:       "sess-cred",
				Username:        "Owner",
				Metadata:        map[string]string{"tool": "whiteboard"},
				IsOwner:         true,
				Encrypted:       true,
				ProtocolVersion: 2,
			},
		},
	})
	ownerClientId := event1.GetRoomJoined().ClientId

	event2, _ := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:       "sess-cred",
				Username:        "Peer",
				Metadata:        map[string]string{"tool": "whiteboard"},
				ProtocolVersion: 2,
			},
		},
	})
	peerClientId := event2.GetRoomJoined().ClientId

	// Drain PeerJoined from owner's channel
	ownerCh := svc.GetClientSendCh("sess-cred", ownerClientId)
	drainCh(ownerCh)

	// Owner sends CredentialsChanged
	svc.HandleAction(ctx, &pb.CollabAction{
		ClientId: ownerClientId,
		Action: &pb.CollabAction_CredentialsChanged{
			CredentialsChanged: &pb.CredentialsChanged{
				Reason: "password_changed",
			},
		},
	})

	// Peer should receive CredentialsChanged event
	peerCh := svc.GetClientSendCh("sess-cred", peerClientId)
	if peerCh == nil {
		t.Fatal("expected non-nil peer channel")
	}
	select {
	case event := <-peerCh:
		cc := event.GetCredentialsChanged()
		if cc == nil {
			t.Fatalf("expected CredentialsChanged event, got %T", event.Event)
		}
		if cc.Reason != "password_changed" {
			t.Fatalf("expected reason=password_changed, got %s", cc.Reason)
		}
	default:
		t.Fatal("expected CredentialsChanged event on peer channel")
	}
}
