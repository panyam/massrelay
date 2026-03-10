package services

import (
	"context"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

func TestOwnerJoin_SetsOwnership(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	clientId, err := joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	room, _ := svc.GetOrCreateRoom("sess1")
	if room.OwnerClientId != clientId {
		t.Fatalf("expected OwnerClientId %s, got %s", clientId, room.OwnerClientId)
	}
	if room.OwnerBrowserId != "browser-1" {
		t.Fatalf("expected OwnerBrowserId browser-1, got %s", room.OwnerBrowserId)
	}
}

func TestOwnerJoin_RoomJoinedIncludesOwnerClientId(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	ownerClientId, _ := joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")

	// Second client joins — RoomJoined should include owner_client_id
	event, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{SessionId: "sess1", Username: "Bob", Metadata: map[string]string{"tool": "whiteboard"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rj := event.GetRoomJoined()
	if rj.GetRoom().GetOwnerClientId() != ownerClientId {
		t.Fatalf("expected owner_client_id %s, got %s", ownerClientId, rj.GetRoom().GetOwnerClientId())
	}
}

func TestOwnerJoin_DifferentBrowserRejected(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	_, err := joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")
	if err != nil {
		t.Fatalf("first owner join failed: %v", err)
	}

	// Different browser tries to claim ownership
	_, err = joinAsOwner(svc, ctx, "sess1", "Bob", "browser-2")
	if err == nil {
		t.Fatal("expected error for second owner from different browser")
	}
}

func TestOwnerJoin_SameBrowserAllowed(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	_, err := joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")
	if err != nil {
		t.Fatalf("first owner join failed: %v", err)
	}

	// Same browser, second tab — should be allowed
	clientId2, err := joinAsOwner(svc, ctx, "sess1", "Alice Tab 2", "browser-1")
	if err != nil {
		t.Fatalf("same-browser second tab should be allowed: %v", err)
	}
	if clientId2 == "" {
		t.Fatal("expected valid client ID for second tab")
	}
}

func TestOwnerLeave_TransfersToSameBrowserTab(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	ownerId, _ := joinAsOwner(svc, ctx, "sess1", "Alice Tab 1", "browser-1")
	tab2Id, _ := joinAsOwner(svc, ctx, "sess1", "Alice Tab 2", "browser-1")
	followerId, _ := joinAsFollower(svc, ctx, "sess1", "Bob", "browser-2")

	// Drain PeerJoined broadcasts
	room, _ := svc.GetOrCreateRoom("sess1")
	drainCh(room.Clients[ownerId].SendCh)
	drainCh(room.Clients[tab2Id].SendCh)
	drainCh(room.Clients[followerId].SendCh)

	// Owner leaves
	leaveRoom(svc, ctx, ownerId)

	// Tab2 should receive OwnerChanged
	evt := <-room.Clients[tab2Id].SendCh
	oc := evt.GetOwnerChanged()
	if oc == nil {
		t.Fatal("expected OwnerChanged event for tab2")
	}
	if oc.NewOwnerClientId != tab2Id {
		t.Fatalf("expected new owner %s, got %s", tab2Id, oc.NewOwnerClientId)
	}

	// Follower should also receive OwnerChanged
	evt2 := <-room.Clients[followerId].SendCh
	if evt2.GetOwnerChanged() == nil {
		t.Fatal("expected OwnerChanged event for follower")
	}

	// Room should still have owner set to tab2
	if room.OwnerClientId != tab2Id {
		t.Fatalf("expected room owner to be %s, got %s", tab2Id, room.OwnerClientId)
	}
}

func TestOwnerLeave_NoSameBrowser_SessionEnded(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	ownerId, _ := joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")
	followerId, _ := joinAsFollower(svc, ctx, "sess1", "Bob", "browser-2")

	// Drain PeerJoined broadcasts
	room, _ := svc.GetOrCreateRoom("sess1")
	drainCh(room.Clients[ownerId].SendCh)
	drainCh(room.Clients[followerId].SendCh)

	// Capture follower channel before owner leaves (room.CloseAllClients will close it)
	followerCh := room.Clients[followerId].SendCh

	// Owner leaves — no same-browser client
	leaveRoom(svc, ctx, ownerId)

	// Follower should receive SessionEnded
	evt := <-followerCh
	se := evt.GetSessionEnded()
	if se == nil {
		t.Fatal("expected SessionEnded event for follower")
	}
	if se.Reason != "owner_disconnected" {
		t.Fatalf("expected reason 'owner_disconnected', got %s", se.Reason)
	}

	// Room should be removed
	resp, _ := svc.ListRooms(ctx, &pb.ListRoomsRequest{})
	if len(resp.Rooms) != 0 {
		t.Fatalf("expected 0 rooms after session ended, got %d", len(resp.Rooms))
	}
}

func TestNonOwnerLeave_NormalPeerLeftBehavior(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	ownerId, _ := joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")
	followerId, _ := joinAsFollower(svc, ctx, "sess1", "Bob", "browser-2")

	room, _ := svc.GetOrCreateRoom("sess1")
	drainCh(room.Clients[ownerId].SendCh)

	// Follower leaves — normal PeerLeft, no session end
	leaveRoom(svc, ctx, followerId)

	evt := <-room.Clients[ownerId].SendCh
	pl := evt.GetPeerLeft()
	if pl == nil {
		t.Fatal("expected PeerLeft event")
	}
	if pl.ClientId != followerId {
		t.Fatalf("expected departed client %s, got %s", followerId, pl.ClientId)
	}

	// Room should still exist with owner
	if room.OwnerClientId != ownerId {
		t.Fatalf("owner should still be %s", ownerId)
	}
}
