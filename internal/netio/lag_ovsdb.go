package netio

import (
	"context"
	"errors"
	"fmt"

	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
)

const (
	defaultOVSDBEndpoint    = "unix:/var/run/openvswitch/db.sock"
	ovsdbDatabaseName       = "Open_vSwitch"
	ovsdbPortTable          = "Port"
	ovsdbInterfaceTable     = "Interface"
	ovsdbMemberInterfaceRef = "gobfd_member_interface"
)

var (
	ErrOVSDBPortNotFound       = errors.New("OVSDB port not found")
	ErrOVSDBInterfaceAmbiguity = errors.New("OVSDB interface query returned multiple rows")
	ErrOVSDBPortNotMutated     = errors.New("OVSDB port interfaces set was not mutated")
	ErrOVSDBRowMissingUUID     = errors.New("OVSDB row is missing _uuid")
	ErrOVSDBRowInvalidUUID     = errors.New("OVSDB row _uuid has unsupported type")
)

// OVSDBLAGBackendConfig configures native OVSDB LAG enforcement.
type OVSDBLAGBackendConfig struct {
	// Endpoint is the OVSDB endpoint. Empty means unix:/var/run/openvswitch/db.sock.
	Endpoint string

	// Client applies high-level bond interface operations. Empty uses libovsdb.
	Client BondLAGClient
}

// OVSDBLAGBackend applies member changes to an existing OVS bond port via OVSDB.
type OVSDBLAGBackend struct {
	client BondLAGClient
}

// NewOVSDBLAGBackend creates a native OVSDB backend for OVS bonded ports.
func NewOVSDBLAGBackend(cfg OVSDBLAGBackendConfig) *OVSDBLAGBackend {
	ovsClient := cfg.Client
	if ovsClient == nil {
		endpoint := cfg.Endpoint
		if endpoint == "" {
			endpoint = defaultOVSDBEndpoint
		}
		ovsClient = newLibOVSDBLAGClient(endpoint)
	}
	return &OVSDBLAGBackend{client: ovsClient}
}

// RemoveMember removes memberInterface from an OVS bond port through OVSDB.
func (b *OVSDBLAGBackend) RemoveMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	if err := validateLAGInterfaceName(lagInterface); err != nil {
		return fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if err := validateLAGInterfaceName(memberInterface); err != nil {
		return fmt.Errorf("member interface %q: %w", memberInterface, err)
	}
	if err := b.client.RemoveBondInterface(ctx, lagInterface, memberInterface); err != nil {
		return fmt.Errorf("ovsdb lag backend: %w", err)
	}
	return nil
}

// AddMember adds memberInterface to an existing OVS bond port through OVSDB.
func (b *OVSDBLAGBackend) AddMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	if err := validateLAGInterfaceName(lagInterface); err != nil {
		return fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if err := validateLAGInterfaceName(memberInterface); err != nil {
		return fmt.Errorf("member interface %q: %w", memberInterface, err)
	}
	if err := b.client.AddBondInterface(ctx, lagInterface, memberInterface); err != nil {
		return fmt.Errorf("ovsdb lag backend: %w", err)
	}
	return nil
}

type ovsdbTransactor interface {
	Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error)
	Disconnect()
}

type ovsdbTransactorFactory func(context.Context) (ovsdbTransactor, error)

type libOVSDBLAGClient struct {
	newTransactor ovsdbTransactorFactory
}

func newLibOVSDBLAGClient(endpoint string) *libOVSDBLAGClient {
	return &libOVSDBLAGClient{
		newTransactor: func(ctx context.Context) (ovsdbTransactor, error) {
			dbModel, err := newOpenVSwitchClientDBModel()
			if err != nil {
				return nil, err
			}
			ovs, err := client.NewOVSDBClient(dbModel, client.WithEndpoint(endpoint))
			if err != nil {
				return nil, err
			}
			if err := ovs.Connect(ctx); err != nil {
				return nil, err
			}
			return ovs, nil
		},
	}
}

func (c *libOVSDBLAGClient) RemoveBondInterface(ctx context.Context, bond string, iface string) error {
	return c.withTransactor(ctx, func(tx ovsdbTransactor) error {
		if err := c.requirePort(ctx, tx, bond); err != nil {
			return err
		}
		ifaceUUID, found, err := c.interfaceUUID(ctx, tx, iface)
		if err != nil || !found {
			return err
		}
		return c.mutatePortInterfaces(ctx, tx, bond, ovsdb.MutateOperationDelete, ifaceUUID)
	})
}

func (c *libOVSDBLAGClient) AddBondInterface(ctx context.Context, bond string, iface string) error {
	return c.withTransactor(ctx, func(tx ovsdbTransactor) error {
		if err := c.requirePort(ctx, tx, bond); err != nil {
			return err
		}
		ifaceUUID, found, err := c.interfaceUUID(ctx, tx, iface)
		if err != nil {
			return err
		}
		if found {
			return c.mutatePortInterfaces(ctx, tx, bond, ovsdb.MutateOperationInsert, ifaceUUID)
		}

		ops, err := buildInsertInterfaceAndMutatePortOps(bond, iface)
		if err != nil {
			return err
		}
		results, err := tx.Transact(ctx, ops...)
		if err != nil {
			return err
		}
		if _, err := ovsdb.CheckOperationResults(results, ops); err != nil {
			return err
		}
		return checkPortMutationCount(results[len(results)-1])
	})
}

