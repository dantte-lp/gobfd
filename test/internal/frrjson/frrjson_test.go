package frrjson

import "testing"

func TestExtractJSONArray(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		output string
		want   string
	}{
		"plain": {
			output: `[{"peer":"172.21.0.10","status":"up"}]`,
			want:   `[{"peer":"172.21.0.10","status":"up"}]`,
		},
		"warning suffix": {
			output: "[\n  {\"peer\":\"172.21.0.10\",\"status\":\"up\"}\n]\n% Can't open configuration file /etc/frr/vtysh.conf\nConfiguration file[/etc/frr/frr.conf] processing failure: 11\n",
			want:   "[\n  {\"peer\":\"172.21.0.10\",\"status\":\"up\"}\n]",
		},
		"warning prefix and suffix": {
			output: "% warning\n[\n  {\"peer\":\"172.21.0.10\",\"status\":\"up\"}\n]\n% suffix\n",
			want:   "[\n  {\"peer\":\"172.21.0.10\",\"status\":\"up\"}\n]",
		},
		"diagnostic bracket before json": {
			output: "% Can't open configuration file /etc/frr/vtysh.conf\nConfiguration file[/etc/frr/frr.conf] processing failure: 11\n[\n  {\"peer\":\"172.21.0.10\",\"status\":\"up\"}\n]\n",
			want:   "[\n  {\"peer\":\"172.21.0.10\",\"status\":\"up\"}\n]",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ExtractJSONArray(tt.output)
			if err != nil {
				t.Fatalf("ExtractJSONArray: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ExtractJSONArray = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractJSONArrayRejectsMissingArray(t *testing.T) {
	t.Parallel()

	if _, err := ExtractJSONArray("% no json\n"); err == nil {
		t.Fatal("ExtractJSONArray returned nil error")
	}
}
