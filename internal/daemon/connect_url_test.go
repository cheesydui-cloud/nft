package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePanelConnectURL(t *testing.T) {
	got, err := NormalizePanelConnectURL("https://panel.example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://panel.example.com/v1/agents" {
		t.Fatalf("got %q", got)
	}
	got, err = NormalizePanelConnectURL("https://panel.example.com:8443/", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://panel.example.com:8443/v1/agents" {
		t.Fatalf("got %q", got)
	}
	got, err = NormalizePanelConnectURL("wss://p.example/v1/agents", false)
	if err != nil || got != "wss://p.example/v1/agents" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := NormalizePanelConnectURL("http://1.2.3.4:7788", false); err == nil {
		t.Fatal("want reject plain http without insecure")
	}
	got, err = NormalizePanelConnectURL("http://1.2.3.4:7788", true)
	if err != nil || got != "ws://1.2.3.4:7788/v1/agents" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = NormalizePanelConnectURL("panel.example.com", false)
	if err != nil || got != "wss://panel.example.com/v1/agents" {
		t.Fatalf("bare host: got %q err=%v", got, err)
	}
}

func TestWriteReadConnectURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.connect")
	if err := WriteConnectURL(path, "wss://x/v1/agents"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadConnectURL(path)
	if err != nil || got != "wss://x/v1/agents" {
		t.Fatalf("got %q err=%v", got, err)
	}
	// Prefer file over flag
	resolved, err := ResolveConnectURL(path, "wss://old/v1/agents")
	if err != nil || resolved != "wss://x/v1/agents" {
		t.Fatalf("resolve %q err=%v", resolved, err)
	}
	// Missing file → flag
	resolved, err = ResolveConnectURL(filepath.Join(dir, "missing"), "wss://flag/v1/agents")
	if err != nil || resolved != "wss://flag/v1/agents" {
		t.Fatalf("flag fallback %q err=%v", resolved, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("want private mode, got %v", fi.Mode())
	}
}
