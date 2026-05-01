package netio

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ovn-org/libovsdb/ovsdb"
)

func TestOVSDBLAGBackendDelegatesBondInterfaceOperations(t *testing.T) {
	t.Parallel()

	client := &recordingOVSDBLAGClient{}
	backend := NewOVSDBLAGBackend(OVSDBLAGBackendConfig{Client: client})

	if err := backend.RemoveMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if err := backend.AddMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	want := []ovsdbClientCall{
		{op: "remove", bond: "bond0", iface: "eth0"},
		{op: "add", bond: "bond0", iface: "eth0"},
	}
	if !reflect.DeepEqual(client.calls, want) {
		t.Fatalf("OVSDB client calls = %#v, want %#v", client.calls, want)
	}
}

func TestOVSDBLAGBackendRejectsUnsafeInterfaceNames(t *testing.T) {
	t.Parallel()

	backend := NewOVSDBLAGBackend(OVSDBLAGBackendConfig{
		Client: &recordingOVSDBLAGClient{},
	})

	err := backend.RemoveMember(context.Background(), "bond0/../../x", "eth0")
	if !errors.Is(err, ErrInvalidLAGInterfaceName) {
		t.Fatalf("RemoveMember error = %v, want %v", err, ErrInvalidLAGInterfaceName)
	}
}

func TestLibOVSDBLAGClientRemoveDeletesInterfaceUUIDFromPort(t *testing.T) {
	t.Parallel()

	tx := &scriptedOVSDBTransactor{
		results: [][]ovsdb.OperationResult{
			{{Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: "11111111-1111-1111-1111-111111111111"}}}}},
			{{Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: "22222222-2222-2222-2222-222222222222"}}}}},
			{{Count: 1}},
		},
	}
	client := testLibOVSDBLAGClient(tx)

	if err := client.RemoveBondInterface(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveBondInterface: %v", err)
	}
	if !tx.closed {
		t.Fatal("RemoveBondInterface did not disconnect OVSDB client")
	}

	assertTransactTables(t, tx.operations, []string{ovsdbPortTable, ovsdbInterfaceTable, ovsdbPortTable})
	mutate := tx.operations[2][0]
	if mutate.Op != ovsdb.OperationMutate {
		t.Fatalf("remove operation = %q, want %q", mutate.Op, ovsdb.OperationMutate)
	}
	if got := mutate.Mutations[0].Mutator; got != ovsdb.MutateOperationDelete {
		t.Fatalf("remove mutator = %q, want %q", got, ovsdb.MutateOperationDelete)
	}
}

func TestLibOVSDBLAGClientAddInsertsMissingInterfaceAndMutatesPort(t *testing.T) {
	t.Parallel()

	tx := &scriptedOVSDBTransactor{
		results: [][]ovsdb.OperationResult{
			{{Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: "11111111-1111-1111-1111-111111111111"}}}}},
			{{Rows: nil}},
			{{UUID: ovsdb.UUID{GoUUID: "33333333-3333-3333-3333-333333333333"}}, {Count: 1}},
		},
	}
	client := testLibOVSDBLAGClient(tx)

	if err := client.AddBondInterface(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddBondInterface: %v", err)
	}
	if !tx.closed {
		t.Fatal("AddBondInterface did not disconnect OVSDB client")
	}

	assertTransactTables(t, tx.operations, []string{
		ovsdbPortTable,
		ovsdbInterfaceTable,
		ovsdbInterfaceTable,
		ovsdbPortTable,
	})
	insert := tx.operations[2][0]
	if insert.Op != ovsdb.OperationInsert {
		t.Fatalf("add first operation = %q, want %q", insert.Op, ovsdb.OperationInsert)
	}
	if insert.UUIDName != ovsdbMemberInterfaceRef {
		t.Fatalf("insert UUIDName = %q, want %q", insert.UUIDName, ovsdbMemberInterfaceRef)
	}
	mutate := tx.operations[2][1]
	if got := mutate.Mutations[0].Mutator; got != ovsdb.MutateOperationInsert {
		t.Fatalf("add mutator = %q, want %q", got, ovsdb.MutateOperationInsert)
	}
}

func TestLibOVSDBLAGClientErrorsWhenPortMissing(t *testing.T) {
	t.Parallel()

	tx := &scriptedOVSDBTransactor{
		results: [][]ovsdb.OperationResult{
			{{Rows: nil}},
		},
	}
	client := testLibOVSDBLAGClient(tx)

	err := client.AddBondInterface(context.Background(), "bond0", "eth0")
	if !errors.Is(err, ErrOVSDBPortNotFound) {
		t.Fatalf("AddBondInterface error = %v, want %v", err, ErrOVSDBPortNotFound)
	}
	if !tx.closed {
		t.Fatal("AddBondInterface did not disconnect OVSDB client")
	}
}

type ovsdbClientCall struct {
	op    string
	bond  string
	iface string
}

type recordingOVSDBLAGClient struct {
	calls []ovsdbClientCall
}

func (r *recordingOVSDBLAGClient) RemoveBondInterface(_ context.Context, bond string, iface string) error {
	r.calls = append(r.calls, ovsdbClientCall{op: "remove", bond: bond, iface: iface})
	return nil
}

func (r *recordingOVSDBLAGClient) AddBondInterface(_ context.Context, bond string, iface string) error {
	r.calls = append(r.calls, ovsdbClientCall{op: "add", bond: bond, iface: iface})
	return nil
}

type scriptedOVSDBTransactor struct {
	operations [][]ovsdb.Operation
	results    [][]ovsdb.OperationResult
	closed     bool
}

func (s *scriptedOVSDBTransactor) Transact(
	_ context.Context,
	ops ...ovsdb.Operation,
) ([]ovsdb.OperationResult, error) {
	s.operations = append(s.operations, append([]ovsdb.Operation(nil), ops...))
	if len(s.results) == 0 {
		return nil, errors.New("unexpected OVSDB transaction")
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result, nil
}

func (s *scriptedOVSDBTransactor) Disconnect() {
	s.closed = true
}

func testLibOVSDBLAGClient(tx *scriptedOVSDBTransactor) *libOVSDBLAGClient {
	return &libOVSDBLAGClient{
		newTransactor: func(context.Context) (ovsdbTransactor, error) {
			return tx, nil
		},
	}
}

func assertTransactTables(t *testing.T, got [][]ovsdb.Operation, want []string) {
	t.Helper()

	var tables []string
	for _, transaction := range got {
		for _, op := range transaction {
			tables = append(tables, op.Table)
		}
	}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("OVSDB operation tables = %#v, want %#v", tables, want)
	}
}
