package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	<-svc.GetRoomByID("sess1").Clients[clientId1].SendCh

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
	room, _ := svc.GetOrCreateRoom("sess1")
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

	<-svc.GetRoomByID("sess1").Clients[clientId1].SendCh

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

	room, _ := svc.GetOrCreateRoom("sess1")
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

	<-svc.GetRoomByID("sess1").Clients[clientId1].SendCh

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

	room, _ := svc.GetOrCreateRoom("sess1")
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

func TestBroadcastAppMessage(t *testing.T) {
	svc := NewCollabService()
	ctx := context.Background()

	joinAction := func(name string) string {
		action := &pb.CollabAction{
			Action: &pb.CollabAction_Join{
				Join: &pb.JoinRoom{SessionId: "sessAM", Username: name, Metadata: map[string]string{"tool": "diffpp"}},
			},
		}
		event, _ := svc.HandleAction(ctx, action)
		return event.GetRoomJoined().GetClientId()
	}
	clientId1 := joinAction("window1")
	clientId2 := joinAction("window2")

	// Drain window1's PeerJoined for window2.
	<-svc.GetRoomByID("sessAM").Clients[clientId1].SendCh

	// window2 publishes a generic app message.
	payload := []byte(`{"requestId":"r1","markdown":"the answer"}`)
	action := &pb.CollabAction{
		ClientId: clientId2,
		Action:   &pb.CollabAction_AppMessage{AppMessage: &pb.AppMessage{Kind: "answer", Payload: payload}},
	}
	if _, err := svc.HandleAction(ctx, action); err != nil {
		t.Fatalf("broadcast error: %v", err)
	}

	room := svc.GetRoomByID("sessAM")
	// window1 (the other peer) receives it verbatim.
	select {
	case evt := <-room.Clients[clientId1].SendCh:
		am := evt.GetAppMessage()
		if am == nil {
			t.Fatalf("expected AppMessage event, got %T", evt.Event)
		}
		if am.GetKind() != "answer" || string(am.GetPayload()) != string(payload) {
			t.Fatalf("app message not fanned out verbatim: kind=%q payload=%q", am.GetKind(), am.GetPayload())
		}
	default:
		t.Fatal("window1 should have received the AppMessage broadcast")
	}

	// The sender (window2) must NOT receive its own message (BroadcastExcept).
	select {
	case evt := <-room.Clients[clientId2].SendCh:
		if evt.GetAppMessage() != nil {
			t.Fatal("sender should not receive its own AppMessage")
		}
	default:
	}
}

// joinN joins len(names) peers to sessionId and drains every PeerJoined, so
// each SendCh starts empty.
func joinN(t *testing.T, svc *CollabService, sessionId string, names ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(names))
	for _, name := range names {
		event, err := svc.HandleAction(context.Background(), &pb.CollabAction{
			Action: &pb.CollabAction_Join{Join: &pb.JoinRoom{SessionId: sessionId, Username: name}},
		})
		if err != nil {
			t.Fatalf("join %s: %v", name, err)
		}
		ids = append(ids, event.GetRoomJoined().GetClientId())
	}
	room := svc.GetRoomByID(sessionId)
	for _, id := range ids {
		drainCh(room.Clients[id].SendCh)
	}
	return ids
}

func sendAppMessage(t *testing.T, svc *CollabService, from, kind string, to ...string) *pb.CollabEvent {
	t.Helper()
	resp, err := svc.HandleAction(context.Background(), &pb.CollabAction{
		ClientId: from,
		Action: &pb.CollabAction_AppMessage{AppMessage: &pb.AppMessage{
			Kind: kind, Payload: []byte("args"), ToClientIds: to,
		}},
	})
	if err != nil {
		t.Fatalf("app message: %v", err)
	}
	return resp
}

