package provision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A slow server must rank after a fast one, and an unreachable one after both.
func TestRankRegionsByMeasuredTime(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fast.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got := rankRegions(measureAll(ctx, []target{
		{"slow", "Slowville", slow.URL},
		{"dead", "Nowhere", deadURL},
		{"fast", "Fastville", fast.URL},
	}))

	if got[0].Name != "fast" || !got[0].OK {
		t.Fatalf("fastest first: got %+v", got)
	}
	if got[1].Name != "slow" || !got[1].OK || got[1].Ms < 100 {
		t.Fatalf("slow second with its real time: got %+v", got[1])
	}
	if got[2].Name != "dead" || got[2].OK {
		t.Fatalf("unreachable last: got %+v", got[2])
	}
}
