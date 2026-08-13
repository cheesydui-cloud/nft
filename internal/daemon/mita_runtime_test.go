package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMitaSystemdUnitUsesRunNotStart(t *testing.T) {
	body := mitaSystemdUnit("/usr/local/bin/mita")
	if !strings.Contains(body, "ExecStart=/usr/local/bin/mita run") {
		t.Fatalf("unit must ExecStart `mita run`, got:\n%s", body)
	}
	if strings.Contains(body, "ExecStart=/usr/local/bin/mita start") {
		t.Fatal("must not use `mita start` as ExecStart (start is RPC, needs daemon)")
	}
	if !strings.Contains(body, mitaUnitMarker) {
		t.Fatal("missing nft-managed marker")
	}
	if !strings.Contains(body, "User=mita") || !strings.Contains(body, "Group=mita") {
		t.Fatal("unit must run as mita user")
	}
	if !strings.Contains(body, "/var/run/mita") {
		t.Fatal("unit must prepare /var/run/mita")
	}
	if !strings.Contains(body, "ExecStartPre=+/usr/bin/test -x /usr/local/bin/mita") {
		t.Fatal("unit should verify binary is executable before start")
	}
}

func TestUnitIsNFTManaged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mita.service")
	if unitIsNFTManaged(p) {
		t.Fatal("missing file should be false")
	}
	if err := os.WriteFile(p, []byte("[Unit]\nDescription=other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if unitIsNFTManaged(p) {
		t.Fatal("unrelated unit")
	}
	if err := os.WriteFile(p, []byte(mitaSystemdUnit("/usr/local/bin/mita")), 0o644); err != nil {
		t.Fatal(err)
	}
	if !unitIsNFTManaged(p) {
		t.Fatal("expected nft-managed")
	}
}

func TestNeedRewrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b")
	if !needRewrite(p, []byte("abc")) {
		t.Fatal("missing file needs rewrite")
	}
	if err := os.WriteFile(p, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if needRewrite(p, []byte("abc")) {
		t.Fatal("same content")
	}
	if !needRewrite(p, []byte("abd")) {
		t.Fatal("different content")
	}
}

func TestMitaPortBindingsStillOK(t *testing.T) {
	b := mitaPortBindings(8443, nil)
	if len(b) != 2 {
		t.Fatalf("%+v", b)
	}
}

func TestMitaStatusIsRunning(t *testing.T) {
	if !mitaStatusIsRunning(`mieru server status is "RUNNING"`) {
		t.Fatal("RUNNING")
	}
	if mitaStatusIsRunning(`mieru server status is "IDLE"`) {
		t.Fatal("IDLE")
	}
	if mitaStatusIsRunning("stopped") {
		t.Fatal("stopped")
	}
}
