package netio

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestNetworkManagerControllerLookupHonorsContext(t *testing.T) {
	t.Parallel()
	const uuid = "d9cbb60e-8c3d-4d01-8f22-111111111111"
	const parent = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/2")
	for _, mode := range []string{
		"reply", "remote error", "wrong type", "string path", "missing object",
		"empty reply", "extra value", "cancel", "deadline",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			reached := make(chan struct{}, 1)
			client := networkManagerWireClient(t, func(request *dbus.Message) *dbus.Message {
				if request.Headers[dbus.FieldDestination].Value() != "org.freedesktop.NetworkManager" ||
					request.Headers[dbus.FieldPath].Value() != dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings") ||
					request.Headers[dbus.FieldInterface].Value() != "org.freedesktop.NetworkManager.Settings" ||
					request.Headers[dbus.FieldMember].Value() != "GetConnectionByUuid" ||
					!reflect.DeepEqual(request.Body, []any{uuid}) {
					t.Errorf("unexpected UUID lookup: %#v", request)
				}
				reached <- struct{}{}
				if mode == "cancel" || mode == "deadline" {
					return nil
				}
				if mode == "remote error" {
					return &dbus.Message{
						Type: dbus.TypeError,
						Headers: map[dbus.HeaderField]dbus.Variant{
							dbus.FieldErrorName: dbus.MakeVariant("org.freedesktop.NetworkManager.Settings.PermissionDenied"),
						}, Body: []any{"private rejection"},
					}
				}
				var value any = parent
				if mode == "wrong type" {
					value = int64(7)
				}
				if mode == "string path" {
					value = string(parent)
				}
				if mode == "missing object" {
					value = dbus.ObjectPath("/")
				}
				if mode == "empty reply" {
					return &dbus.Message{Type: dbus.TypeMethodReply}
				}
				if mode == "extra value" {
					return &dbus.Message{Type: dbus.TypeMethodReply, Body: []any{parent, parent}}
				}
				return &dbus.Message{Type: dbus.TypeMethodReply, Body: []any{value}}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
			defer cancel()
			type lookupResult struct {
				path dbus.ObjectPath
				err  error
			}
			results := make(chan lookupResult, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				path, err := client.ConnectionByUUID(ctx, uuid)
				results <- lookupResult{path: path, err: err}
			}()
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("UUID lookup did not stop after close")
				}
			})
			select {
			case <-reached:
			case <-time.After(time.Second):
				t.Fatal("UUID lookup did not reach its peer")
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case result := <-results:
				switch mode {
				case "reply":
					if result.err != nil || result.path != parent {
						t.Errorf("UUID lookup returned %q, %v", result.path, result.err)
					}
				case "remote error":
					remote, ok := errors.AsType[dbus.Error](result.err)
					if !ok || remote.Name != "org.freedesktop.NetworkManager.Settings.PermissionDenied" {
						t.Errorf("UUID permission error was lost: %v", result.err)
					}
				case "wrong type", "string path", "missing object", "empty reply", "extra value":
					if result.err == nil || errors.Is(result.err, context.DeadlineExceeded) {
						t.Errorf("UUID response type was not rejected: %v", result.err)
					}
				case "cancel":
					if !errors.Is(result.err, context.Canceled) {
						t.Errorf("UUID lookup lost cancellation: %v", result.err)
					}
				case "deadline":
					if !errors.Is(result.err, context.DeadlineExceeded) {
						t.Errorf("UUID lookup lost deadline: %v", result.err)
					}
				}
			case <-time.After(time.Second):
				t.Error("UUID lookup ignored context")
			}
		})
	}
}
