package services

import (
	"context"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

func TestGetRoom(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Join a room
	svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{SessionId: "sess1", Username: "Alice", Metadata: map[string]string{"tool": "whiteboard"}},
		},
	})

	resp, err := svc.GetRoom(ctx, &pb.GetRoomRequest{SessionId: "sess1"})
	if err != nil {
		t.Fatalf("GetRoom error: %v", err)
	}
	room := resp.GetRoom()
	if room.GetSessionId() != "sess1" {
		t.Fatalf("expected session sess1, got %s", room.GetSessionId())
	}
	if len(room.GetPeers()) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(room.GetPeers()))
	}
	for _, p := range room.GetPeers() {
		if p.Username != "Alice" {
			t.Fatalf("expected username Alice, got %s", p.Username)
		}
	}
}

func TestGetRoom_Nonexistent(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	_, err := svc.GetRoom(ctx, &pb.GetRoomRequest{SessionId: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent room")
	}
}

func TestGetRoom_IncludesOwnerClientId(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	ownerId, _ := joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")

	resp, err := svc.GetRoom(ctx, &pb.GetRoomRequest{SessionId: "sess1"})
	if err != nil {
		t.Fatalf("GetRoom error: %v", err)
	}
	if resp.GetRoom().GetOwnerClientId() != ownerId {
		t.Fatalf("expected owner_client_id %s, got %s", ownerId, resp.GetRoom().GetOwnerClientId())
	}
}

func TestGetRoom_PeerInfoIncludesIsOwner(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	joinAsOwner(svc, ctx, "sess1", "Alice", "browser-1")
	joinAsFollower(svc, ctx, "sess1", "Bob", "browser-2")

	resp, err := svc.GetRoom(ctx, &pb.GetRoomRequest{SessionId: "sess1"})
	if err != nil {
		t.Fatalf("GetRoom error: %v", err)
	}

	ownerCount := 0
	for _, p := range resp.GetRoom().GetPeers() {
		if p.IsOwner {
			ownerCount++
			if p.Username != "Alice" {
				t.Fatalf("expected owner to be Alice, got %s", p.Username)
			}
		}
	}
	if ownerCount != 1 {
		t.Fatalf("expected exactly 1 owner in peers, got %d", ownerCount)
	}
}

func TestGetRoom_IncludesEncrypted(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Create encrypted room
	svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:       "sess-enc-rest",
				Username:        "Owner",
				Metadata:        map[string]string{"tool": "whiteboard"},
				IsOwner:         true,
				Encrypted:       true,
				ProtocolVersion: 2,
			},
		},
	})

	resp, err := svc.GetRoom(ctx, &pb.GetRoomRequest{SessionId: "sess-enc-rest"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetRoom().GetEncrypted() {
		t.Fatal("expected encrypted=true in GetRoomResponse")
	}
}

func TestListRooms(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Create two rooms with clients
	for _, sess := range []string{"sess1", "sess2"} {
		svc.HandleAction(ctx, &pb.CollabAction{
			Action: &pb.CollabAction_Join{
				Join: &pb.JoinRoom{SessionId: sess, Username: "User", Metadata: map[string]string{"tool": "whiteboard"}},
			},
		})
	}

	resp, err := svc.ListRooms(ctx, &pb.ListRoomsRequest{})
	if err != nil {
		t.Fatalf("ListRooms error: %v", err)
	}
	if len(resp.Rooms) != 2 {
		t.Fatalf("expected 2 rooms, got %d", len(resp.Rooms))
	}
}

func TestCollabClientEmbedsPeerInfo(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	event, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:  "sess-embed",
				Username:   "Alice",
				Metadata:   map[string]string{"tool": "whiteboard", "role": "designer"},
				ClientType: "browser",
				AvatarUrl:  "https://example.com/alice.png",
				IsOwner:    true,
				BrowserId:  "browser-1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	clientId := event.GetRoomJoined().GetClientId()

	room := svc.GetOrCreateRoom("sess-embed")
	client := room.Clients[clientId]

	// Verify PeerInfo is embedded and fields are promoted
	if client.PeerInfo == nil {
		t.Fatal("expected embedded PeerInfo to be non-nil")
	}
	if client.ClientId != clientId {
		t.Fatalf("expected promoted ClientId %s, got %s", clientId, client.ClientId)
	}
	if client.Username != "Alice" {
		t.Fatalf("expected promoted Username Alice, got %s", client.Username)
	}
	if client.AvatarUrl != "https://example.com/alice.png" {
		t.Fatalf("expected promoted AvatarUrl, got %s", client.AvatarUrl)
	}
	if client.ClientType != "browser" {
		t.Fatalf("expected promoted ClientType browser, got %s", client.ClientType)
	}
	if !client.IsActive {
		t.Fatal("expected promoted IsActive=true")
	}
	if !client.IsOwner {
		t.Fatal("expected promoted IsOwner=true")
	}
	// Metadata is now on PeerInfo
	if client.Metadata["tool"] != "whiteboard" {
		t.Fatalf("expected metadata tool=whiteboard, got %v", client.Metadata)
	}
	if client.Metadata["role"] != "designer" {
		t.Fatalf("expected metadata role=designer, got %v", client.Metadata)
	}
	// Server-only fields remain on CollabClient
	if client.SessionId != "sess-embed" {
		t.Fatalf("expected SessionId sess-embed, got %s", client.SessionId)
	}
	if client.BrowserId != "browser-1" {
		t.Fatalf("expected BrowserId browser-1, got %s", client.BrowserId)
	}
}

func TestGetPeerInfo_ReturnsEmbeddedPeerInfo(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:  "sess-pi",
				Username:   "Alice",
				Metadata:   map[string]string{"tool": "whiteboard"},
				ClientType: "browser",
			},
		},
	})

	room := svc.GetOrCreateRoom("sess-pi")
	peers := room.GetPeerInfo()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	for _, p := range peers {
		if p.Username != "Alice" {
			t.Fatalf("expected username Alice, got %s", p.Username)
		}
		if p.Metadata["tool"] != "whiteboard" {
			t.Fatalf("expected peer metadata tool=whiteboard, got %v", p.Metadata)
		}
	}
}

