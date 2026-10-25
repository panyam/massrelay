package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/panyam/oneauth/keys"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
	"github.com/panyam/massrelay/services"
	"github.com/panyam/massrelay/web/middleware"
)

const (
	streamTestClientID = "design-app"
	streamTestSecret   = "stream-test-secret"
)

// streamFromRequest runs a WebSocket upgrade request through the relay auth
// middleware and opens a CollabBidiStream on the resulting context, which is
// what the grpcws handler does with r.Context().
func streamFromRequest(t *testing.T, svc *services.CollabService, token string) *CollabBidiStream {
	t.Helper()
	ks := keys.NewInMemoryKeyStore()
	ks.PutKey(context.Background(), &keys.PutKeyRequest{Record: &keys.KeyRecord{
		ClientID: streamTestClientID, Key: []byte(streamTestSecret), Algorithm: "HS256",
	}})
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{KeyStore: ks, TokenQueryParam: "token"})

	var stream *CollabBidiStream
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stream = NewCollabBidiStream(r.Context(), svc, StreamConfig{})
	})
	target := "/ws/v1/x/sync"
	if token != "" {
		target += "?token=" + token
	}
	rr := httptest.NewRecorder()
	auth.Middleware(h).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
	if stream == nil {
		t.Fatalf("auth rejected request: %d %s", rr.Code, rr.Body.String())
	}
	t.Cleanup(stream.cancel)
	return stream
}

func mintStreamToken(t *testing.T, extra jwt.MapClaims) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":       "user-42",
		"client_id": streamTestClientID,
		"type":      "access",
		"iat":       time.Now().Unix(),
		"exp":       time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(streamTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func recvEvent(t *testing.T, s *CollabBidiStream) *pb.CollabEvent {
	t.Helper()
	select {
	case evt := <-s.sendCh:
		return evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return nil
	}
}

// recvErr calls Recv the way servicekit does and returns its error.
func recvErr(t *testing.T, s *CollabBidiStream) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := s.Recv()
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Recv to return")
		return nil
	}
}

func expectNoEvent(t *testing.T, s *CollabBidiStream) {
	t.Helper()
	select {
	case evt := <-s.sendCh:
		t.Fatalf("unexpected event %v", evt)
	case <-time.After(50 * time.Millisecond):
	}
}

func joinStream(t *testing.T, s *CollabBidiStream, sessionId string) *pb.CollabEvent {
	t.Helper()
	if err := s.Send(&pb.CollabAction{Action: &pb.CollabAction_Join{
		Join: &pb.JoinRoom{SessionId: sessionId, Username: "claims-to-be-admin"},
	}}); err != nil {
		t.Fatalf("join: %v", err)
	}
	return recvEvent(t, s)
}

func TestStreamPeerIdentityFromToken(t *testing.T) {
	svc := services.NewCollabService()
	anon := streamFromRequest(t, svc, "")
	anonId := joinStream(t, anon, "design:abc").GetRoomJoined().GetClientId()

	authed := streamFromRequest(t, svc, mintStreamToken(t, nil))
	joined := joinStream(t, authed, "design:abc").GetRoomJoined()

	if id := joined.GetRoom().GetPeers()[anonId].GetIdentity(); id != nil {
		t.Fatalf("anonymous peer carries identity %v", id)
	}
	id := recvEvent(t, anon).GetPeerJoined().GetPeer().GetIdentity()
	if id.GetSubject() != "user-42" || id.GetClientId() != streamTestClientID {
		t.Fatalf("PeerJoined identity = %v, want sub=user-42 client_id=%s", id, streamTestClientID)
	}
}

func TestJoinRoomJSONCannotSetIdentity(t *testing.T) {
	var action pb.CollabAction
	err := protojson.Unmarshal([]byte(`{"join":{"sessionId":"s","identity":{"subject":"forged"}}}`), &action)
	if err == nil {
		t.Fatalf("JoinRoom JSON with identity decoded: %v", &action)
	}
}

func TestStreamRoomsClaimRefusesOtherRoom(t *testing.T) {
	svc := services.NewCollabService()
	tok := mintStreamToken(t, jwt.MapClaims{"rooms": "design:abc"})

	if evt := joinStream(t, streamFromRequest(t, svc, tok), "design:other"); evt.GetError().GetCode() != "ROOM_FORBIDDEN" {
		t.Fatalf("expected ROOM_FORBIDDEN for another room, got %v", evt)
	}
	if evt := joinStream(t, streamFromRequest(t, svc, tok), "design:abc"); evt.GetRoomJoined() == nil {
		t.Fatalf("expected RoomJoined for the token's room, got %v", evt)
	}
}