func (c *libOVSDBLAGClient) withTransactor(
	ctx context.Context,
	fn func(ovsdbTransactor) error,
) error {
	tx, err := c.newTransactor(ctx)
	if err != nil {
		return err
	}
	defer tx.Disconnect()
	return fn(tx)
}

func (c *libOVSDBLAGClient) requirePort(ctx context.Context, tx ovsdbTransactor, name string) error {
	results, err := runCheckedOVSDBOperations(ctx, tx, ovsdb.Operation{
		Op:      ovsdb.OperationSelect,
		Table:   ovsdbPortTable,
		Columns: []string{"_uuid"},
		Where: []ovsdb.Condition{
			ovsdb.NewCondition("name", ovsdb.ConditionEqual, name),
		},
	})
	if err != nil {
		return err
	}
	if len(results[0].Rows) == 0 {
		return fmt.Errorf("%s: %w", name, ErrOVSDBPortNotFound)
	}
	return nil
}

func (c *libOVSDBLAGClient) interfaceUUID(
	ctx context.Context,
	tx ovsdbTransactor,
	name string,
) (string, bool, error) {
	results, err := runCheckedOVSDBOperations(ctx, tx, ovsdb.Operation{
		Op:      ovsdb.OperationSelect,
		Table:   ovsdbInterfaceTable,
		Columns: []string{"_uuid"},
		Where: []ovsdb.Condition{
			ovsdb.NewCondition("name", ovsdb.ConditionEqual, name),
		},
	})
	if err != nil {
		return "", false, err
	}
	rows := results[0].Rows
	switch len(rows) {
	case 0:
		return "", false, nil
	case 1:
		uuid, err := rowUUID(rows[0])
		return uuid, true, err
	default:
		return "", false, fmt.Errorf("%s: %w", name, ErrOVSDBInterfaceAmbiguity)
	}
}

func (c *libOVSDBLAGClient) mutatePortInterfaces(
	ctx context.Context,
	tx ovsdbTransactor,
	bond string,
	mutator ovsdb.Mutator,
	ifaceUUID string,
) error {
	op, err := buildMutatePortInterfacesOp(bond, mutator, ifaceUUID)
	if err != nil {
		return err
	}
	results, err := runCheckedOVSDBOperations(ctx, tx, op)
	if err != nil {
		return err
	}
	return checkPortMutationCount(results[0])
}

func runCheckedOVSDBOperations(
	ctx context.Context,
	tx ovsdbTransactor,
	ops ...ovsdb.Operation,
) ([]ovsdb.OperationResult, error) {
	results, err := tx.Transact(ctx, ops...)
	if err != nil {
		return nil, err
	}
	if _, err := ovsdb.CheckOperationResults(results, ops); err != nil {
		return nil, err
	}
	return results, nil
}

func buildInsertInterfaceAndMutatePortOps(bond string, iface string) ([]ovsdb.Operation, error) {
	mutateOp, err := buildMutatePortInterfacesOp(
		bond, ovsdb.MutateOperationInsert, ovsdbMemberInterfaceRef)
	if err != nil {
		return nil, err
	}
	return []ovsdb.Operation{
		{
			Op:       ovsdb.OperationInsert,
			Table:    ovsdbInterfaceTable,
			Row:      ovsdb.Row{"name": iface},
			UUIDName: ovsdbMemberInterfaceRef,
		},
		mutateOp,
	}, nil
}

func buildMutatePortInterfacesOp(
	bond string,
	mutator ovsdb.Mutator,
	ifaceUUID string,
) (ovsdb.Operation, error) {
	ifaceSet, err := ovsdb.NewOvsSet([]ovsdb.UUID{{GoUUID: ifaceUUID}})
	if err != nil {
		return ovsdb.Operation{}, err
	}
	return ovsdb.Operation{
		Op:    ovsdb.OperationMutate,
		Table: ovsdbPortTable,
		Where: []ovsdb.Condition{
			ovsdb.NewCondition("name", ovsdb.ConditionEqual, bond),
		},
		Mutations: []ovsdb.Mutation{
			*ovsdb.NewMutation("interfaces", mutator, ifaceSet),
		},
	}, nil
}

func rowUUID(row ovsdb.Row) (string, error) {
	value, ok := row["_uuid"]
	if !ok {
		return "", ErrOVSDBRowMissingUUID
	}
	switch uuid := value.(type) {
	case ovsdb.UUID:
		return uuid.GoUUID, nil
	case string:
		return uuid, nil
	default:
		return "", fmt.Errorf("%T: %w", value, ErrOVSDBRowInvalidUUID)
	}
}

func checkPortMutationCount(result ovsdb.OperationResult) error {
	if result.Count == 0 {
		return ErrOVSDBPortNotMutated
	}
	return nil
}

type ovsdbPortModel struct {
	UUID       string   `ovsdb:"_uuid"`
	Name       string   `ovsdb:"name"`
	Interfaces []string `ovsdb:"interfaces"`
}

type ovsdbInterfaceModel struct {
	UUID string `ovsdb:"_uuid"`
	Name string `ovsdb:"name"`
}

func newOpenVSwitchClientDBModel() (model.ClientDBModel, error) {
	return model.NewClientDBModel(ovsdbDatabaseName, map[string]model.Model{
		ovsdbPortTable:      &ovsdbPortModel{},
		ovsdbInterfaceTable: &ovsdbInterfaceModel{},
	})
}
