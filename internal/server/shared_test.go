package server

import "testing"

func TestParseExitBareIPv6Hint(t *testing.T) {
	_, _, err := parseExit("2001:db8::1:1080")
	if err == nil {
		t.Fatal("expected error for bare IPv6 without brackets")
	}
	want := "IPv6 地址需要用方括号包裹，例如 [::1]:1080"
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}

func TestParseExitBracketedIPv6Succeeds(t *testing.T) {
	host, port, err := parseExit("[2001:db8::1]:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "2001:db8::1" || port != 1080 {
		t.Fatalf("got host=%q port=%d, want host=2001:db8::1 port=1080", host, port)
	}
}

func TestParseExitGenericFormatError(t *testing.T) {
	_, _, err := parseExit("not-an-address")
	if err == nil {
		t.Fatal("expected error for malformed input")
	}
	want := "出口需为 host:port 形式"
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}

func TestParseExitValidIPv4(t *testing.T) {
	host, port, err := parseExit("10.0.0.1:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "10.0.0.1" || port != 80 {
		t.Fatalf("got host=%q port=%d, want host=10.0.0.1 port=80", host, port)
	}
}

func TestParseExit(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{"1.2.3.4:80", false},
		{"example.com:443", false},
		{"[2001:db8::1]:80", false},
		{"4212:80", true}, // 纯数字 host —— 被误填的端口
		{"host:0", true},  // 端口非法
		{":80", true},     // host 空
		{"nohostport", true},
	}
	for _, c := range cases {
		_, _, err := parseExit(c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("parseExit(%q) err = %v, wantErr = %v", c.raw, err, c.wantErr)
		}
	}
}

func TestPreferLandingProtoMieru(t *testing.T) {
	if got := preferLandingProto("mierus://u:p@h?port=1&protocol=TCP", "tcp"); got != "tcp+udp" {
		t.Fatalf("share uri: got %q", got)
	}
	if got := preferLandingProtoHint("", "mieru", "tcp"); got != "tcp+udp" {
		t.Fatalf("warehouse hint: got %q", got)
	}
	if got := preferLandingProtoHint("", "mieru", "udp"); got != "udp" {
		t.Fatalf("explicit udp must stay, got %q", got)
	}
	if got := preferLandingProto("ss://x@h:1", "tcp"); got != "tcp" {
		t.Fatalf("ss must stay tcp, got %q", got)
	}
}

func TestParseExitFullSOCKS5(t *testing.T) {
	pe, err := parseExitFull("1.2.3.4:443", "socks5", "socks5://user:pass@10.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	if pe.Type != "socks5" || pe.Host != "1.2.3.4" || pe.Port != 443 {
		t.Fatalf("got %+v", pe)
	}
	if pe.URI != "socks5://user:pass@10.0.0.1:1080" {
		t.Fatalf("uri=%q", pe.URI)
	}
	// socks:// normalizes to socks5://
	pe, err = parseExitFull("example.com:80", "socks", "socks://10.0.0.2:1080")
	if err != nil {
		t.Fatal(err)
	}
	if pe.URI != "socks5://10.0.0.2:1080" {
		t.Fatalf("uri=%q", pe.URI)
	}
	// Missing URI.
	if _, err := parseExitFull("1.2.3.4:80", "socks5", ""); err == nil {
		t.Fatal("expected error for missing exit_uri")
	}
	// Single socks URI in exit without split fields.
	if _, err := parseExitFull("socks5://u:p@h:1", "", ""); err == nil {
		t.Fatal("expected guidance error")
	}
}

func TestApplyExitConstraints(t *testing.T) {
	mode, err := applyExitConstraints("socks5", "tcp", "kernel")
	if err != nil || mode != "userspace" {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	if _, err := applyExitConstraints("socks5", "udp", "userspace"); err == nil {
		t.Fatal("expected udp rejection")
	}
	mode, err = applyExitConstraints("direct", "tcp+udp", "kernel")
	if err != nil || mode != "kernel" {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
}

func TestRedactedExitURI(t *testing.T) {
	got := redactedExitURI("socks5://alice:secret@1.2.3.4:1080")
	if got != "socks5://alice:***@1.2.3.4:1080" {
		t.Fatalf("got %q", got)
	}
	got = redactedExitURI("socks5://1.2.3.4:1080")
	if got != "socks5://1.2.3.4:1080" {
		t.Fatalf("got %q", got)
	}
}
