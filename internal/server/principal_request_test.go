package server

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// gobfd-qj0.8.2.3.2: debug handlers require a principal; with mutating
// identities set, only listed mTLS identities pass.
func TestRequirePrincipal(t *testing.T) {
	t.Parallel()

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	tlsFor := func(uri string) *tls.ConnectionState {
		u, err := url.Parse(uri)
		if err != nil {
			t.Fatalf("parse %s: %v", uri, err)
		}
		return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{u}}}}}
	}
	policy := MutationPolicy{AllowAnonymous: true, MutatingIdentities: []string{"spiffe://gobfd.test/ops"}}
	for _, tc := range []struct {
		name string
		tls  *tls.ConnectionState
		want int
	}{
		{"anonymous", nil, http.StatusForbidden},
		{
			"verified certificate without principal",
			&tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}},
			http.StatusForbidden,
		},
		{"listed identity", tlsFor("spiffe://gobfd.test/ops"), http.StatusOK},
		{"unlisted identity", tlsFor("spiffe://gobfd.test/monitor"), http.StatusForbidden},
		{"TLS URI cannot impersonate Unix UID", tlsFor("uid:1000"), http.StatusForbidden},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/flightrecorder", nil)
		req.TLS = tc.tls
		rec := httptest.NewRecorder()
		RequirePrincipal(policy, ok).ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}
