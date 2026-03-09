package services

import (
	"context"
	"strings"
	"testing"

	pb "github.com/panyam/massrelay/gen/go/massrelay/v1/models"
)

func TestLogPayloads_DisabledByDefault(t *testing.T) {
	svc := NewCollabService()
	// LogPayloads defaults to 0 (disabled)
	_, c2 := setupTwoClients(t, svc, "log-off", map[string]string{"tool": "whiteboard"})
	ctx := context.Background()

	output := captureLog(func() {
		svc.HandleAction(ctx, &pb.CollabAction{
			ClientId: c2,
			Action: &pb.CollabAction_SceneUpdate{
				SceneUpdate: &pb.SceneUpdate{
					Elements: []*pb.ElementUpdate{
						{Id: "e1", Version: 1, Data: `{"type":"rectangle","x":10}`},
					},
				},
			},
		})
	})

	if strings.Contains(output, "SceneUpdate element") {
		t.Fatalf("expected no payload log when LogPayloads=0, got: %s", output)
	}
}

func TestLogPayloads_SceneUpdate(t *testing.T) {
	svc := NewCollabService()
	svc.LogPayloads = 20
	_, c2 := setupTwoClients(t, svc, "log-scene", map[string]string{"tool": "whiteboard"})
	ctx := context.Background()

	longData := `{"type":"rectangle","x":100,"y":200,"width":300}`
	output := captureLog(func() {
		svc.HandleAction(ctx, &pb.CollabAction{
			ClientId: c2,
			Action: &pb.CollabAction_SceneUpdate{
				SceneUpdate: &pb.SceneUpdate{
					Elements: []*pb.ElementUpdate{
						{Id: "e1", Version: 1, Data: longData},
					},
				},
			},
		})
	})

	if !strings.Contains(output, "SceneUpdate element") {
		t.Fatalf("expected SceneUpdate element log, got: %s", output)
	}
	if !strings.Contains(output, "e1") {
		t.Fatalf("expected element id in log, got: %s", output)
	}
	// Data should be truncated to 20 chars
	if strings.Contains(output, longData) {
		t.Fatalf("expected data to be truncated, but found full data in log")
	}
}

func TestLogPayloads_TextUpdate(t *testing.T) {
	svc := NewCollabService()
	svc.LogPayloads = 15
	_, c2 := setupTwoClients(t, svc, "log-text", map[string]string{"tool": "text-editor"})
	ctx := context.Background()

	longText := "flowchart TD\n  A --> B --> C --> D"
	output := captureLog(func() {
		svc.HandleAction(ctx, &pb.CollabAction{
			ClientId: c2,
			Action: &pb.CollabAction_TextUpdate{
				TextUpdate: &pb.TextUpdate{Text: longText, Version: 1},
			},
		})
	})

	if !strings.Contains(output, "TextUpdate") {
		t.Fatalf("expected TextUpdate log, got: %s", output)
	}
	// Text should be truncated to 15 chars
	if strings.Contains(output, longText) {
		t.Fatalf("expected text to be truncated, but found full text in log")
	}
}

func TestLogPayloads_SceneInitResponse(t *testing.T) {
	svc := NewCollabService()
	svc.LogPayloads = 10
	_, c2 := setupTwoClients(t, svc, "log-init", map[string]string{"tool": "whiteboard"})
	ctx := context.Background()

	longPayload := `{"elements":[{"id":"1","type":"rect"}],"appState":{}}`
	output := captureLog(func() {
		svc.HandleAction(ctx, &pb.CollabAction{
			ClientId: c2,
			Action: &pb.CollabAction_SceneInitResponse{
				SceneInitResponse: &pb.SceneInitResponse{Payload: longPayload},
			},
		})
	})

	if !strings.Contains(output, "SceneInitResponse") {
		t.Fatalf("expected SceneInitResponse log, got: %s", output)
	}
	if strings.Contains(output, longPayload) {
		t.Fatalf("expected payload to be truncated, but found full payload in log")
	}
}
