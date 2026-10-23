package espapi

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi/pb"
)

func TestHandledMessagesHaveIDs(t *testing.T) {
	seen := map[uint32]string{}
	for _, m := range handledMessages {
		name := string(m.ProtoReflect().Descriptor().FullName())
		id := messageID(m)
		if id == 0 {
			t.Errorf("%s has no (id) option", name)
			continue
		}
		if other, dup := seen[id]; dup {
			t.Errorf("%s and %s share id %d", name, other, id)
		}
		seen[id] = name
	}
}

// Spot-check IDs against api.proto, so a broken option lookup cannot go
// unnoticed.
func TestKnownMessageIDs(t *testing.T) {
	tests := []struct {
		msg  proto.Message
		want uint32
	}{
		{(*pb.HelloRequest)(nil), 1},
		{(*pb.HelloResponse)(nil), 2},
		{(*pb.DisconnectRequest)(nil), 5},
		{(*pb.PingRequest)(nil), 7},
		{(*pb.ListEntitiesRequest)(nil), 11},
		{(*pb.ListEntitiesDoneResponse)(nil), 19},
		{(*pb.SubscribeStatesRequest)(nil), 20},
		{(*pb.ListEntitiesSelectResponse)(nil), 52},
		{(*pb.SelectCommandRequest)(nil), 54},
		{(*pb.ButtonCommandRequest)(nil), 62},
		{(*pb.SwitchCommandRequest)(nil), 33},
		{(*pb.EventResponse)(nil), 108},
	}
	for _, tt := range tests {
		if got := messageID(tt.msg); got != tt.want {
			t.Errorf("%s: id %d, want %d", tt.msg.ProtoReflect().Descriptor().Name(), got, tt.want)
		}
	}
}

// Cases follow aioesphomeapi/object_id.py, compute_object_id.
func TestComputeObjectID(t *testing.T) {
	tests := map[string]string{
		"Status":           "status",
		"Display language": "display_language",
		"Heartbeat":        "heartbeat",
		"Temp-Sensor_2":    "temp-sensor_2",
		"Città (cucina)":   "citt___cucina_",
		"":                 "",
	}
	for name, want := range tests {
		if got := computeObjectID(name); got != want {
			t.Errorf("computeObjectID(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEntityFromMessageComputesMissingObjectID(t *testing.T) {
	e, ok := entityFromMessage(&pb.ListEntitiesButtonResponse{Key: 3, Name: "Heartbeat"})
	if !ok || e.Kind != KindButton || e.ObjectID != "heartbeat" || e.Key != 3 {
		t.Fatalf("got %+v, %v", e, ok)
	}
}