func TestAppMessageAddressedReachesOnlyTarget(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessAddr", "bridge", "page1", "page2")
	bridge, page1, page2 := ids[0], ids[1], ids[2]

	if resp := sendAppMessage(t, svc, bridge, "tools/call", page2); resp != nil {
		t.Fatalf("expected no direct response on successful delivery, got %v", resp)
	}

	room := svc.GetRoomByID("sessAddr")
	select {
	case evt := <-room.Clients[page2].SendCh:
		am := evt.GetAppMessage()
		if am == nil || am.GetKind() != "tools/call" || string(am.GetPayload()) != "args" {
			t.Fatalf("target got wrong event: %v", evt)
		}
		if evt.GetFromClientId() != bridge {
			t.Fatalf("from_client_id = %q, want %q", evt.GetFromClientId(), bridge)
		}
	default:
		t.Fatal("addressed peer did not receive the AppMessage")
	}
	select {
	case evt := <-room.Clients[page1].SendCh:
		t.Fatalf("non-addressed peer received %v", evt)
	default:
	}
	select {
	case evt := <-room.Clients[bridge].SendCh:
		t.Fatalf("sender received %v", evt)
	default:
	}
}

func TestAppMessageAddressedPeerNotFound(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessA", "bridge", "page1", "gone")
	bridge, page1, gone := ids[0], ids[1], ids[2]
	other := joinN(t, svc, "sessB", "elsewhere")[0]
	if err := leaveRoom(svc, context.Background(), gone); err != nil {
		t.Fatalf("leave: %v", err)
	}
	room := svc.GetRoomByID("sessA")
	drainCh(room.Clients[bridge].SendCh)
	drainCh(room.Clients[page1].SendCh)

	for name, to := range map[string]string{"left the room": gone, "in another room": other} {
		resp := sendAppMessage(t, svc, bridge, "tools/call", to)
		if resp.GetError().GetCode() != "PEER_NOT_FOUND" {
			t.Fatalf("%s: expected PEER_NOT_FOUND, got %v", name, resp)
		}
		select {
		case evt := <-room.Clients[page1].SendCh:
			t.Fatalf("%s: room peer received %v", name, evt)
		default:
		}
		select {
		case evt := <-svc.GetRoomByID("sessB").Clients[other].SendCh:
			t.Fatalf("%s: peer in another room received %v", name, evt)
		default:
		}
	}
}

func TestDeliverAppMessage(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessD", "page1", "page2")
	room := svc.GetRoomByID("sessD")

	if err := svc.DeliverAppMessage("sessD", "", &pb.AppMessage{Kind: "k", ToClientIds: []string{ids[1]}}); err != nil {
		t.Fatalf("addressed deliver: %v", err)
	}
	select {
	case evt := <-room.Clients[ids[1]].SendCh:
		if evt.GetAppMessage().GetKind() != "k" || evt.GetFromClientId() != "" {
			t.Fatalf("unexpected event %v", evt)
		}
	default:
		t.Fatal("target did not receive server-originated AppMessage")
	}
	select {
	case evt := <-room.Clients[ids[0]].SendCh:
		t.Fatalf("non-target received %v", evt)
	default:
	}

	if err := svc.DeliverAppMessage("sessD", ids[0], &pb.AppMessage{Kind: "all"}); err != nil {
		t.Fatalf("fan-out deliver: %v", err)
	}
	if evt := <-room.Clients[ids[1]].SendCh; evt.GetAppMessage().GetKind() != "all" || evt.GetFromClientId() != ids[0] {
		t.Fatalf("fan-out got %v", evt)
	}
	select {
	case evt := <-room.Clients[ids[0]].SendCh:
		t.Fatalf("fan-out echoed to sender: %v", evt)
	default:
	}

	if err := svc.DeliverAppMessage("sessD", "", &pb.AppMessage{ToClientIds: []string{"nobody"}}); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("want ErrPeerNotFound, got %v", err)
	}
	if err := svc.DeliverAppMessage("no-such-room", "", &pb.AppMessage{}); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("want ErrRoomNotFound, got %v", err)
	}
}

func TestAppMessageAddressedToSeveralPeers(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessMany", "bridge", "page1", "page2", "page3", "bystander")
	bridge, pages, bystander := ids[0], ids[1:4], ids[4]

	if resp := sendAppMessage(t, svc, bridge, "tools/call", pages...); resp != nil {
		t.Fatalf("expected no direct response on successful delivery, got %v", resp)
	}

	room := svc.GetRoomByID("sessMany")
	for _, page := range pages {
		select {
		case evt := <-room.Clients[page].SendCh:
			am := evt.GetAppMessage()
			if am.GetKind() != "tools/call" || evt.GetFromClientId() != bridge {
				t.Fatalf("%s got wrong event: %v", page, evt)
			}
			if got := am.GetToClientIds(); len(got) != 1 || got[0] != page {
				t.Fatalf("%s sees to_client_ids %v, want only its own id", page, got)
			}
		default:
			t.Fatalf("%s did not receive the AppMessage", page)
		}
	}
	for name, id := range map[string]string{"bystander": bystander, "sender": bridge} {
		select {
		case evt := <-room.Clients[id].SendCh:
			t.Fatalf("%s received %v", name, evt)
		default:
		}
	}
}

