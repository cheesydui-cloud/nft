package daemon

import (
	"testing"

	"nft/internal/wsproto"
)

func TestProxyStatsReAddAndSamplePark(t *testing.T) {
	s := &proxyStatsState{
		last:    map[int64][2]int64{},
		pending: map[int64][2]int64{},
	}
	// Seed cursor as if we already reported 100/50.
	s.last[7] = [2]int64{100, 50}
	// NACK retransmit of 40/20 when cursor cannot fully rewind.
	s.reAdd([]wsproto.ProxyCounterSample{{InstanceID: 7, BytesUp: 150, BytesDown: 80}})
	// last should clamp to 0, overflow parked.
	if s.last[7][0] != 0 || s.last[7][1] != 0 {
		t.Fatalf("cursor want 0,0 got %v", s.last[7])
	}
	if s.pending[7][0] != 50 || s.pending[7][1] != 30 {
		t.Fatalf("park want 50/30 got %v", s.pending[7])
	}
}

func TestParseXrayStatsQueryJSON(t *testing.T) {
	out := `{"stat":[
		{"name":"inbound>>>vless-in>>>traffic>>>uplink","value":"1234"},
		{"name":"inbound>>>vless-in>>>traffic>>>downlink","value":"5678"}
	]}`
	up, down, ok := parseXrayStatsQuery(out)
	if !ok || up != 1234 || down != 5678 {
		t.Fatalf("got up=%d down=%d ok=%v", up, down, ok)
	}
}

func TestParseHumanBytes(t *testing.T) {
	// Force non-constant float so int64() conversion is allowed at runtime.
	f129, f938 := 12.9, 938.1
	want129 := int64(f129*float64(1024*1024) + 0.5)
	want938 := int64(f938*float64(1024) + 0.5)
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"12.9MiB", want129, true},
		{"4.0GiB", 4 * 1024 * 1024 * 1024, true},
		{"1024", 1024, true},
		{"938.1KiB", want938, true},
		{"-", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseHumanBytes(c.in)
		if ok != c.ok || (c.ok && got != c.want) {
			t.Fatalf("parseHumanBytes(%q)=%d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseMitaUsersTable(t *testing.T) {
	raw := `User  LastActive            1DayDownload  1DayUpload  30DaysDownload  30DaysUpload
abcd  2025-04-23T01:02:03Z  938.1MiB      12.9MiB     4.0GiB          31.8MiB
efgh  2025-04-23T02:00:00Z  1.0MiB        2.0MiB      10.0MiB         20.0MiB
`
	m := parseMitaUsersTable(raw)
	a, ok := m["abcd"]
	if !ok {
		t.Fatal("missing abcd")
	}
	// up=30DaysUpload, down=30DaysDownload
	wantDown, _ := parseHumanBytes("4.0GiB")
	wantUp, _ := parseHumanBytes("31.8MiB")
	if a.down != wantDown || a.up != wantUp {
		t.Fatalf("abcd up=%d down=%d want up=%d down=%d", a.up, a.down, wantUp, wantDown)
	}
	b, ok := m["efgh"]
	if !ok {
		t.Fatal("missing efgh")
	}
	if b.up != 20*1024*1024 || b.down != 10*1024*1024 {
		t.Fatalf("efgh up=%d down=%d", b.up, b.down)
	}
}

func TestProxyStatsBaselineNoFirstDelta(t *testing.T) {
	s := &proxyStatsState{
		last:      map[int64][2]int64{},
		baselined: map[int64]bool{},
		pending:   map[int64][2]int64{},
	}
	// First live observation should only arm the cursor (no billable dump of
	// long-running mita 30d totals / xray cumulative after agent restart).
	out := s.applyLiveDeltas(map[int64][2]int64{1: {5_000_000, 9_000_000}}, 1000)
	if len(out) != 0 {
		t.Fatalf("first sample must baseline only, got %v", out)
	}
	if !s.baselined[1] || s.last[1][0] != 5_000_000 || s.last[1][1] != 9_000_000 {
		t.Fatalf("baseline state last=%v baselined=%v", s.last[1], s.baselined[1])
	}
	// Growth produces delta.
	out = s.applyLiveDeltas(map[int64][2]int64{1: {5_000_100, 9_000_050}}, 1000)
	if len(out) != 1 || out[0].BytesUp != 100 || out[0].BytesDown != 50 {
		t.Fatalf("growth delta got %v", out)
	}
	// Soft decrease (rolling window): no re-bill.
	out = s.applyLiveDeltas(map[int64][2]int64{1: {4_900_000, 8_900_000}}, 1000)
	if len(out) != 0 {
		t.Fatalf("window slide must yield 0 delta, got %v", out)
	}
	// Hard reset to near-zero: recount current.
	out = s.applyLiveDeltas(map[int64][2]int64{1: {10, 20}}, 1000)
	if len(out) != 1 || out[0].BytesUp != 10 || out[0].BytesDown != 20 {
		t.Fatalf("hard reset delta got %v", out)
	}
}
