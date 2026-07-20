package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassifyDialerEnd(t *testing.T) {
	cases := []struct {
		err        error
		helloAcked bool
		lived      time.Duration
		want       string
	}{
		{nil, true, time.Minute, "agent_stop"},
		{context.Canceled, true, time.Minute, "context_canceled"},
		{fmt.Errorf("watchdog: no read for 50s (stale threshold 45s)"), true, time.Minute, "watchdog_stale"},
		{fmt.Errorf("dial wss://x: dial tcp: connection refused"), false, 0, "dial_fail"},
		{fmt.Errorf("hello rejected: unknown or revoked token"), false, time.Second, "hello_rejected"},
		{fmt.Errorf("read hello_ack: i/o timeout"), false, time.Second, "hello_timeout"},
		{fmt.Errorf("failed to get reader: received close frame: status = StatusGoingAway"), true, time.Minute, "panel_going_away"},
		{fmt.Errorf("failed to get reader: received close frame: status = StatusNormalClosure"), true, time.Minute, "panel_close"},
		{fmt.Errorf("read: i/o timeout"), true, time.Minute, "read_timeout"},
		{fmt.Errorf("read: connection reset by peer"), true, time.Minute, "connection_drop"},
		{fmt.Errorf("session panic: boom"), true, time.Second, "session_panic"},
		{errors.New("something odd"), true, 2 * time.Second, "short_session"},
		{errors.New("something odd"), true, time.Minute, "session_error"},
		{errors.New("something odd"), false, 0, "pre_hello_fail"},
	}
	for _, tc := range cases {
		got := classifyDialerEnd(tc.err, tc.helloAcked, tc.lived)
		if got != tc.want {
			t.Errorf("err=%v hello=%v lived=%v: want %q got %q", tc.err, tc.helloAcked, tc.lived, tc.want, got)
		}
	}
}