func TestStreamStampsSenderClientId(t *testing.T) {
	svc := services.NewCollabService()
	a, b, c := streamFromRequest(t, svc, ""), streamFromRequest(t, svc, ""), streamFromRequest(t, svc, "")
	aId := joinStream(t, a, "s").GetRoomJoined().GetClientId()
	bId := joinStream(t, b, "s").GetRoomJoined().GetClientId()
	joinStream(t, c, "s")
	recvEvent(t, a) // PeerJoined b
	recvEvent(t, a) // PeerJoined c
	recvEvent(t, b) // PeerJoined c

	// b claims to be a.
	if err := b.Send(&pb.CollabAction{
		ClientId: aId,
		Action:   &pb.CollabAction_AppMessage{AppMessage: &pb.AppMessage{Kind: "k"}},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if evt := recvEvent(t, c); evt.GetFromClientId() != bId {
		t.Fatalf("from_client_id = %q, want the real sender %q", evt.GetFromClientId(), bId)
	}
	if evt := recvEvent(t, a); evt.GetFromClientId() != bId {
		t.Fatalf("a got from_client_id = %q, want %q", evt.GetFromClientId(), bId)
	}
	expectNoEvent(t, b)
}

func TestStreamRejectsActionsBeforeJoin(t *testing.T) {
	svc := services.NewCollabService()
	a := streamFromRequest(t, svc, "")
	aId := joinStream(t, a, "s").GetRoomJoined().GetClientId()

	intruder := streamFromRequest(t, svc, "")
	err := intruder.Send(&pb.CollabAction{
		ClientId: aId,
		Action:   &pb.CollabAction_Leave{Leave: &pb.LeaveRoom{Reason: "spoofed"}},
	})
	if err == nil {
		t.Fatal("expected an error for a LeaveRoom sent before joining")
	}
	if room := svc.GetRoomByID("s"); room == nil || room.Clients[aId] == nil {
		t.Fatal("spoofed LeaveRoom removed another peer")
	}
}

func TestStreamRefusesSecondJoin(t *testing.T) {
	svc := services.NewCollabService()
	s := streamFromRequest(t, svc, "")
	firstId := joinStream(t, s, "room-a").GetRoomJoined().GetClientId()

	if evt := joinStream(t, s, "room-b"); evt.GetError().GetCode() != "ALREADY_JOINED" {
		t.Fatalf("expected ALREADY_JOINED for a second join, got %v", evt)
	}
	if svc.GetRoomByID("room-b") != nil {
		t.Fatal("refused second join created room-b")
	}

	other := streamFromRequest(t, svc, "")
	otherId := joinStream(t, other, "room-a").GetRoomJoined().GetClientId()
	if err := other.Send(&pb.CollabAction{
		Action: &pb.CollabAction_AppMessage{AppMessage: &pb.AppMessage{Kind: "k", ToClientIds: []string{firstId}}},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	recvEvent(t, s) // PeerJoined other
	if evt := recvEvent(t, s); evt.GetAppMessage() == nil || evt.GetFromClientId() != otherId {
		t.Fatalf("first client stopped receiving after the refused join: %v", evt)
	}

	s.leaveAndCleanup("test")
	if room := svc.GetRoomByID("room-a"); room != nil && room.Clients[firstId] != nil {
		t.Fatal("first client left behind after its stream cleaned up")
	}
}

func TestStreamAnswersRateLimitedInsteadOfDropping(t *testing.T) {
	svc := services.NewCollabService()
	s := NewCollabBidiStream(context.Background(), svc, StreamConfig{MaxMessageRate: 1})
	t.Cleanup(s.cancel)
	joinStream(t, s, "room-rl")

	for _, id := range []string{"a1", "a2"} {
		if err := s.Send(&pb.CollabAction{ActionId: id, Action: &pb.CollabAction_AppMessage{AppMessage: &pb.AppMessage{Kind: "k"}}}); err != nil {
			t.Fatalf("send %s: %v", id, err)
		}
	}
	evt := recvEvent(t, s)
	if evt.GetError().GetCode() != "RATE_LIMITED" {
		t.Fatalf("expected RATE_LIMITED for the message over the limit, got %v", evt)
	}
	if !strings.Contains(evt.GetError().GetMessage(), "a2") || strings.Contains(evt.GetError().GetMessage(), "a1") {
		t.Fatalf("RATE_LIMITED should name the dropped action a2, got %q", evt.GetError().GetMessage())
	}

	// Leaving is never rate limited.
	if err := s.Send(&pb.CollabAction{Action: &pb.CollabAction_Leave{Leave: &pb.LeaveRoom{}}}); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if evt := recvEvent(t, s); evt.GetPeerLeft() == nil {
		t.Fatalf("leave over the limit should still go through, got %v", evt)
	}
}

// A receiver that stops reading gets the sender's messages in order up to some
// point, then SessionEnded{slow_consumer}, and is removed from the room.
func TestStreamEvictsSlowConsumer(t *testing.T) {
	svc := services.NewCollabService()
	sender := streamFromRequest(t, svc, "")
	reader := streamFromRequest(t, svc, "")
	senderId := joinStream(t, sender, "room-slow").GetRoomJoined().GetClientId()
	readerId := joinStream(t, reader, "room-slow").GetRoomJoined().GetClientId()
	recvEvent(t, sender) // PeerJoined reader

	// 150 overflows the reader's two 64-slot queues, while the sender's own
	// PEER_NOT_FOUND replies after the eviction still fit in its queue.
	const sent = 150
	for i := 0; i < sent; i++ {
		if err := sender.Send(&pb.CollabAction{Action: &pb.CollabAction_AppMessage{AppMessage: &pb.AppMessage{
			Kind: strconv.Itoa(i), ToClientIds: []string{readerId},
		}}}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	next := 0
	for {
		evt := recvEvent(t, reader)
		if end := evt.GetSessionEnded(); end != nil {
			if end.GetReason() != "slow_consumer" {
				t.Fatalf("reason = %q, want slow_consumer", end.GetReason())
			}
			if err := recvErr(t, reader); err != io.EOF {
				t.Fatalf("Recv after slow_consumer = %v, want io.EOF so servicekit closes the socket", err)
			}
			break
		}
		if got := evt.GetAppMessage().GetKind(); got != strconv.Itoa(next) {
			t.Fatalf("message %d arrived as %q: out of order or skipped", next, got)
		}
		next++
	}
	if next == 0 || next >= sent {
		t.Fatalf("expected a strict prefix of the %d messages, got %d", sent, next)
	}

	for {
		evt := recvEvent(t, sender)
		if evt.GetError().GetCode() == "PEER_NOT_FOUND" {
			continue // sends that landed after the eviction
		}
		if left := evt.GetPeerLeft(); left.GetClientId() != readerId || left.GetReason() != "slow_consumer" {
			t.Fatalf("sender should see the reader leave as slow_consumer, got %v", evt)
		}
		break
	}
	if room := svc.GetRoomByID("room-slow"); room == nil || room.Clients[readerId] != nil || room.Clients[senderId] == nil {
		t.Fatal("slow reader should be gone from the room and the sender still in it")
	}
}

// A connection never outlives its token: at exp the client leaves its room,
// gets SessionEnded{token_expired}, and the stream ends.
func TestStreamEndsWhenTokenExpires(t *testing.T) {
	svc := services.NewCollabService()
	peer := streamFromRequest(t, svc, "")
	joinStream(t, peer, "room-exp")
	authed := streamFromRequest(t, svc, mintStreamToken(t, jwt.MapClaims{"exp": time.Now().Add(1500 * time.Millisecond).Unix()}))
	authedId := joinStream(t, authed, "room-exp").GetRoomJoined().GetClientId()
	recvEvent(t, peer) // PeerJoined authed

	var end *pb.SessionEnded
	select {
	case evt := <-authed.sendCh:
		end = evt.GetSessionEnded()
	case <-time.After(3 * time.Second):
		t.Fatal("stream still open 3s after a token that expired within 1.5s")
	}
	if end.GetReason() != "token_expired" {
		t.Fatalf("got %v, want SessionEnded{token_expired}", end)
	}
	if err := recvErr(t, authed); err != io.EOF {
		t.Fatalf("Recv after token_expired = %v, want io.EOF so servicekit closes the socket", err)
	}
	if left := recvEvent(t, peer).GetPeerLeft(); left.GetClientId() != authedId {
		t.Fatalf("peer saw %v, want PeerLeft for %s", left, authedId)
	}
}

func TestStreamWithoutTokenHasNoExpiry(t *testing.T) {
	svc := services.NewCollabService()
	if s := streamFromRequest(t, svc, ""); s.expiry != nil {
		t.Fatal("an anonymous stream got an expiry timer")
	}
	if s := streamFromRequest(t, svc, mintStreamToken(t, nil)); s.expiry == nil {
		t.Fatal("a stream with an exp claim got no expiry timer")
	}
}

// An eviction and an expiry ending the same stream send one SessionEnded.
func TestStreamEndsOnce(t *testing.T) {
	s := streamFromRequest(t, services.NewCollabService(), "")
	joinStream(t, s, "room-once")
	s.endSession("slow_consumer")
	s.endSession("token_expired")
	if end := recvEvent(t, s).GetSessionEnded(); end.GetReason() != "slow_consumer" {
		t.Fatalf("first event %v, want SessionEnded{slow_consumer}", end)
	}
	if err := recvErr(t, s); err != io.EOF {
		t.Fatalf("Recv after the end = %v, want io.EOF", err)
	}
}
