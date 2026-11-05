package battle

import "testing"

func TestPrototypeSquadsHaveDeterministicFactions(t *testing.T) {
	defs := PrototypeSquads()
	want := map[string]Faction{"p0": FactionDawn, "p1": FactionDawn, "p2": FactionTide, "p3": FactionGrove, "p4": FactionEmber, "e0": FactionTide, "e1": FactionTide, "e2": FactionGrove, "e3": FactionEmber, "e4": FactionDawn}
	for _, d := range defs {
		if d.Faction != want[d.ID] {
			t.Fatalf("%s: faction=%q want=%q", d.ID, d.Faction, want[d.ID])
		}
	}
}

func TestUnknownFactionRejected(t *testing.T) {
	defs := PrototypeSquads()
	defs[0].Faction = "unknown"
	if _, err := New(defs, Config{Energy: DefaultEnergy, BalanceVersion: BalanceVersion}); err == nil {
		t.Fatal("неизвестная фракция принята")
	}
}
