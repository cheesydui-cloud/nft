package subexport

import (
	"strings"
	"testing"
)

func sampleNodes() []Node {
	return []Node{
		{
			Name:     "HK · VLESS",
			Protocol: "vless",
			URI:      "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.cloudflare.com&fp=chrome&pbk=abcdefghijklmnopqrstuv&sid=abcd1234&type=tcp#HK",
		},
		{
			Name:     "SS-1",
			Protocol: "shadowsocks",
			URI:      "ss://MjAyMi1ibGFrZTMtYWVzLTEyOC1nY206QUFBQUFBQUFBQUFBQUFBQUFBQUE=@5.6.7.8:8388#SS1",
		},
		{
			Name:     "mieru-1",
			Protocol: "mieru",
			URI:      "mierus://u:p@9.9.9.9?profile=x&port=443&protocol=TCP",
		},
	}
}

func TestPlainBase64(t *testing.T) {
	b64 := PlainBase64(sampleNodes())
	if b64 == "" {
		t.Fatal("empty b64")
	}
	raw := PlainRaw(sampleNodes())
	if !strings.Contains(raw, "vless://") || !strings.Contains(raw, "ss://") {
		t.Fatalf("raw missing uris: %s", raw)
	}
}

func TestClashSplit(t *testing.T) {
	yml := ClashSplit(sampleNodes(), Options{Username: "alice", Panel: "nft"})
	for _, want := range []string{
		"mixed-port: 7890",
		"type: vless",
		"type: ss",
		"type: mieru",
		"listen: 0.0.0.0:1053",
		"GEOSITE,private,DIRECT",
		"GEOSITE,cn,DIRECT",
		"GEOIP,CN,DIRECT",
		"MATCH,PROXY",
		"multiplexing: MULTIPLEXING_OFF",
		`name: "PROXY"`,
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("missing %q in:\n%s", want, yml)
		}
	}
}

func TestMieruToClash(t *testing.T) {
	yml := URIToClashProxy("mierus://alice:secret@1.2.3.4?port=26582&protocol=TCP&protocol=UDP&profile=p1", "PV2")
	for _, want := range []string{
		`name: "PV2"`,
		"type: mieru",
		`server: "1.2.3.4"`,
		"port: 26582",
		"transport: TCP",
		`username: "alice"`,
		`password: "secret"`,
		"multiplexing: MULTIPLEXING_OFF",
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("missing %q in:\n%s", want, yml)
		}
	}
}

func TestClashGlobalNoCN(t *testing.T) {
	yml := ClashGlobal(sampleNodes(), Options{Username: "bob"})
	if strings.Contains(yml, "GEOSITE,cn,DIRECT") || strings.Contains(yml, "GEOIP,CN,DIRECT") {
		t.Fatal("global should not have CN direct")
	}
	if !strings.Contains(yml, "MATCH,PROXY") {
		t.Fatal("need MATCH PROXY")
	}
	if !strings.Contains(yml, "1.1.1.1") {
		t.Fatal("want cloudflare dns")
	}
	if !strings.Contains(yml, "type: mieru") {
		t.Fatal("global should include mieru proxies")
	}
	if !strings.Contains(yml, "respect-rules: true") {
		t.Fatal("global should respect-rules for anti-leak DNS")
	}
}

func TestShadowrocketConf(t *testing.T) {
	conf := ShadowrocketConf(sampleNodes(), Options{Username: "fs", Panel: "nft"})
	for _, want := range []string{
		"[General]",
		"[Proxy]",
		"[Rule]",
		"FINAL,Proxy",
		"vless,",
		"ss,",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("missing %q\n%s", want, conf)
		}
	}
}

func TestURIToClashVLESS(t *testing.T) {
	uri := "vless://u@1.2.3.4:443?security=reality&sni=a.com&pbk=pk&sid=ab&type=tcp&flow=xtls-rprx-vision#n"
	y := URIToClashProxy(uri, "n1")
	if !strings.Contains(y, "type: vless") || !strings.Contains(y, "reality-opts") {
		t.Fatal(y)
	}
}
