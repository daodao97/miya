package agent

import (
	"testing"

	"miya/internal/model"
)

func TestCommandCatalogMergesWithoutChangingCache(t *testing.T) {
	host := []model.ComposerChoice{{Name: "model", Detail: "Host model"}}
	native := []model.ComposerChoice{{Name: "MODEL", Detail: "Native model"}, {Name: "custom"}, {Name: "custom"}, {Name: ""}}
	merged := MergeCommands(host, native)
	if len(merged) != 2 || merged[0].Detail != "Host model" || merged[1].Name != "custom" {
		t.Fatalf("bad merge: %+v", merged)
	}
	merged[0].Detail = "changed"
	if host[0].Detail != "Host model" || len(native) != 4 {
		t.Fatal("catalog merge mutated its source")
	}
	catalog := CommandCatalog{Native: true, Choices: merged}
	if catalog.TakesCommand("model x") || !catalog.TakesCommand("custom x") {
		t.Fatal("host/native routing confused")
	}
	if got := catalog.Filter("CUS"); len(got) != 1 {
		t.Fatalf("case-insensitive filtering failed: %+v", got)
	}
}
