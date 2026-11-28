package server_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestAPIPaddedPDUFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		message protoreflect.MessageDescriptor
		number  protoreflect.FieldNumber
	}{
		{(&bfdv1.AddSessionRequest{}).ProtoReflect().Descriptor(), 11},
		{(&bfdv1.BfdSession{}).ProtoReflect().Descriptor(), 26},
	}

	for _, tt := range tests {
		t.Run(string(tt.message.Name()), func(t *testing.T) {
			t.Parallel()

			field := tt.message.Fields().ByName("padded_pdu_size")
			if field == nil {
				t.Fatal("padded_pdu_size field is missing")
			}
			if got := field.Number(); got != tt.number {
				t.Errorf("padded_pdu_size field number = %d, want %d", got, tt.number)
			}
			if got := field.Kind(); got != protoreflect.Uint32Kind {
				t.Fatalf("padded_pdu_size kind = %s, want uint32", got)
			}
			if got := field.Default().Uint(); got != 0 {
				t.Errorf("padded_pdu_size default = %d, want 0", got)
			}
		})
	}
}