func TestAppMessageAddressedSomeAbsentStillDeliversToTheRest(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessPart", "bridge", "page1", "gone")
	bridge, page1, gone := ids[0], ids[1], ids[2]
	if err := leaveRoom(svc, context.Background(), gone); err != nil {
		t.Fatalf("leave: %v", err)
	}
	room := svc.GetRoomByID("sessPart")
	drainCh(room.Clients[bridge].SendCh)
	drainCh(room.Clients[page1].SendCh)

	resp := sendAppMessage(t, svc, bridge, "tools/call", page1, gone)
	if resp.GetError().GetCode() != "PEER_NOT_FOUND" {
		t.Fatalf("expected PEER_NOT_FOUND, got %v", resp)
	}
	if msg := resp.GetError().GetMessage(); !strings.Contains(msg, gone) || strings.Contains(msg, page1) {
		t.Fatalf("PEER_NOT_FOUND should name only the absent peer, got %q", msg)
	}
	select {
	case evt := <-room.Clients[page1].SendCh:
		if evt.GetAppMessage().GetKind() != "tools/call" {
			t.Fatalf("page1 got wrong event: %v", evt)
		}
	default:
		t.Fatal("present peer should still receive the message")
	}
}

func TestAppMessageAddressedDuplicateIdsDeliverOnce(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessDup", "bridge", "page1")
	if resp := sendAppMessage(t, svc, ids[0], "tools/call", ids[1], ids[1]); resp != nil {
		t.Fatalf("unexpected response %v", resp)
	}
	ch := svc.GetRoomByID("sessDup").Clients[ids[1]].SendCh
	<-ch
	select {
	case evt := <-ch:
		t.Fatalf("duplicate id delivered twice: %v", evt)
	default:
	}
}

func TestDeliverAppMessageSeveralPeers(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessDM", "page1", "page2")
	room := svc.GetRoomByID("sessDM")

	err := svc.DeliverAppMessage("sessDM", "", &pb.AppMessage{Kind: "k", ToClientIds: []string{ids[0], "nobody", ids[1]}})
	if !errors.Is(err, ErrPeerNotFound) || !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("want ErrPeerNotFound naming nobody, got %v", err)
	}
	for _, id := range ids {
		select {
		case evt := <-room.Clients[id].SendCh:
			if got := evt.GetAppMessage().GetToClientIds(); len(got) != 1 || got[0] != id {
				t.Fatalf("%s sees to_client_ids %v", id, got)
			}
		default:
			t.Fatalf("%s did not receive the message", id)
		}
	}
}

// A receiver whose buffer overflows is marked slow, and nothing sent after the
// overflow reaches it, even once its buffer drains: it sees a prefix of the
// sender's messages, never one with a hole in it.
func TestSlowConsumerIsMarkedAndSkipsLaterMessages(t *testing.T) {
	svc := NewCollabService()
	ids := joinN(t, svc, "sessSlow", "bridge", "slow", "fast")
	bridge, slow, fast := ids[0], ids[1], ids[2]
	room := svc.GetRoomByID("sessSlow")
	slowClient := room.Clients[slow]

	for i := 0; i <= cap(slowClient.SendCh); i++ {
		sendAppMessage(t, svc, bridge, fmt.Sprintf("A%d", i), slow)
	}
	select {
	case <-slowClient.Slow():
	default:
		t.Fatal("overflowing receiver should be marked slow")
	}

	drainCh(slowClient.SendCh)
	sendAppMessage(t, svc, bridge, "B", slow, fast)
	select {
	case evt := <-slowClient.SendCh:
		t.Fatalf("slow receiver got a message after its overflow: %v", evt)
	default:
	}
	if evt := <-room.Clients[fast].SendCh; evt.GetAppMessage().GetKind() != "B" {
		t.Fatalf("other receivers are unaffected, got %v", evt)
	}
}
