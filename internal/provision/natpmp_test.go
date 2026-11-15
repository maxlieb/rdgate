package provision

import (
	"context"
	"net"
	"testing"
	"time"
)

// fakeNATPMPRouter answers one NAT-PMP address request with the given reply.
func fakeNATPMPRouter(t *testing.T, reply []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 16)
		_, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if buf[0] != 0 || buf[1] != 0 {
			return // not an external-address request
		}
		conn.WriteTo(reply, addr)
	}()
	return conn.LocalAddr().String()
}

func TestNATPMPReturnsTheExternalAddress(t *testing.T) {
	// version 0, opcode 128 (response to 0), result 0, epoch, then 203.0.113.7
	reply := []byte{0, 128, 0, 0, 0, 0, 0, 1, 203, 0, 113, 7}
	addr := fakeNATPMPRouter(t, reply)
	ip, err := NATPMPExternalIP(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" {
		t.Fatalf("got %s", ip)
	}
}

func TestNATPMPRefusalIsAnError(t *testing.T) {
	reply := []byte{0, 128, 0, 3, 0, 0, 0, 1, 0, 0, 0, 0}
	addr := fakeNATPMPRouter(t, reply)
	if _, err := NATPMPExternalIP(context.Background(), addr); err == nil {
		t.Fatal("a refused request must not produce an address")
	}
}

func TestNATPMPSilentRouterTimesOut(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := NATPMPExternalIP(ctx, conn.LocalAddr().String()); err == nil {
		t.Fatal("a silent router must not produce an address")
	}
}
