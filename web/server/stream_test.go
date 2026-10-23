package server

import (
	"context"
	"net/http"
	"net/http/httptest"
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
