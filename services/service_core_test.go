package services

import (
	"context"
	"sync"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

func TestNewCollabService(t *testing.T) {
	svc := NewCollabService()
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.rooms == nil {
		t.Fatal("expected non-nil rooms map")
	}
	if len(svc.rooms) != 0 {
		t.Fatal("expected empty rooms map")
	}
}

func TestGetOrCreateRoom(t *testing.T) {
	svc := NewCollabService()
	room := svc.GetOrCreateRoom("sess1")
	if room == nil {
		t.Fatal("expected non-nil room")
	}
	if room.SessionId != "sess1" {
		t.Fatalf("expected session ID sess1, got %s", room.SessionId)
	}

	// Second call returns same room
	room2 := svc.GetOrCreateRoom("sess1")
	if room2 != room {
		t.Fatal("expected same room instance on second call")
	}
}

func TestGetOrCreateRoom_Concurrent(t *testing.T) {
	svc := NewCollabService()
	const n = 100
	rooms := make([]*CollabRoom, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			rooms[idx] = svc.GetOrCreateRoom("sess1")
		}(i)
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		if rooms[i] != rooms[0] {
			t.Fatalf("goroutine %d got a different room instance", i)
		}
	}
}

func TestJoinRoom_FirstClient(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	action := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:  "sess1",
				Username:   "Alice",
				Metadata:   map[string]string{"tool": "whiteboard"},
				ClientType: "browser",
			},
		},
	}
	event, err := svc.HandleAction(ctx, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected non-nil event")
	}

	rj := event.GetRoomJoined()
	if rj == nil {
		t.Fatal("expected RoomJoined event")
	}
	if rj.ClientId == "" {
		t.Fatal("expected non-empty client ID")
	}
	if rj.GetRoom().GetSessionId() != "sess1" {
		t.Fatalf("expected session sess1, got %s", rj.GetRoom().GetSessionId())
	}
	// First client joins an empty room — no existing peers
	if len(rj.GetRoom().GetPeers()) != 0 {
		t.Fatalf("expected 0 peers for first client, got %d", len(rj.GetRoom().GetPeers()))
	}
}

func TestJoinRoom_SecondClient(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// First client joins
	action1 := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:  "sess1",
				Username:   "Alice",
				Metadata:   map[string]string{"tool": "whiteboard"},
				ClientType: "browser",
			},
		},
	}
	event1, err := svc.HandleAction(ctx, action1)
	if err != nil {
		t.Fatalf("first join error: %v", err)
	}
	clientId1 := event1.GetRoomJoined().GetClientId()

	// Second client joins
	action2 := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:  "sess1",
				Username:   "Bob",
				Metadata:   map[string]string{"tool": "whiteboard"},
				ClientType: "browser",
			},
		},
	}
	event2, err := svc.HandleAction(ctx, action2)
	if err != nil {
		t.Fatalf("second join error: %v", err)
	}

	rj := event2.GetRoomJoined()
	if rj == nil {
		t.Fatal("expected RoomJoined event for second client")
	}
	peers := rj.GetRoom().GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 existing peer, got %d", len(peers))
	}
	if peers[0].ClientId != clientId1 {
		t.Fatalf("expected peer to be client1 (%s), got %s", clientId1, peers[0].ClientId)
	}
	if peers[0].Username != "Alice" {
		t.Fatalf("expected peer username Alice, got %s", peers[0].Username)
	}
}

func TestJoinRoom_BroadcastsPeerJoined(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// First client joins
	action1 := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: "sess1",
				Username:  "Alice",
				Metadata:  map[string]string{"tool": "whiteboard"},
			},
		},
	}
	event1, _ := svc.HandleAction(ctx, action1)
	clientId1 := event1.GetRoomJoined().GetClientId()

	// Get client1's send channel to watch for broadcasts
	room := svc.GetOrCreateRoom("sess1")
	client1 := room.Clients[clientId1]

	// Second client joins
	action2 := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: "sess1",
				Username:  "Bob",
				Metadata:  map[string]string{"tool": "whiteboard"},
			},
		},
	}
	svc.HandleAction(ctx, action2)

	// Client1 should receive PeerJoined broadcast
	select {
	case evt := <-client1.SendCh:
		pj := evt.GetPeerJoined()
		if pj == nil {
			t.Fatal("expected PeerJoined event")
		}
		if pj.Peer.Username != "Bob" {
			t.Fatalf("expected peer Bob, got %s", pj.Peer.Username)
		}
	default:
		t.Fatal("client1 should have received PeerJoined broadcast")
	}
}

