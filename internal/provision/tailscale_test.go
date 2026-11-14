package provision

import "testing"

func TestFunnelOutputActive(t *testing.T) {
	cases := map[string]bool{
		"":                false,
		"No serve config": false,
		"https://pc.example.ts.net (Funnel on)\n|-- /  proxy http://127.0.0.1:8081":             true,
		"|-- tcp://rdgatetest.tail69c48.ts.net:443 (TLS terminated)\n|--> tcp://127.0.0.1:8081": true,
	}
	for out, want := range cases {
		if got := funnelOutputActive(out); got != want {
			t.Errorf("funnelOutputActive(%q) = %v, want %v", out, got, want)
		}
	}
}
