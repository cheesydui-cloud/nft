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
