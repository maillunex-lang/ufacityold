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

func TestFactionFormationBonus(t *testing.T) {
	defs := PrototypeSquads()[:2]
	defs = append(defs,
		UnitDef{ID: "p2", Side: "player", Slot: 2, Role: Archer, Faction: FactionDawn, MaxHP: 1000, Attack: 100, IntervalMs: 1000, AttackRange: 1000, RunSpeed: 1000},
		UnitDef{ID: "p3", Side: "player", Slot: 3, Role: Mage, Faction: FactionDawn, MaxHP: 1000, Attack: 100, IntervalMs: 1000, AttackRange: 1000, RunSpeed: 1000},
		UnitDef{ID: "p4", Side: "player", Slot: 4, Role: Mage, Faction: FactionDawn, MaxHP: 1000, Attack: 100, IntervalMs: 1000, AttackRange: 1000, RunSpeed: 1000},
		UnitDef{ID: "e0", Side: "enemy", Slot: 0, Role: Tank, Faction: FactionTide, MaxHP: 1000, Attack: 100, IntervalMs: 1000, AttackRange: 1000, RunSpeed: 1000})
	b, err := New(defs, Config{Energy: DefaultEnergy, BalanceVersion: BalanceVersion})
	if err != nil {
		t.Fatal(err)
	}
	if b.units[0].Attack != 72 || b.units[0].MaxHP != 5940 {
		t.Fatalf("3 героя: attack=%d hp=%d", b.units[0].Attack, b.units[0].MaxHP)
	}
}
