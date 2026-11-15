package provision

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"sort"
	"sync"
	"time"
)

// Probe is one measured endpoint. Ms is the median of the samples, measured
// from the start of the request to the first byte of the reply, so it includes
// DNS, TCP and TLS. The status code does not matter, only that a reply came.
type Probe struct {
	Name  string `json:"name"`
	City  string `json:"city,omitempty"`
	Ms    int    `json:"ms"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// LatencyReport is what the speed check shows: this PC's Funnel address, and
// every Oracle Cloud region from the list, fastest first.
type LatencyReport struct {
	// Funnel is this PC's own Funnel address, when one is published.
	Funnel *Probe `json:"funnel,omitempty"`
	// Reference is a public Tailscale endpoint, measured on every run. It
	// stands in for a Funnel address before one exists: Funnel traffic also
	// enters Tailscale's own infrastructure.
	Reference *Probe  `json:"reference,omitempty"`
	Regions   []Probe `json:"regions"`
	// Fastest is the Oracle region that answered first, if any did.
	Fastest string `json:"fastest,omitempty"`
}

// ReferenceURL is the stand-in for a Funnel address when none is published.
const ReferenceURL = "https://login.tailscale.com/"

// oracleRegions are the Oracle Cloud regions near Israel plus two far ones for
// comparison. The object storage endpoint exists in every region, so it is a
// reliable place to ask for a reply.
var oracleRegions = []struct{ ID, City string }{
	{"il-jerusalem-1", "Jerusalem, Israel"},
	{"me-jeddah-1", "Jeddah, Saudi Arabia"},
	{"me-dubai-1", "Dubai, UAE"},
	{"me-abudhabi-1", "Abu Dhabi, UAE"},
	{"eu-frankfurt-1", "Frankfurt, Germany"},
	{"eu-milan-1", "Milan, Italy"},
	{"eu-marseille-1", "Marseille, France"},
	{"eu-amsterdam-1", "Amsterdam, Netherlands"},
	{"eu-zurich-1", "Zurich, Switzerland"},
	{"eu-paris-1", "Paris, France"},
	{"eu-london-1", "London, UK"},
	{"us-ashburn-1", "Ashburn, USA"},
}

const latencySamples = 3

// sampleOnce times one request on a fresh connection, so every sample pays the
// full cost of DNS, TCP and TLS, as a first visit does.
func sampleOnce(ctx context.Context, url string) (time.Duration, error) {
	tr := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		Timeout:   6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var first time.Time
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() { first = time.Now() },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if first.IsZero() {
		first = time.Now()
	}
	return first.Sub(start), nil
}

// measure takes the median of several samples. It fails only when none of the
// samples got a reply.
func measure(ctx context.Context, url string) (time.Duration, error) {
	var got []time.Duration
	var lastErr error
	for i := 0; i < latencySamples; i++ {
		d, err := sampleOnce(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		got = append(got, d)
	}
	if len(got) == 0 {
		return 0, lastErr
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	return got[len(got)/2], nil
}

func probe(ctx context.Context, name, city, url string) Probe {
	d, err := measure(ctx, url)
	if err != nil {
		return Probe{Name: name, City: city, Error: err.Error()}
	}
	return Probe{Name: name, City: city, Ms: int(d.Milliseconds()), OK: true}
}

// target is one endpoint to time, with the name and city it is shown under.
type target struct{ name, city, url string }

// measureAll probes every target at once and returns them in the order given.
func measureAll(ctx context.Context, targets []target) []Probe {
	out := make([]Probe, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			out[i] = probe(ctx, t.name, t.city, t.url)
		}(i, t)
	}
	wg.Wait()
	return out
}

// rankRegions puts the answering regions first, fastest first. Regions that
// did not answer follow, so the list still shows them.
func rankRegions(ps []Probe) []Probe {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].OK != ps[j].OK {
			return ps[i].OK
		}
		return ps[i].Ms < ps[j].Ms
	})
	return ps
}

// MeasureLatency probes the Funnel address (when there is one) and every
// Oracle region, then ranks the regions.
func MeasureLatency(ctx context.Context, funnelHost string) LatencyReport {
	var targets []target
	for _, r := range oracleRegions {
		targets = append(targets, target{r.ID, r.City, "https://objectstorage." + r.ID + ".oraclecloud.com/"})
	}
	var funnelTarget *target
	if funnelHost != "" {
		funnelTarget = &target{"Funnel: " + funnelHost, "", "https://" + funnelHost + "/"}
	}

	var rep LatencyReport
	done := make(chan []Probe, 1)
	go func() { done <- measureAll(ctx, targets) }()
	ref := measureAll(ctx, []target{{"Tailscale edge", "reference for Funnel", ReferenceURL}})[0]
	rep.Reference = &ref
	if funnelTarget != nil {
		p := measureAll(ctx, []target{*funnelTarget})[0]
		rep.Funnel = &p
	}
	rep.Regions = rankRegions(<-done)
	for _, p := range rep.Regions {
		if p.OK {
			rep.Fastest = p.Name
			break
		}
	}
	return rep
}