func TestRoomToProto(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Owner creates room with metadata and title
	joinAsOwner(svc, ctx, "sess-proto", "Alice", "browser-1")
	joinAsFollower(svc, ctx, "sess-proto", "Bob", "browser-2")

	room := svc.GetOrCreateRoom("sess-proto")
	room.mu.Lock()
	room.Metadata = map[string]string{"tool": "whiteboard"}
	room.Title = "Design Session"
	room.Encrypted = true
	room.mu.Unlock()

	proto := room.ToProto()
	if proto.SessionId != "sess-proto" {
		t.Fatalf("expected session_id sess-proto, got %s", proto.SessionId)
	}
	if len(proto.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(proto.Peers))
	}
	if proto.OwnerClientId == "" {
		t.Fatal("expected non-empty owner_client_id")
	}
	if proto.CreatedAt == nil {
		t.Fatal("expected non-nil created_at")
	}
	if proto.Metadata["tool"] != "whiteboard" {
		t.Fatalf("expected metadata tool=whiteboard, got %v", proto.Metadata)
	}
	if !proto.Encrypted {
		t.Fatal("expected encrypted=true")
	}
	if proto.Title != "Design Session" {
		t.Fatalf("expected title 'Design Session', got %s", proto.Title)
	}
}

func TestRoomJoinedContainsRoomProto(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	// Owner joins
	event1, err := svc.HandleAction(ctx, &pb.CollabAction{
		Action: &pb.CollabAction_Join{
			Join: &pb.JoinRoom{
				SessionId:       "sess-nested",
				Username:        "Alice",
				IsOwner:         true,
				Encrypted:       true,
				ProtocolVersion: 2,
				BrowserId:       "browser-1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rj := event1.GetRoomJoined()
	if rj == nil {
		t.Fatal("expected RoomJoined")
	}
	if rj.Room == nil {
		t.Fatal("expected nested Room message in RoomJoined")
	}
	if rj.Room.SessionId != "sess-nested" {
		t.Fatalf("expected session_id in Room, got %s", rj.Room.SessionId)
	}
	if !rj.Room.Encrypted {
		t.Fatal("expected encrypted=true in Room")
	}
	// MaxPeers and ProtocolVersion stay on RoomJoined directly
	if rj.MaxPeers != int32(svc.MaxPeersPerRoom) {
		t.Fatalf("expected max_peers=%d on RoomJoined, got %d", svc.MaxPeersPerRoom, rj.MaxPeers)
	}
	if rj.ProtocolVersion != svc.ProtocolVersion {
		t.Fatalf("expected protocol_version=%d on RoomJoined, got %d", svc.ProtocolVersion, rj.ProtocolVersion)
	}
}

func TestListRooms_Empty(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	resp, err := svc.ListRooms(ctx, &pb.ListRoomsRequest{})
	if err != nil {
		t.Fatalf("ListRooms error: %v", err)
	}
	if resp.Rooms == nil {
		t.Fatal("expected non-nil rooms slice")
	}
	if len(resp.Rooms) != 0 {
		t.Fatalf("expected 0 rooms, got %d", len(resp.Rooms))
	}
}
