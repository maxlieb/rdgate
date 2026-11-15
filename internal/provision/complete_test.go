package provision

import "testing"

func TestIsCompleteFollowsTheFlag(t *testing.T) {
	no, yes := false, true
	half := Config{ClientID: "x", Hostname: "pc.tail.ts.net", Complete: &no}
	if half.IsComplete() {
		t.Fatal("an interrupted setup must not count as installed")
	}
	done := Config{ClientID: "x", Hostname: "pc.tail.ts.net", Complete: &yes}
	if !done.IsComplete() {
		t.Fatal("a finished setup must count as installed")
	}
}

func TestIsCompleteKeepsOlderConfigs(t *testing.T) {
	old := Config{ClientID: "x", Hostname: "pc.tail.ts.net"}
	if !old.IsComplete() {
		t.Fatal("a config from before the flag, with a client and an address, counts as installed")
	}
	none := Config{Hostname: "pc.tail.ts.net"}
	if none.IsComplete() {
		t.Fatal("no sign-in client means no installation")
	}
}
