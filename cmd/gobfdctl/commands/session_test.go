package commands

import (
	"errors"
	"testing"
	"time"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestBuildAddSessionRequestWithAuth(t *testing.T) {
	req, err := buildAddSessionRequest(addSessionOptions{
		peer:       "192.0.2.1",
		local:      "192.0.2.2",
		iface:      "eth0",
		sessType:   "multi-hop",
		txInterval: 100 * time.Millisecond,
		rxInterval: 200 * time.Millisecond,
		detectMult: 5,
		authType:   "meticulous-keyed-sha1",
		authKeyID:  7,
		authSecret: "api-auth-secret",
	})
	if err != nil {
		t.Fatalf("buildAddSessionRequest: %v", err)
	}

	if req.GetType() != bfdv1.SessionType_SESSION_TYPE_MULTI_HOP {
		t.Fatalf("Type = %s, want MULTI_HOP", req.GetType())
	}
	if req.GetAuthType() != bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_SHA1 {
		t.Fatalf("AuthType = %s, want METICULOUS_KEYED_SHA1", req.GetAuthType())
	}
	if req.GetAuthKeyId() != 7 {
		t.Fatalf("AuthKeyId = %d, want 7", req.GetAuthKeyId())
	}
	if string(req.GetAuthSecret()) != "api-auth-secret" {
		t.Fatalf("AuthSecret = %q, want api-auth-secret", string(req.GetAuthSecret()))
	}
}

func TestBuildAddSessionRequestAuthValidation(t *testing.T) {
	tests := []struct {
		name    string
		opts    addSessionOptions
		wantErr error
	}{
		{
			name: "secret without auth type",
			opts: addSessionOptions{
				peer:       "192.0.2.1",
				sessType:   "single-hop",
				txInterval: time.Second,
				rxInterval: time.Second,
				detectMult: 3,
				authSecret: "secret",
			},
			wantErr: errAuthKeyMaterialWithoutType,
		},
		{
			name: "auth type without secret",
			opts: addSessionOptions{
				peer:       "192.0.2.1",
				sessType:   "single-hop",
				txInterval: time.Second,
				rxInterval: time.Second,
				detectMult: 3,
				authType:   "keyed-sha1",
			},
			wantErr: errAuthSecretRequired,
		},
		{
			name: "unknown auth type",
			opts: addSessionOptions{
				peer:       "192.0.2.1",
				sessType:   "single-hop",
				txInterval: time.Second,
				rxInterval: time.Second,
				detectMult: 3,
				authType:   "rot13",
				authSecret: "secret",
			},
			wantErr: errUnknownAuthType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildAddSessionRequest(tt.opts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
