package provision

import "testing"

func TestClassifyWAN(t *testing.T) {
	cases := []struct {
		name, wan, public, want string
	}{
		{"carrier NAT range", "100.64.12.9", "203.0.113.7", VerdictCGNAT},
		{"private router", "192.168.1.1", "203.0.113.7", VerdictCGNAT},
		{"public and matching", "203.0.113.7", "203.0.113.7", VerdictDirect},
		{"public but different from the internet", "203.0.113.7", "198.51.100.9", VerdictMismatch},
		{"public, internet view unknown", "203.0.113.7", "", VerdictUnsure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, reason := ClassifyWAN(c.wan, c.public); got != c.want {
				t.Fatalf("got %s (%s), want %s", got, reason, c.want)
			}
		})
	}
}
