package server_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestPaddedPDULegacyBinaryCompatibility(t *testing.T) {
	t.Parallel()
	file := protodesc.ToFileDescriptorProto((&bfdv1.AddSessionRequest{}).ProtoReflect().Descriptor().ParentFile())
	for _, message := range file.GetMessageType() {
		if message.GetName() == "AddSessionRequest" || message.GetName() == "BfdSession" {
			message.Field = slices.DeleteFunc(message.GetField(), func(field *descriptorpb.FieldDescriptorProto) bool {
				return field.GetName() == "padded_pdu_size"
			})
		}
	}
	legacyFile, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []proto.Message{
		&bfdv1.AddSessionRequest{PeerAddress: "192.0.2.1", PaddedPduSize: 128},
		&bfdv1.BfdSession{PeerAddress: "192.0.2.1", PaddedPduSize: 128},
	} {
		name := message.ProtoReflect().Descriptor().Name()
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			legacyDescriptor := legacyFile.Messages().ByName(name)
			if legacyDescriptor == nil || legacyDescriptor.Fields().ByName("padded_pdu_size") != nil {
				t.Fatal("legacy descriptor must omit padded_pdu_size")
			}
			wire, err := proto.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			legacy := dynamicpb.NewMessage(legacyDescriptor)
			if unmarshalErr := proto.Unmarshal(wire, legacy); unmarshalErr != nil {
				t.Fatal(unmarshalErr)
			}
			if got := legacy.Get(legacyDescriptor.Fields().ByName("peer_address")).String(); got != "192.0.2.1" {
				t.Fatalf("legacy peer = %q, want 192.0.2.1", got)
			}
			if len(legacy.GetUnknown()) == 0 {
				t.Fatal("legacy decoder discarded the new field")
			}
			wire, err = proto.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip := message.ProtoReflect().Type().New().Interface()
			if unmarshalErr := proto.Unmarshal(wire, roundTrip); unmarshalErr != nil {
				t.Fatal(unmarshalErr)
			}
			if !proto.Equal(roundTrip, message) {
				t.Fatalf("legacy binary round trip = %v, want %v", roundTrip, message)
			}
		})
	}
}
