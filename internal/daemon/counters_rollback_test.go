package daemon

import (
	"testing"

	"nft/internal/forward"
)

// A failed counters send must not lose the deltas: reAddCounters rewinds the
// cursor so the next sample re-reports them, and a successful send (no rollback)
// leaves the cursor advanced so unchanged counters yield nothing.
func TestCounterSamplesRollbackOnFailedSend(t *testing.T) {
	fake := &fakeDataplane{}
	d := newTestDaemon(t)
	d.dp = fake

	fake.counters = []forward.Counter{{Proto: "tcp", ListenPort: 12000, BytesUp: 100, BytesDown: 50}}

	samples := d.counterSamples()
	if len(samples) != 1 || samples[0].BytesUp != 100 || samples[0].BytesDown != 50 {
		t.Fatalf("first sample = %+v, want up=100 down=50", samples)
	}

	// Simulate a failed send: roll the deltas back.
	d.reAddCounters(samples)

	// Kernel counters unchanged → the rolled-back delta must be re-reported.
	samples2 := d.counterSamples()
	if len(samples2) != 1 || samples2[0].BytesUp != 100 || samples2[0].BytesDown != 50 {
		t.Fatalf("after rollback re-report = %+v, want up=100 down=50", samples2)
	}

	// This send "succeeds" (no rollback); with no new bytes the next poll is empty.
	if got := d.counterSamples(); len(got) != 0 {
		t.Fatalf("after commit with no new bytes, want no samples, got %+v", got)
	}
}

// foldCountersBeforeFlush must preserve bytes that would otherwise be zeroed by
// nft.Apply's delete+recreate table. After fold, a kernel counter drop to 0
// must still surface the pre-flush delta on the next counterSamples call.
func TestFoldCountersBeforeFlushPreservesBytes(t *testing.T) {
	fake := &fakeDataplane{}
	d := newTestDaemon(t)
	d.dp = fake

	fake.counters = []forward.Counter{{Proto: "tcp", ListenPort: 13000, BytesUp: 500, BytesDown: 200}}
	// Establish a baseline (first sample advances cursor, reports 500/200).
	if s := d.counterSamples(); len(s) != 1 || s[0].BytesUp != 500 {
		t.Fatalf("baseline sample = %+v", s)
	}

	// More traffic then a reconcile-style flush.
	fake.counters = []forward.Counter{{Proto: "tcp", ListenPort: 13000, BytesUp: 800, BytesDown: 350}}
	d.foldCountersBeforeFlush()
	// Kernel table recreated → counters reset to 0.
	fake.counters = []forward.Counter{{Proto: "tcp", ListenPort: 13000, BytesUp: 0, BytesDown: 0}}

	samples := d.counterSamples()
	if len(samples) != 1 {
		t.Fatalf("want 1 sample after fold+flush, got %+v", samples)
	}
	if samples[0].BytesUp != 300 || samples[0].BytesDown != 150 {
		t.Fatalf("fold must park pre-flush delta 300/150, got up=%d down=%d", samples[0].BytesUp, samples[0].BytesDown)
	}

	// NACK after fold-originated sample: reAdd must still retransmit (cursor
	// may be 0 post-flush, so remainder is parked).
	d.reAddCounters(samples)
	samples2 := d.counterSamples()
	if len(samples2) != 1 || samples2[0].BytesUp != 300 || samples2[0].BytesDown != 150 {
		t.Fatalf("reAdd after flush must retransmit parked delta, got %+v", samples2)
	}
}
