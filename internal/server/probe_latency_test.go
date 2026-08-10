package server

import (
	"strings"
	"testing"

	"nft/internal/db"
)

func TestClassifyProbeFail(t *testing.T) {
	cases := []struct {
		raw, status string
		wantSub     string
	}{
		{"connection refused", db.ProxyDeployReady, "无监听"},
		{"connection refused", db.ProxyDeployError, "部署失败"},
		{"node 3 not connected", "", "节点离线"},
		{"probe timeout", "", "超时"},
	}
	for _, c := range cases {
		got := classifyProbeFail(c.raw, c.status, 443)
		if !strings.Contains(got, c.wantSub) {
			t.Fatalf("raw=%q status=%q got=%q want substring %q", c.raw, c.status, got, c.wantSub)
		}
	}
}
