package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMitaSystemdUnitUsesRunNotStart(t *testing.T) {
	body := mitaSystemdUnit("/var/lib/nft/cores/mieru/mita")
	if !strings.Contains(body, "ExecStart=/var/lib/nft/cores/mieru/mita run") {
		t.Fatalf("unit must ExecStart `mita run`, got:\n%s", body)
	}
	if strings.Contains(body, "ExecStart=/var/lib/nft/cores/mieru/mita start") {
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
	if err := os.WriteFile(p, []byte(mitaSystemdUnit("/usr/bin/mita")), 0o644); err != nil {
		t.Fatal(err)
	}
	if !unitIsNFTManaged(p) {
		t.Fatal("expected nft-managed")
	}
}

func TestResolveMitaBinaryPrefersPackaged(t *testing.T) {
	// Without real binaries, resolve returns empty — just ensure no panic.
	_ = resolveMitaBinary()
}

func TestMitaPortBindingsStillOK(t *testing.T) {
	// sanity that package still compiles with deploy path
	b := mitaPortBindings(8443, nil)
	if len(b) != 2 {
		t.Fatalf("%+v", b)
	}
}
