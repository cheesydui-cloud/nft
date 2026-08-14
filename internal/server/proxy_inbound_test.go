package server

import (
	"encoding/json"
	"testing"

	"nft/internal/db"
	"nft/internal/proxysvc"
)

func TestEnsureMieruTemplateClientKeepsWarehouseAccount(t *testing.T) {
	raw, _ := json.Marshal(proxysvc.MieruConfig{Username: "warehouse", Password: "sharepass"})
	got := ensureMieruTemplateClient(raw, nil)
	if len(got) != 1 || got[0].Username != "warehouse" || got[0].Password != "sharepass" {
		t.Fatalf("empty live list must keep template: %+v", got)
	}
	got = ensureMieruTemplateClient(raw, []proxysvc.InboundClient{{Username: "u1", Password: "p1"}})
	if len(got) != 2 || got[0].Username != "warehouse" {
		t.Fatalf("template should be prepended: %+v", got)
	}
	got = ensureMieruTemplateClient(raw, []proxysvc.InboundClient{{Username: "warehouse", Password: "sharepass"}})
	if len(got) != 1 {
		t.Fatalf("do not duplicate template: %+v", got)
	}
}

func TestOverlayProxyConfigForPublishEmptyMieruKeepsTemplate(t *testing.T) {
	raw, _ := json.Marshal(proxysvc.MieruConfig{Username: "warehouse", Password: "sharepass", ListenPort: 8964})
	s := &Server{}
	out, err := s.overlayProxyConfigForPublish(&db.ProxyService{ID: 0, Protocol: "mieru"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	var got proxysvc.MieruConfig
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "warehouse" || got.Password != "sharepass" {
		t.Fatalf("template lost: %+v", got)
	}
	if len(got.Users) == 1 && (got.Users[0].Name != "warehouse" || got.Users[0].Password != "sharepass") {
		t.Fatalf("template user overwritten: %+v", got.Users)
	}
}
