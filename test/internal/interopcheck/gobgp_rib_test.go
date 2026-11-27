package interopcheck

import (
	"encoding/json"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestGoBGPRIBObservation(t *testing.T) {
	t.Parallel()
	const target = "10.20.0.0/24"
	for _, tc := range []struct {
		name, input, prefix string
		present, invalid    bool
	}{
		{name: "empty table", input: "{}"},
		{name: "present", input: `{"10.20.0.0/24":[{"best":true}]}`, present: true},
		{name: "non-best path", input: `{"10.20.0.0/24":[{"best":false}]}`, present: true},
		{name: "other attributes", input: `{"10.20.0.0/24":[{"best":true,"attrs":[],"future":{"a":1}}]}`, present: true},
		{name: "substring prefix", input: `{"110.20.0.0/24":[{"best":true}]}`},
		{name: "different prefix length", input: `{"10.20.0.0/25":[{"best":true}]}`},
		{name: "IPv6", prefix: "2001:db8::/64", input: `{"2001:db8::/64":[{"best":false}]}`, present: true},
		{name: "maximum bytes", input: strings.Repeat(" ", maxGoBGPRIBSize-2) + "{}"},
		{name: "oversize", input: strings.Repeat(" ", maxGoBGPRIBSize-1) + "{}", invalid: true},
		{name: "empty payload", invalid: true},
		{name: "truncated", input: "{", invalid: true},
		{name: "null table", input: "null", invalid: true},
		{name: "array table", input: "[]", invalid: true},
		{name: "trailing object", input: "{} {}", invalid: true},
		{name: "null destination", input: `{"10.20.0.0/24":null}`, invalid: true},
		{name: "empty paths", input: `{"10.20.0.0/24":[]}`, invalid: true},
		{name: "null path", input: `{"10.20.0.0/24":[null]}`, invalid: true},
		{name: "missing best", input: `{"10.20.0.0/24":[{}]}`, invalid: true},
		{name: "null best", input: `{"10.20.0.0/24":[{"best":null}]}`, invalid: true},
		{name: "string best", input: `{"10.20.0.0/24":[{"best":"true"}]}`, invalid: true},
		{name: "wrong path kind", input: `{"10.20.0.0/24":[true]}`, invalid: true},
		{name: "duplicate route", input: `{"10.20.0.0/24":[{"best":true}],"10.20.0.0/24":[{"best":false}]}`, invalid: true},
		{name: "duplicate path field", input: `{"10.20.0.0/24":[{"best":true,"best":false}]}`, invalid: true},
		{name: "duplicate ignored field", input: `{"10.20.0.0/24":[{"best":true,"attrs":{"a":1,"a":2}}]}`, invalid: true},
		{name: "invalid UTF8", input: "{\"10.20.0.0/24\":[{\"best\":true,\"future\":\"" +
			string([]byte{0xff}) + "\"}]}", invalid: true},
		{name: "invalid requested prefix", prefix: "bad", input: "{}", invalid: true},
		{name: "requested host bits", prefix: "10.20.0.1/24", input: "{}", invalid: true},
		{name: "invalid other prefix", input: `{"bad":[{"best":true}]}`, invalid: true},
		{name: "route host bits", input: `{"10.20.0.1/24":[{"best":true}]}`, invalid: true},
		{name: "noncanonical IPv6", input: `{"2001:0db8::/64":[{"best":true}]}`, invalid: true},
		{name: "malformed unrelated destination", input: `{"10.21.0.0/24":[{}]}`, invalid: true},
		{name: "valid target plus invalid other", input: `{"10.20.0.0/24":[{"best":true}],"10.21.0.0/24":[]}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prefix := tc.prefix
			if prefix == "" {
				prefix = target
			}
			present, err := GoBGPRouteExists([]byte(tc.input), prefix)
			if (err != nil) != tc.invalid || present != tc.present {
				t.Fatalf("RIB membership=%t error=%v, want present=%t invalid=%t", present, err, tc.present, tc.invalid)
			}
		})
	}
}

func FuzzGoBGPRIBObservation(f *testing.F) {
	for _, input := range []string{"{}", "null", `{"10.20.0.0/24":[{"best":false}]}`, `{"10.21.0.0/24":[]}`} {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		const prefix = "10.20.0.0/24"
		present, err := GoBGPRouteExists(input, prefix)
		if err != nil {
			if present {
				t.Fatal("failed observation published route presence")
			}
			return
		}
		var model map[string]json.RawMessage
		if err := json.Unmarshal(input, &model); err != nil || model == nil || len(input) > maxGoBGPRIBSize {
			t.Fatalf("invalid input accepted: error=%v", err)
		}
		_, expected := model[prefix]
		if present != expected {
			t.Fatalf("RIB membership=%t, independent object key=%t", present, expected)
		}
	})
}

func BenchmarkGoBGPRIBObservation(b *testing.B) {
	for _, count := range []int{0, 1, 256, 4096} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			best := true
			destinations := make(map[string][]goBGPRIBPath, count)
			address := netip.MustParseAddr("10.0.0.0")
			for range count {
				destinations[netip.PrefixFrom(address, 32).String()] = []goBGPRIBPath{{Best: &best}}
				address = address.Next()
			}
			input, err := json.Marshal(destinations)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			for b.Loop() {
				present, err := GoBGPRouteExists(input, "10.0.0.0/32")
				if err != nil || present != (count > 0) {
					b.Fatalf("RIB membership=%t error=%v", present, err)
				}
			}
		})
	}
}

func BenchmarkGoBGPRIBPrefix(b *testing.B) {
	for _, text := range []string{"10.0.0.0/32", "2001:db8::/64"} {
		b.Run(text, func(b *testing.B) {
			want := netip.MustParsePrefix(text)
			b.ReportAllocs()
			for b.Loop() {
				prefix, err := goBGPRIBPrefix(text)
				if err != nil || prefix != want {
					b.Fatalf("prefix=%v error=%v", prefix, err)
				}
			}
		})
	}
}
