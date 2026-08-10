package daemon

import "testing"

func TestExtractUsefulCoreErrDropsBanner(t *testing.T) {
	banner := `Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/amd64)
A unified platform for anti-censorship.
Failed to start: main: failed to load config files: [instance-1.json] > infra/conf: REALITY only supports RAW, XHTTP and gRPC for now.`
	got := extractUsefulCoreErr(banner)
	if got == "" {
		t.Fatal("empty")
	}
	if !containsFold(got, "REALITY only supports") && !containsFold(got, "Failed to start") {
		t.Fatalf("expected real error, got %q", got)
	}
	if containsFold(got, "Penetrates Everything") {
		t.Fatalf("banner should be dropped: %q", got)
	}
	out := truncateOut(banner)
	if containsFold(out, "Penetrates Everything") {
		t.Fatalf("truncateOut still has banner: %q", out)
	}
	if !containsFold(out, "Failed to start") && !containsFold(out, "REALITY") {
		t.Fatalf("truncateOut missing error: %q", out)
	}
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			// simple case-insensitive contains
			ls, lsub := []rune(s), []rune(sub)
			for i := 0; i+len(lsub) <= len(ls); i++ {
				ok := true
				for j := 0; j < len(lsub); j++ {
					a, b := ls[i+j], lsub[j]
					if a >= 'A' && a <= 'Z' {
						a += 32
					}
					if b >= 'A' && b <= 'Z' {
						b += 32
					}
					if a != b {
						ok = false
						break
					}
				}
				if ok {
					return true
				}
			}
			return false
		})())
}
