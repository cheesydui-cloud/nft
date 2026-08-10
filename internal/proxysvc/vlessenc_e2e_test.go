// Live e2e: panel-native / xray-vlessenc / none through real xray client+server (plain TCP).
// REALITY is skipped here — dest reachability is environment-dependent; vlessenc itself is not.
// Requires /tmp/xray (or xray on PATH). Skips otherwise.
package proxysvc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func findXrayBin(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/tmp/xray"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if b, err := exec.LookPath("xray"); err == nil {
		return b
	}
	t.Skip("xray binary not found")
	return ""
}

func parseXrayVlessEncShort(out string) (enc, dec string) {
	var gotDec, gotEnc string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.Contains(ln, `"decryption"`) && strings.Contains(ln, "600s") && gotDec == "" {
			if i := strings.Index(ln, `"mlkem`); i >= 0 {
				if j := strings.LastIndex(ln, `"`); j > i {
					gotDec = ln[i+1 : j]
				}
			}
		}
		if strings.Contains(ln, `"encryption"`) && strings.Contains(ln, "0rtt") && gotDec != "" && gotEnc == "" {
			if i := strings.Index(ln, `"mlkem`); i >= 0 {
				if j := strings.LastIndex(ln, `"`); j > i {
					cand := ln[i+1 : j]
					if len(cand) < 120 {
						gotEnc = cand
					}
				}
			}
		}
	}
	return gotEnc, gotDec
}

func TestE2EVlessEncHandshake(t *testing.T) {
	bin := findXrayBin(t)
	dir := t.TempDir()
	uuid := "83aca93f-7528-44d8-81f3-28f60f9b4eee"
	portBase := 40551

	encN, decN := GenerateVlessEncX25519()
	xout, err := exec.Command(bin, "vlessenc").CombinedOutput()
	if err != nil {
		t.Fatalf("vlessenc: %v\n%s", err, xout)
	}
	encX, decX := parseXrayVlessEncShort(string(xout))
	if encX == "" || decX == "" || len(encX) > 120 {
		t.Fatalf("parse short pair failed enc=%q dec=%q", encX, decX)
	}

	// Local HTTP origin the proxy will reach (no external network).
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("OK-ORIGIN"))
	}))
	defer origin.Close()
	originURL := origin.URL

	type tc struct {
		name, enc, dec string
	}
	cases := []tc{
		{"native", encN, decN},
		{"xray-vlessenc", encX, decX},
		{"none", "none", "none"},
	}

	for i, c := range cases {
		c := c
		p := portBase + i*2
		cliPort := p + 1
		t.Run(c.name, func(t *testing.T) {
			srv := map[string]any{
				"log": map[string]any{"loglevel": "warning"},
				"inbounds": []any{map[string]any{
					"listen": "127.0.0.1", "port": p, "protocol": "vless",
					"settings": map[string]any{
						"clients":    []any{map[string]any{"id": uuid}},
						"decryption": c.dec,
					},
					"streamSettings": map[string]any{"network": "tcp", "security": "none"},
				}},
				"outbounds": []any{map[string]any{"protocol": "freedom"}},
			}
			cli := map[string]any{
				"log": map[string]any{"loglevel": "warning"},
				"inbounds": []any{map[string]any{
					"listen": "127.0.0.1", "port": cliPort, "protocol": "socks",
					"settings": map[string]any{"udp": false},
				}},
				"outbounds": []any{map[string]any{
					"protocol": "vless",
					"settings": map[string]any{
						"vnext": []any{map[string]any{
							"address": "127.0.0.1", "port": p,
							"users": []any{map[string]any{
								"id": uuid, "encryption": c.enc,
							}},
						}},
					},
					"streamSettings": map[string]any{"network": "tcp", "security": "none"},
				}},
			}
			srvPath := filepath.Join(dir, c.name+"-srv.json")
			cliPath := filepath.Join(dir, c.name+"-cli.json")
			sb, _ := json.MarshalIndent(srv, "", "  ")
			cb, _ := json.MarshalIndent(cli, "", "  ")
			if err := os.WriteFile(srvPath, sb, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cliPath, cb, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{srvPath, cliPath} {
				if out, err := exec.Command(bin, "run", "-test", "-c", path).CombinedOutput(); err != nil {
					t.Fatalf("xray -test %s: %v\n%s", path, err, out)
				}
			}

			srvP := exec.Command(bin, "run", "-c", srvPath)
			if err := srvP.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srvP.Process.Kill(); _, _ = srvP.Process.Wait() }()
			time.Sleep(300 * time.Millisecond)

			cliP := exec.Command(bin, "run", "-c", cliPath)
			if err := cliP.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cliP.Process.Kill(); _, _ = cliP.Process.Wait() }()
			time.Sleep(350 * time.Millisecond)

			cmd := exec.Command("curl", "-sS", "-m", "8",
				"--socks5-hostname", fmt.Sprintf("127.0.0.1:%d", cliPort),
				originURL+"/")
			out, err := cmd.CombinedOutput()
			body := string(out)
			t.Logf("enc_len=%d dec_len=%d body=%q err=%v", len(c.enc), len(c.dec), body, err)
			if err != nil || body != "OK-ORIGIN" {
				t.Fatalf("handshake/proxy FAILED for %s: err=%v body=%q", c.name, err, body)
			}
		})
	}
}

func TestShareURIEncryptionRoundTrip(t *testing.T) {
	enc, dec := GenerateVlessEncX25519()
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "83aca93f-7528-44d8-81f3-28f60f9b4eee", Flow: "xtls-rprx-vision", Network: "tcp",
		Security: "reality", ServerName: "www.kyoto-u.ac.jp", PublicKey: pub, PrivateKey: priv,
		ShortID: "aabbccdd", Encryption: enc, Decryption: dec, Fingerprint: "chrome",
	})
	uri, err := BuildShareURI("vless", "TEST7", "82.22.26.185", 34675, raw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	got := u.Query().Get("encryption")
	if got != enc {
		t.Fatalf("roundtrip encryption mismatch\nwant %s\ngot  %s\nuri %s", enc, got, uri)
	}
	_ = dec
}
