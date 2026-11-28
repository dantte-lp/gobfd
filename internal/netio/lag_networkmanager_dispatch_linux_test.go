//go:build linux

package netio

import (
	"context"
	"log/slog"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestNetworkManagerStalledPropertyBoundsManagerShutdown(t *testing.T) {
	t.Parallel()

	for _, cancelDispatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "actuator deadline", true: "dispatch cancellation"}[cancelDispatch], func(t *testing.T) {
			t.Parallel()

			requested := make(chan struct{}, 1)
			client := networkManagerWireClient(t, func(request *dbus.Message) *dbus.Message {
				switch request.Headers[dbus.FieldMember].Value() {
				case "GetDeviceByIpIface":
					if !reflect.DeepEqual(request.Body, []any{"eth0"}) {
						t.Errorf("device lookup arguments = %#v", request.Body)
					}
					return &dbus.Message{Type: dbus.TypeMethodReply, Body: []any{fakeDevicePath}}
				case "Get":
					if request.Headers[dbus.FieldPath].Value() != fakeDevicePath ||
						!reflect.DeepEqual(request.Body, []any{networkManagerDeviceInterface, "ActiveConnection"}) {
						t.Errorf("unexpected stalled property request: %#v", request)
					}
					requested <- struct{}{}
					return nil
				default:
					t.Errorf("unexpected NetworkManager call: %#v", request)
					return nil
				}
			})
			backend := NewNetworkManagerLAGBackend(NetworkManagerLAGBackendConfig{Client: &dbusNetworkManagerLAGClient{
				newDBus: func(context.Context) (networkManagerDBus, error) { return client, nil },
				cache:   make(map[networkManagerMemberKey]dbus.ObjectPath),
			}})
			logger := slog.New(slog.DiscardHandler)
			actuator, err := NewLAGActuator(LAGActuatorConfig{
				Mode: LAGActuatorModeEnforce, Backend: LAGActuatorBackendNetworkManager,
				OwnerPolicy: LAGOwnerPolicyNetworkManagerDBus, UpAction: LAGActuatorActionAddMember,
			}, backend, logger)
			if err != nil {
				t.Fatal(err)
			}
			manager := bfd.NewManager(logger, bfd.WithMicroBFDActuator(actuator))
			ctx, cancel := context.WithCancel(t.Context())
			dispatched := make(chan struct{})
			go func() {
				defer close(dispatched)
				manager.RunDispatch(ctx)
			}()
			t.Cleanup(func() {
				cancel()
				if closeErr := client.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				manager.Close()
				select {
				case <-dispatched:
				case <-time.After(time.Second):
					t.Error("manager dispatch did not stop")
				}
			})
			peer, local := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")
			if _, groupErr := manager.CreateMicroBFDGroup(bfd.MicroBFDConfig{
				LAGInterface: "bond0", MemberLinks: []string{"eth0", "eth1"}, PeerAddr: peer, LocalAddr: local,
				DesiredMinTxInterval: time.Second, RequiredMinRxInterval: 30 * time.Second,
				DetectMultiplier: 3, MinActiveLinks: 1,
			}); groupErr != nil {
				t.Fatal(groupErr)
			}
			session, err := manager.CreateSession(ctx, bfd.SessionConfig{
				PeerAddr: peer, LocalAddr: local, Interface: "eth0", Type: bfd.SessionTypeMicroBFD,
				Role: bfd.RolePassive, DesiredMinTxInterval: time.Second,
				RequiredMinRxInterval: 30 * time.Second, DetectMultiplier: 3,
			}, bfd.NonOwningSenderLeaseFactory(discardTestSender{}))
			if err != nil {
				t.Fatal(err)
			}
			session.RecvPacket(&bfd.ControlPacket{
				Version: bfd.Version, State: bfd.StateInit, DetectMult: 3, MyDiscriminator: 42,
				YourDiscriminator: session.LocalDiscriminator(), DesiredMinTxInterval: 1_000_000,
				RequiredMinRxInterval: 30_000_000,
			})
			select {
			case <-requested:
			case <-time.After(time.Second):
				t.Fatal("real manager actuator did not reach the stalled property")
			}
			if cancelDispatch {
				cancel()
			}
			closed := make(chan struct{})
			go func() {
				manager.Close()
				close(closed)
			}()
			select {
			case <-closed:
				if client.conn.Connected() {
					t.Error("actuator retained its D-Bus connection after returning")
				}
			case <-time.After(6 * time.Second):
				t.Error("manager shutdown waited beyond the actuator context bound")
			}
		})
	}
}
