package routines

import "testing"

func TestRegistryHasAll(t *testing.T) {
	expected := []string{"idle", "miner", "trader", "rescue", "ai", "return_home"}
	for _, name := range expected {
		if _, ok := Registry[name]; !ok {
			t.Fatalf("missing routine %s", name)
		}
	}
	if len(Registry) < 30 {
		t.Fatalf("expected >=30 routines, got %d", len(Registry))
	}
}