func TestJoinRoom_EmptyUsernameDefaultsToAnonymous(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	action := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId: "sess1",
				Username:  "",
				Metadata:  map[string]string{"tool": "whiteboard"},
			},
		},
	}
	event, err := svc.HandleAction(ctx, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rj := event.GetRoomJoined()
	if rj == nil {
		t.Fatal("expected RoomJoined event")
	}
	// Verify the client was stored with "Anonymous" username
	room := svc.GetOrCreateRoom("sess1")
	client := room.Clients[rj.ClientId]
	if client.Username != "Anonymous" {
		t.Fatalf("expected username 'Anonymous', got %s", client.Username)
	}
}

func TestLeaveRoom(t *testing.T) {
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

	// Drain PeerJoined from client1's channel
	<-svc.GetOrCreateRoom("sess1").Clients[clientId1].SendCh

	// Client2 leaves
	leaveAction := &pb.CollabAction{
		ClientId: clientId2,
		Action: &pb.CollabAction_Leave{
			Leave: &pb.LeaveRoom{Reason: "bye"},
		},
	}
	_, err := svc.HandleAction(ctx, leaveAction)
	if err != nil {
		t.Fatalf("leave error: %v", err)
	}

	// Client1 should receive PeerLeft
	room := svc.GetOrCreateRoom("sess1")
	select {
	case evt := <-room.Clients[clientId1].SendCh:
		pl := evt.GetPeerLeft()
		if pl == nil {
			t.Fatal("expected PeerLeft event")
		}
		if pl.ClientId != clientId2 {
			t.Fatalf("expected departed client %s, got %s", clientId2, pl.ClientId)
		}
		if pl.PeerCount != 1 {
			t.Fatalf("expected peer count 1, got %d", pl.PeerCount)
		}
	default:
		t.Fatal("client1 should have received PeerLeft")
	}
}

func TestLeaveRoom_LastClientCleansUp(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	action := &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{SessionId: "sess1", Username: "Alice", Metadata: map[string]string{"tool": "whiteboard"}},
		},
	}
	event, _ := svc.HandleAction(ctx, action)
	clientId := event.GetRoomJoined().GetClientId()

	leaveAction := &pb.CollabAction{
		ClientId: clientId,
		Action: &pb.CollabAction_Leave{
			Leave: &pb.LeaveRoom{Reason: "done"},
		},
	}
	svc.HandleAction(ctx, leaveAction)

	// Room should be cleaned up
	resp, err := svc.ListRooms(ctx, &pb.ListRoomsRequest{})
	if err != nil {
		t.Fatalf("ListRooms error: %v", err)
	}
	if len(resp.Rooms) != 0 {
		t.Fatalf("expected 0 rooms after last client leaves, got %d", len(resp.Rooms))
	}
}

func TestLeaveRoom_NonexistentClient(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Create room with one client
	svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{SessionId: "sess1", Username: "Alice", Metadata: map[string]string{"tool": "whiteboard"}},
		},
	})

	leaveAction := &pb.CollabAction{
		ClientId: "nonexistent",
		Action: &pb.CollabAction_Leave{
			Leave: &pb.LeaveRoom{Reason: "ghost"},
		},
	}
	_, err := svc.HandleAction(ctx, leaveAction)
	if err == nil {
		t.Fatal("expected error for nonexistent client leave")
	}
}

func TestPresenceUpdate(t *testing.T) {
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

	// Client2 sends presence update
	presAction := &pb.CollabAction{
		ClientId: clientId2,
		Action: &pb.CollabAction_Presence{
			Presence: &pb.PresenceUpdate{IsActive: false, Username: "Bob"},
		},
	}
	_, err := svc.HandleAction(ctx, presAction)
	if err != nil {
		t.Fatalf("presence update error: %v", err)
	}

	// Client1 should receive presence broadcast
	room := svc.GetOrCreateRoom("sess1")
	select {
	case evt := <-room.Clients[clientId1].SendCh:
		p := evt.GetPresence()
		if p == nil {
			t.Fatal("expected PresenceUpdate event")
		}
		if p.IsActive != false {
			t.Fatal("expected IsActive=false")
		}
	default:
		t.Fatal("client1 should have received presence update")
	}
}

func TestHandleAction_NilAction(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	_, err := svc.HandleAction(ctx, &pb.CollabAction{})
	if err == nil {
		t.Fatal("expected error for empty action (no oneof set)")
	}
}

func TestFindRoomForClient(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	event, _ := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{SessionId: "sess1", Username: "Alice", Metadata: map[string]string{"tool": "whiteboard"}},
		},
	})
	clientId := event.GetRoomJoined().GetClientId()

	room := svc.findRoomForClient(clientId)
	if room == nil {
		t.Fatal("expected to find room for client")
	}
	if room.SessionId != "sess1" {
		t.Fatalf("expected session sess1, got %s", room.SessionId)
	}

	// Unknown client
	if svc.findRoomForClient("nonexistent") != nil {
		t.Fatal("expected nil for unknown client")
	}
}
