package server

import (
	"testing"

	"nft/internal/db"
)

func TestUpgradeForLabelSyncWhenShaMatches(t *testing.T) {
	art := &agentArtifact{Version: "v1.0.0", SHA: "abc123", Data: []byte("agent-bytes")}

	// Node already on the target binary: label-only sync, no download URL.
	match := upgradeFor(&db.Node{AgentSHA: "abc123"}, art, "https://panel")
	if len(match.Data) != 0 || match.DownloadAt != "" {
		t.Fatalf("matched sha must be label-only, got data=%d downloadAt=%q", len(match.Data), match.DownloadAt)
	}
	if match.Version != "v1.0.0" || match.SHA256 != "abc123" {
		t.Fatalf("label-only must still carry version+sha: %+v", match)
	}

	// Different binary: metadata + HTTP download URL, never inline the agent
	// on the control WS (avoids multi-MB write timeouts on slow links).
	full := upgradeFor(&db.Node{AgentSHA: "stale"}, art, "https://panel/")
	if len(full.Data) != 0 {
		t.Fatalf("mismatched sha must not inline bytes, got %d", len(full.Data))
	}
	if full.DownloadAt != "https://panel/v1/binary" || full.Size != int64(len(art.Data)) {
		t.Fatalf("mismatched sha must point at panel binary: %+v", full)
	}

	// Legacy agent with no reported sha: still must offer a download (can't assume match).
	legacy := upgradeFor(&db.Node{AgentSHA: ""}, art, "https://panel")
	if legacy.DownloadAt == "" || legacy.Size != int64(len(art.Data)) {
		t.Fatalf("empty agent sha must offer download, got %+v", legacy)
	}
	if len(legacy.Data) != 0 {
		t.Fatalf("empty agent sha must not inline bytes")
	}
}
