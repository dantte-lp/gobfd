package clabbootstrap

import (
	"errors"
	"strings"
	"testing"
)

const vendorPackageField = `"Package":"github.com/dantte-lp/gobfd/test/interop-clab"`

const requiredVendorWirePass = `{"Action":"pass",` + vendorPackageField +
	`,"Test":"TestVendorBFD_SessionEstablish/frr"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_SessionEstablish/frr-v6"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_FailureDetection/frr"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_FailureDetection/frr-v6"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_TimerNegotiation/frr"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_TimerNegotiation/frr-v6"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_DetectionTiming/frr"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_DetectionTiming/frr-v6"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_RFC5880_PacketFormat"}` + "\n" +
	`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_AllAvailable"}` + "\n"

const completeVendorWirePass = requiredVendorWirePass + `{"Action":"pass",` + vendorPackageField + "}\n"

func TestValidateVendorTestResultRequiresWireAcceptance(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		output  string
		wantErr bool
	}{
		{name: "complete wire acceptance", output: completeVendorWirePass},
		{
			name:    "missing final package verdict",
			output:  requiredVendorWirePass,
			wantErr: true,
		},
		{
			name: "wire skipped after passing",
			output: completeVendorWirePass + `{"Action":"skip",` + vendorPackageField +
				`,"Test":"TestVendorBFD_SessionEstablish/frr"}` + "\n",
			wantErr: true,
		},
		{
			name: "policy pass alone",
			output: `{"Action":"pass",` + vendorPackageField +
				`,"Test":"TestVendorPeersExcludeRetainedUnavailableNOS"}` + "\n" +
				`{"Action":"pass",` + vendorPackageField + "}\n",
			wantErr: true,
		},
		{
			name: "parent passes with skipped wire leaves",
			output: `{"Action":"skip",` + vendorPackageField +
				`,"Test":"TestVendorBFD_SessionEstablish/frr"}` + "\n" +
				`{"Action":"skip",` + vendorPackageField + `,"Test":"TestVendorBFD_SessionEstablish/frr-v6"}` + "\n" +
				`{"Action":"pass",` + vendorPackageField + `,"Test":"TestVendorBFD_SessionEstablish"}` + "\n" +
				`{"Action":"skip",` + vendorPackageField + `,"Test":"TestVendorBFD_AllAvailable"}` + "\n" +
				`{"Action":"pass",` + vendorPackageField +
				`,"Test":"TestVendorPeersExcludeRetainedUnavailableNOS"}` + "\n" +
				`{"Action":"pass",` + vendorPackageField + "}\n",
			wantErr: true,
		},
		{
			name: "missing IPv6 wire pass",
			output: strings.Replace(completeVendorWirePass,
				`{"Action":"pass",`+vendorPackageField+
					`,"Test":"TestVendorBFD_SessionEstablish/frr-v6"}`+"\n", "", 1),
			wantErr: true,
		},
		{
			name: "failure after required passes",
			output: completeVendorWirePass + `{"Action":"fail",` + vendorPackageField +
				`,"Test":"TestVendorBFD_DetectionTiming/frr"}` + "\n",
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateVendorTestResult(test.output)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateVendorTestResult() error = %v, wantErr %t", err, test.wantErr)
			}
			if test.wantErr && !errors.Is(err, ErrBootstrapFailed) {
				t.Fatalf("wire acceptance error = %v, want ErrBootstrapFailed", err)
			}
		})
	}
}

func TestValidateVendorTestResultRequiresEveryFRRBehavior(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"TestVendorBFD_FailureDetection/frr",
		"TestVendorBFD_FailureDetection/frr-v6",
		"TestVendorBFD_TimerNegotiation/frr",
		"TestVendorBFD_TimerNegotiation/frr-v6",
		"TestVendorBFD_DetectionTiming/frr",
		"TestVendorBFD_DetectionTiming/frr-v6",
		"TestVendorBFD_RFC5880_PacketFormat",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pass := `{"Action":"pass",` + vendorPackageField + `,"Test":"` + name + `"}` + "\n"
			skip := `{"Action":"skip",` + vendorPackageField + `,"Test":"` + name + `"}` + "\n"
			if strings.Count(completeVendorWirePass, pass) != 1 {
				t.Fatal("independent complete fixture must contain exactly one pass for the selected behavior")
			}
			for _, test := range []struct {
				name   string
				output string
			}{
				{name: "omitted", output: strings.Replace(completeVendorWirePass, pass, "", 1)},
				{name: "skipped", output: strings.Replace(completeVendorWirePass, pass, skip, 1)},
				{name: "passed then skipped", output: completeVendorWirePass + skip},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					if err := validateVendorTestResult(test.output); !errors.Is(err, ErrBootstrapFailed) {
						t.Fatalf("incomplete mandatory FRR behavior error = %v, want ErrBootstrapFailed", err)
					}
				})
			}
		})
	}
}

func TestValidateVendorTestResultAllowsOnlyFRRSessionIndependenceSkip(t *testing.T) {
	t.Parallel()
	allowed := requiredVendorWirePass + `{"Action":"skip",` + vendorPackageField +
		`,"Test":"TestVendorBFD_SessionIndependence"}` + "\n" +
		`{"Action":"pass",` + vendorPackageField + "}\n"
	if err := validateVendorTestResult(allowed); err != nil {
		t.Fatalf("documented FRR-only SessionIndependence skip was refused: %v", err)
	}
	for _, test := range []struct {
		name  string
		event string
	}{
		{
			name: "policy test",
			event: `{"Action":"skip",` + vendorPackageField +
				`,"Test":"TestVendorPeersExcludeRetainedUnavailableNOS"}`,
		},
		{name: "package", event: `{"Action":"skip",` + vendorPackageField + `}`},
		{
			name:  "unknown test",
			event: `{"Action":"skip",` + vendorPackageField + `,"Test":"TestUnexpectedVendorBehavior"}`,
		},
		{
			name:  "foreign package",
			event: `{"Action":"skip","Package":"example.invalid/foreign"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateVendorTestResult(completeVendorWirePass + test.event + "\n"); !errors.Is(err, ErrBootstrapFailed) {
				t.Fatalf("unexpected skip error = %v, want ErrBootstrapFailed", err)
			}
		})
	}
}

func TestValidateVendorTestResultRejectsAmbiguousJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		event string
	}{
		{name: "malformed event", event: `{"Action":`},
		{
			name:  "duplicate action",
			event: `{"Action":"fail","Action":"pass","Package":"github.com/dantte-lp/gobfd/test/interop-clab"}`,
		},
		{
			name: "invalid UTF-8",
			event: "{\"Action\":\"output\",\"Package\":\"github.com/dantte-lp/gobfd/test/interop-clab\"," +
				"\"Output\":\"\xff\"}",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateVendorTestResult(completeVendorWirePass + test.event + "\n"); !errors.Is(err, errLifecycleState) {
				t.Fatalf("ambiguous event error = %v, want errLifecycleState", err)
			}
		})
	}
}
