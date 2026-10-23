package espapi

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi/pb"
)

// handledMessages lists every message type this client sends or decodes.
// Their wire IDs come from the `(id)` option in api.proto, so no ID is
// written by hand. Messages of other types are skipped, as aioesphomeapi
// does (connection.py, "Skipping unknown message type").
var handledMessages = []proto.Message{
	(*pb.HelloRequest)(nil),
	(*pb.HelloResponse)(nil),
	(*pb.DisconnectRequest)(nil),
	(*pb.DisconnectResponse)(nil),
	(*pb.PingRequest)(nil),
	(*pb.PingResponse)(nil),
	(*pb.ListEntitiesRequest)(nil),
	(*pb.ListEntitiesDoneResponse)(nil),
	(*pb.ListEntitiesSelectResponse)(nil),
	(*pb.ListEntitiesButtonResponse)(nil),
	(*pb.ListEntitiesTextSensorResponse)(nil),
	(*pb.ListEntitiesServicesResponse)(nil),
	(*pb.SubscribeStatesRequest)(nil),
	(*pb.SelectStateResponse)(nil),
	(*pb.TextSensorStateResponse)(nil),
	(*pb.SelectCommandRequest)(nil),
	(*pb.ButtonCommandRequest)(nil),
	(*pb.ExecuteServiceRequest)(nil),
}

// messageTable maps wire IDs to message types and back. It is built once
// and never modified.
type messageTable struct {
	byID   map[uint32]protoreflect.MessageType
	byName map[protoreflect.FullName]uint32
}

var messages = sync.OnceValue(func() *messageTable {
	t := &messageTable{
		byID:   make(map[uint32]protoreflect.MessageType, len(handledMessages)),
		byName: make(map[protoreflect.FullName]uint32, len(handledMessages)),
	}
	for _, m := range handledMessages {
		mt := m.ProtoReflect().Type()
		desc := mt.Descriptor()
		id := proto.GetExtension(desc.Options(), pb.E_Id).(uint32)
		if id == 0 {
			// Only possible if api.proto changes; TestHandledMessagesHaveIDs
			// catches it.
			continue
		}
		t.byID[id] = mt
		t.byName[desc.FullName()] = id
	}
	return t
})

// messageID returns the wire ID of a handled message.
func messageID(m proto.Message) uint32 {
	return messages().byName[m.ProtoReflect().Descriptor().FullName()]
}

// EntityKind is the type of an entity on the device.
type EntityKind int

// Entity kinds this client understands.
const (
	KindSelect EntityKind = iota + 1
	KindButton
	KindTextSensor
	// KindAction is a user-defined API action (`api: actions:`), called
	// with ExecuteAction. Its ObjectID is the action name.
	KindAction
)

func (k EntityKind) String() string {
	switch k {
	case KindSelect:
		return "select"
	case KindButton:
		return "button"
	case KindTextSensor:
		return "text_sensor"
	case KindAction:
		return "action"
	default:
		return fmt.Sprintf("EntityKind(%d)", int(k))
	}
}

// Entity is one entity reported by ListEntities. Commands address entities
// by Key.
type Entity struct {
	Kind     EntityKind
	Key      uint32
	ObjectID string
	Name     string
	Options  []string    // select options
	Args     []ActionArg // action arguments, in call order
}

// ActionArg is one argument of an action.
type ActionArg struct {
	Name string
	Type pb.ServiceArgType
}

// Entities is the result of ListEntities.
type Entities []Entity

// Find returns the entity of the given kind and object ID.
func (e Entities) Find(kind EntityKind, objectID string) (Entity, bool) {
	for _, ent := range e {
		if ent.Kind == kind && ent.ObjectID == objectID {
			return ent, true
		}
	}
	return Entity{}, false
}

// State is a state update for a select or text sensor entity.
type State struct {
	Key   uint32
	Value string
	// Missing is true when the entity has no valid state yet.
	Missing bool
}

// entityFromMessage converts a ListEntities*Response into an Entity.
func entityFromMessage(m proto.Message) (Entity, bool) {
	var e Entity
	switch r := m.(type) {
	case *pb.ListEntitiesSelectResponse:
		e = Entity{Kind: KindSelect, Key: r.GetKey(), ObjectID: r.GetObjectId(), Name: r.GetName(), Options: r.GetOptions()}
	case *pb.ListEntitiesButtonResponse:
		e = Entity{Kind: KindButton, Key: r.GetKey(), ObjectID: r.GetObjectId(), Name: r.GetName()}
	case *pb.ListEntitiesTextSensorResponse:
		e = Entity{Kind: KindTextSensor, Key: r.GetKey(), ObjectID: r.GetObjectId(), Name: r.GetName()}
	case *pb.ListEntitiesServicesResponse:
		e = Entity{Kind: KindAction, Key: r.GetKey(), ObjectID: r.GetName(), Name: r.GetName()}
		for _, a := range r.GetArgs() {
			e.Args = append(e.Args, ActionArg{Name: a.GetName(), Type: a.GetType()})
		}
	default:
		return Entity{}, false
	}
	if e.ObjectID == "" {
		e.ObjectID = computeObjectID(e.Name)
	}
	return e, true
}

// computeObjectID derives an object ID from an entity name, for devices that
// no longer send object_id ("planned for removal" in ESPHome). It is a port
// of aioesphomeapi/object_id.py, compute_object_id: snake_case, then every
// character outside [a-z0-9_-] becomes '_'.
func computeObjectID(name string) string {
	b := make([]byte, 0, len(name))
	for _, r := range name {
		switch {
		case r == ' ':
			b = append(b, '_')
		case r >= 'A' && r <= 'Z':
			b = append(b, byte(r-'A'+'a'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b = append(b, byte(r))
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}
