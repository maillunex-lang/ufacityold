package battle

import (
	"encoding/json"
	"testing"
)

func runBattle(t *testing.T, defs []UnitDef, cfg Energy) Outcome {
	t.Helper()
	o, err := Run(defs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestDeterministic(t *testing.T) {
	a, _ := json.Marshal(runBattle(t, PrototypeSquads(), DefaultEnergy))
	b, _ := json.Marshal(runBattle(t, PrototypeSquads(), DefaultEnergy))
	if string(a) != string(b) {
		t.Fatal("бой недетерминирован")
	}
}

func TestPrototypeOutcome(t *testing.T) {
	o := runBattle(t, PrototypeSquads(), DefaultEnergy)
	t.Logf("итог: %+v, событий %d", o.Result, len(o.Events))
	if o.Result.DurationMs > TimeLimitMs || o.Result.DurationMs <= 0 {
		t.Fatal("длительность")
	}
	skills := 0
	for _, e := range o.Events {
		if e.Type == "skill" {
			skills++
		}
	}
	t.Logf("главных способностей: %d", skills)
	if skills < 3 {
		t.Fatal("в демо-бою должны срабатывать главные способности")
	}
	if o.Result.Winner != "player" {
		t.Fatalf("в прототипе игрок должен выигрывать, получили %+v", o.Result)
	}
}

// Инварианты: время не убывает, здоровье в пределах, мёртвые не действуют, после смерти нет событий от юнита.
func TestInvariants(t *testing.T) {
	o := runBattle(t, PrototypeSquads(), DefaultEnergy)
	maxhp := map[string]int{}
	for _, u := range o.Units {
		maxhp[u.ID] = u.MaxHP
	}
	dead := map[string]int{}
	last := 0
	for _, e := range o.Events {
		if e.T < last {
			t.Fatal("время убывает")
		}
		last = e.T
		if e.Type == "death" {
			dead[e.Src] = e.T
			continue
		}
		if dt, ok := dead[e.Src]; ok && e.T > dt {
			t.Fatalf("%s действует после смерти (%d > %d)", e.Src, e.T, dt)
		}
		for _, h := range e.Targets {
			if h.HP < 0 || h.HP > maxhp[h.Unit] || h.Amount < 0 {
				t.Fatalf("недопустимые значения %+v", h)
			}
		}
		for id, en := range e.Energy {
			if en < 0 || en > int(DefaultEnergy.Max) {
				t.Fatalf("энергия %s=%d вне диапазона", id, en)
			}
		}
	}
}

// Энергия в начале боя нулевая; главная способность тратит весь запас; её урон энергию владельцу не даёт.
func TestEnergyRules(t *testing.T) {
	defs := []UnitDef{
		{ID: "p0", Name: "Воин", Role: Fighter, Side: "player", Slot: 0, MaxHP: 100000, Attack: 100, IntervalMs: 1000, Melee: true},
		{ID: "e0", Name: "Мишень", Role: Tank, Side: "enemy", Slot: 0, MaxHP: 100000, Attack: 0, IntervalMs: 100000},
	}
	cfg := Energy{Max: 100, PerDealt: 0.1, PerTaken: 0, PerHealed: 0} // 0.1 энергии за единицу урона: 100 урона = 10 энергии, 10 ударов до полного
	o := runBattle(t, defs, cfg)
	skills, attacksBefore := 0, 0
	for _, e := range o.Events {
		if e.Src != "p0" {
			continue
		}
		switch e.Type {
		case "attack":
			if skills == 0 {
				attacksBefore++
			}
		case "skill":
			skills++
			if v, ok := e.Energy["p0"]; !ok || v != 0 {
				t.Fatalf("после главной способности энергия владельца должна быть 0, есть %v", e.Energy)
			}
		}
	}
	if attacksBefore != 10 {
		t.Fatalf("ожидалось 10 обычных атак до первой способности, было %d", attacksBefore)
	}
	if skills == 0 {
		t.Fatal("главная способность не сработала")
	}
}

func TestOverhealGivesNoEnergy(t *testing.T) {
	// Целитель лечит полностью здоровых союзников: фактическое лечение 0 → энергия получателей не растёт.
	defs := []UnitDef{
		{ID: "p0", Name: "Страж", Role: Tank, Side: "player", Slot: 0, MaxHP: 1000, Attack: 0, IntervalMs: 100000},
		{ID: "p1", Name: "Целитель", Role: Healer, Side: "player", Slot: 4, MaxHP: 1000, Attack: 100, IntervalMs: 1000},
		{ID: "e0", Name: "Мишень", Role: Tank, Side: "enemy", Slot: 0, MaxHP: 1000000, Attack: 0, IntervalMs: 100000},
	}
	cfg := Energy{Max: 20, PerDealt: 100, PerTaken: 0, PerHealed: 100}
	o := runBattle(t, defs, cfg)
	for _, e := range o.Events {
		if e.Type == "skill" {
			if en, ok := e.Energy["p0"]; ok && en != 0 {
				t.Fatalf("избыточное лечение дало энергию: %d", en)
			}
		}
	}
}

func TestTimeoutAttackerLoses(t *testing.T) {
	defs := []UnitDef{
		{ID: "p0", Name: "Слабый", Role: Fighter, Side: "player", Slot: 0, MaxHP: 1000, Attack: 1, IntervalMs: 1000, Melee: true},
		{ID: "e0", Name: "Стена", Role: Tank, Side: "enemy", Slot: 0, MaxHP: 1000000, Attack: 0, IntervalMs: 100000},
	}
	o := runBattle(t, defs, DefaultEnergy)
	if o.Result.Winner != "enemy" || o.Result.Reason != "timeout" || o.Result.DurationMs != TimeLimitMs {
		t.Fatalf("%+v", o.Result)
	}
}
