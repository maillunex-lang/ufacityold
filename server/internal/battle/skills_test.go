package battle

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"
)

func skillFixture() []UnitDef {
	return []UnitDef{
		{ID: "p", Role: Mage, Side: "player", MaxHP: 100000, Attack: 100, IntervalMs: 100000,
			AutoSkills: [2]AutoSkill{
				{ID: "one", UnlockLevel: 10, CooldownMs: 1000, Kind: "damage", Target: CurrentEnemy, PowerPermille: 1000},
				{ID: "two", UnlockLevel: 20, CooldownMs: 1500, Kind: "damage", Target: AllEnemies, PowerPermille: 1000},
			}, Passive: &Passive{ID: "passive", UnlockLevel: 40, AttackPermille: 100, HPPermille: 200}},
		stationaryTarget("e", 0),
	}
}

func newBattle(t *testing.T, defs []UnitDef, energy Energy) *Battle {
	t.Helper()
	b, err := New(defs, Config{Energy: energy})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSkillUnlockBoundaries(t *testing.T) {
	for _, level := range []int{1, 9, 10, 19, 20, 39, 40} {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			defs := skillFixture()
			defs[0].Level = level
			o := runBattle(t, defs, Energy{Max: 1000000})
			checkMovementReplay(t, o)
			counts := map[string]int{}
			for _, e := range o.Events {
				if e.Src == "p" && (e.Type == "auto_skill" || e.Type == "passive") {
					counts[e.SkillID]++
				}
			}
			for _, skill := range []struct {
				id       string
				unlocked bool
			}{{"one", level >= 10}, {"two", level >= 20}, {"passive", level >= 40}} {
				if (counts[skill.id] > 0) != skill.unlocked {
					t.Fatalf("уровень %d: %v", level, counts)
				}
			}
			if level >= 40 {
				if counts["passive"] != 1 || o.Units[0].MaxHP != 120000 || o.Units[0].Attack != 110 || !o.Units[0].PassiveActive {
					t.Fatal("пассив применён неверно")
				}
			} else if o.Units[0].PassiveActive || o.Units[0].MaxHP != 100000 || o.Units[0].Attack != 100 {
				t.Fatal("пассив до открытия")
			}
		})
	}
}

func TestConfiguredUnlocksAndHighestUpgrade(t *testing.T) {
	defs := skillFixture()
	defs[0].Level = 7
	defs[0].AutoSkills[0].UnlockLevel = 7
	defs[0].AutoSkills[0].Upgrades = []SkillUpgrade{{Level: 9, PowerPermille: 3000}, {Level: 8, PowerPermille: 2000}}
	defs[0].Passive.UnlockLevel = 7
	defs[0].Passive.Upgrades = []PassiveBonus{{Level: 9, AttackPermille: 300}, {Level: 8, AttackPermille: 200}}
	for _, tc := range []struct{ level, attack, damage int }{{6, 100, 0}, {7, 110, 110}, {8, 120, 240}, {9, 130, 390}} {
		defs[0].Level = tc.level
		o := runBattle(t, defs, Energy{Max: 1000000})
		if o.Units[0].Attack != tc.attack {
			t.Fatalf("уровень %d: атака %d", tc.level, o.Units[0].Attack)
		}
		amount := 0
		for _, e := range o.Events {
			if e.Type == "auto_skill" && e.SkillID == "one" {
				amount = e.Targets[0].Amount
				break
			}
		}
		if amount != tc.damage {
			t.Fatalf("уровень %d: урон %d, ожидался %d", tc.level, amount, tc.damage)
		}
	}
}

func TestPassiveIsAppliedOnceWithoutStartingEnergy(t *testing.T) {
	defs := skillFixture()
	defs[0].Level = 40
	b := newBattle(t, defs, DefaultEnergy)
	for range 20 {
		s := b.Snapshot().Units[0]
		if s.MaxHP != 120000 || s.Attack != 110 {
			t.Fatal("пассив накопился повторно", s)
		}
		if b.t == 0 && (s.HP != s.MaxHP || s.Energy != 0) {
			t.Fatal("неверное начальное здоровье/энергия", s)
		}
		b.Step()
	}
	if defs[0].MaxHP != 100000 || defs[0].Attack != 100 || defs[0].PassiveActive {
		t.Fatal("пассив изменил базовые определения")
	}
}

func TestRarityDoesNotScaleSkillsOrPassive(t *testing.T) {
	defs := skillFixture()
	defs[0].Level = 40
	baseline := runBattle(t, defs, DefaultEnergy)
	for _, rarity := range []string{"common", "rare", "epic", "legendary"} {
		defs[0].Rarity = rarity
		o := runBattle(t, defs, DefaultEnergy)
		if o.Units[0].MaxHP != baseline.Units[0].MaxHP || o.Units[0].Attack != baseline.Units[0].Attack || !reflect.DeepEqual(o.Events, baseline.Events) {
			t.Fatal("скрытый множитель редкости", rarity)
		}
	}
}

func TestIndependentCooldownsAndStablePriority(t *testing.T) {
	defs := skillFixture()
	defs[0].Level = 20
	defs[0].AutoSkills[1].CooldownMs = 1000
	defs[0].IntervalMs = 100
	b := newBattle(t, defs, Energy{Max: 1000000})
	var first, second []int
	for b.Snapshot().T < 4000 {
		for _, e := range b.Step() {
			if e.Type != "auto_skill" {
				continue
			}
			if e.SkillSlot == 1 {
				first = append(first, e.T)
			} else {
				second = append(second, e.T)
			}
		}
	}
	if !reflect.DeepEqual(first, []int{1000, 2000, 3000, 4000}) || !reflect.DeepEqual(second, []int{1100, 2100, 3100}) {
		t.Fatal("перезарядки зависят от обычных атак или друг от друга", first, second)
	}
}

func TestAutoDamageGeneratesActualDamageEnergy(t *testing.T) {
	defs := skillFixture()
	defs[0].Level = 10
	defs[1].MaxHP = 50 // фактический урон меньше силы навыка
	o := runBattle(t, defs, Energy{Max: 1000, PerDealt: 1, PerTaken: 1})
	for _, e := range o.Events {
		if e.Type == "auto_skill" {
			if e.Targets[0].Amount != 50 || e.Energy["p"] != 50 || e.Energy["e"] != 50 {
				t.Fatal("энергия автонавыка не по фактическому урону", e)
			}
			return
		}
	}
	t.Fatal("нет автонавыка")
}

func TestAutoHealingEnergyAndOverheal(t *testing.T) {
	for _, self := range []bool{false, true} {
		defs := skillFixture()
		defs[0].Level, defs[0].Role = 10, Healer
		defs[0].AutoSkills[0].Kind, defs[0].AutoSkills[0].Target = "heal", LowestAlly
		if !self {
			defs = append(defs, UnitDef{ID: "ally", Role: Tank, Side: "player", Slot: 1, MaxHP: 1000, IntervalMs: 100000})
		}
		b := newBattle(t, defs, Energy{Max: 1000, PerHealed: 1})
		// Без ранения навык ждёт, не расходует перезарядку и не даёт энергию.
		for b.t < 1100 {
			b.Step()
		}
		if b.units[0].autoAt[0] != 1000 || b.units[0].energy != 0 {
			t.Fatal("избыточное лечение потратило перезарядку или дало энергию")
		}
		target := b.units[0]
		if !self {
			target = b.units[2]
		}
		target.hp -= 30
		var heal *Event
		for _, e := range b.Step() {
			if e.Type == "auto_skill" {
				copy := e
				heal = &copy
			}
		}
		if heal == nil || len(heal.Targets) != 1 || heal.Targets[0].Unit != target.ID || heal.Targets[0].Amount != 30 || heal.Energy["p"] != 30 {
			t.Fatalf("неверное лечение/энергия: %+v", heal)
		}
		if target.energy != 30 {
			t.Fatal("получатель не получил энергию или самолечение учтено дважды")
		}
		if b.units[0].autoAt[0] != 2200 {
			t.Fatal("перезарядка не от фактического применения")
		}
	}
}

func TestAutoTargetRulesAndRange(t *testing.T) {
	defs := skillFixture()
	defs[0].Level, defs[0].AttackRange = 20, 6000
	defs[1].MaxHP = 2000
	defs = append(defs, UnitDef{ID: "back", Role: Tank, Side: "enemy", Slot: 2, MaxHP: 1000, IntervalMs: 100000},
		UnitDef{ID: "far", Role: Tank, Side: "enemy", Slot: 3, MaxHP: 500, IntervalMs: 100000, Position: Position{100000, 1000}})
	defs[0].AutoSkills[0].Target = LowestEnemy
	b := newBattle(t, defs, Energy{Max: 1000000})
	for b.t < 1500 {
		for _, e := range b.Step() {
			if e.Type != "auto_skill" {
				continue
			}
			if e.SkillSlot == 1 && (len(e.Targets) != 1 || e.Targets[0].Unit != "back") {
				t.Fatal("неверная слабая цель", e)
			}
			if e.SkillSlot == 2 && len(e.Targets) != 2 {
				t.Fatal("неверный состав массовой атаки", e)
			}
			for _, h := range e.Targets {
				if h.Unit == "far" {
					t.Fatal("удар вне дальности")
				}
			}
		}
	}
	// При отсутствии цели в дальности таймер не расходуется и боец не перемещается.
	defs[0].AttackRange = 100
	b = newBattle(t, defs, Energy{Max: 1000000})
	for b.t < 3000 {
		if events := b.Step(); len(events) != 0 {
			t.Fatal("дальний боец действовал вне дальности", events)
		}
	}
	if b.units[0].autoAt != ([2]int{1000, 1500}) {
		t.Fatal("потрачена перезарядка без цели")
	}
}

func TestLowestAllyUsesHealthFractionAndStableTie(t *testing.T) {
	defs := skillFixture()
	defs[0].Level, defs[0].Role = 10, Healer
	defs[0].AutoSkills[0].Kind, defs[0].AutoSkills[0].Target = "heal", LowestAlly
	defs = append(defs, UnitDef{ID: "a", Role: Tank, Side: "player", Slot: 1, MaxHP: 2000, IntervalMs: 100000},
		UnitDef{ID: "b", Role: Tank, Side: "player", Slot: 2, MaxHP: 1000, IntervalMs: 100000})
	b := newBattle(t, defs, Energy{Max: 1000000})
	b.units[2].hp, b.units[3].hp = 1000, 600 // 50% против 60%, хотя абсолютное здоровье больше
	for b.t < 1000 {
		for _, e := range b.Step() {
			if e.Type == "auto_skill" && e.Targets[0].Unit != "a" {
				t.Fatal("лечится не самая раненая доля здоровья", e)
			}
		}
	}
	// Равные доли: порядок входного отряда, без случайности.
	b = newBattle(t, defs, Energy{Max: 1000000})
	b.units[2].hp, b.units[3].hp = 1000, 500
	if got := b.skillTargets(b.units[0], b.units[0].AutoSkills[0]); len(got) != 1 || got[0].ID != "a" {
		t.Fatal("неустойчивое разрешение равенства")
	}
}

func TestMeleeAutoWaitsForApproachAndDeadUnitsCannotCast(t *testing.T) {
	defs := skillFixture()
	defs[0].Melee, defs[0].Role, defs[0].Level, defs[0].RunSpeed = true, Fighter, 10, 500
	defs[0].AutoSkills[0].CooldownMs = 100
	o := runBattle(t, defs, Energy{Max: 1000000})
	checkMovementReplay(t, o)
	found := false
	for _, e := range o.Events {
		if e.Type == "auto_skill" && e.Src == "p" {
			if e.T < 4000 {
				t.Fatal("навык до подхода")
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("автонавык не применён после подхода")
	}
	defs[0].MaxHP = 1
	defs[1].Attack, defs[1].IntervalMs = 1, 100
	b := newBattle(t, defs, Energy{Max: 1000000})
	events := b.Step()
	if !b.Done() {
		t.Fatal("герой не погиб")
	}
	for _, e := range events {
		if e.Type == "auto_skill" {
			t.Fatal("мёртвый/не подошедший герой применил навык")
		}
	}
}

func TestInvalidSkillConfigurationIsRejected(t *testing.T) {
	mutations := []func([]UnitDef){
		func(d []UnitDef) { d[0].AutoSkills[0].CooldownMs = 0 },
		func(d []UnitDef) { d[0].AutoSkills[0].Kind = "unknown" },
		func(d []UnitDef) { d[0].AutoSkills[0].Target = Self },
		func(d []UnitDef) { d[0].AutoSkills[0].PowerPermille = -1 },
		func(d []UnitDef) { d[0].AutoSkills[0].UnlockLevel = 0 },
		func(d []UnitDef) { d[0].AutoSkills[0].ID = "" },
		func(d []UnitDef) { d[0].AutoSkills[1].ID = "one" },
		func(d []UnitDef) { d[0].AutoSkills[0].Upgrades = []SkillUpgrade{{20, 1000}, {20, 2000}} },
		func(d []UnitDef) { d[0].Passive.HPPermille = -1 },
		func(d []UnitDef) { d[0].Passive.ID = "one" },
		func(d []UnitDef) { d[0].Level = -1 },
		func(d []UnitDef) { d[0].MaxHP = maxStat; d[0].Level = 40 },
		func(d []UnitDef) { d[1].Side = "invalid" },
		func(d []UnitDef) { d[1].ID = "p" },
	}
	for i, mutate := range mutations {
		defs := skillFixture()
		mutate(defs)
		if _, err := New(defs, Config{Energy: DefaultEnergy}); err == nil {
			t.Fatalf("приняты неверные данные %d", i)
		}
	}
	for _, max := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, err := New(skillFixture(), Config{Energy: Energy{Max: max}}); err == nil {
			t.Fatal("принята неверная энергия")
		}
	}
}

func TestIncrementalMatchesFullRunAndCompletedStepIsIdempotent(t *testing.T) {
	defs := PrototypeSquads()
	want := runBattle(t, defs, DefaultEnergy)
	b := newBattle(t, defs, DefaultEnergy)
	if _, ok := b.Outcome(); ok {
		t.Fatal("незавершённый бой выдан за результат")
	}
	var events []Event
	for !b.Done() {
		events = append(events, b.Step()...)
	}
	got, ok := b.Outcome()
	if !ok || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(events, got.Events) {
		t.Fatal("пошаговый расчёт отличается от полного")
	}
	before := b.Snapshot()
	for range 10 {
		if len(b.Step()) != 0 {
			t.Fatal("новые события после результата")
		}
	}
	if !reflect.DeepEqual(before, b.Snapshot()) {
		t.Fatal("повтор шага изменил итог/характеристики")
	}
}

func TestConcurrentBattlesDoNotShareTimersOrPassiveBonuses(t *testing.T) {
	defs := PrototypeSquads()
	want, err := json.Marshal(runBattle(t, defs, DefaultEnergy))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(runBattle(t, defs, DefaultEnergy))
			if err != nil || string(got) != string(want) {
				t.Fatal("бои разделяют состояние", err)
			}
		})
	}
}

func TestBattlePinsNestedConfigurationAndSnapshotsCannotMutateIt(t *testing.T) {
	defs := skillFixture()
	defs[0].Level = 40
	defs[0].AutoSkills[0].Upgrades = []SkillUpgrade{{Level: 30, PowerPermille: 2000}}
	defs[0].Passive.Upgrades = []PassiveBonus{{Level: 30, AttackPermille: 200}}
	// Для пассива улучшение должно быть после открытия.
	defs[0].Passive.UnlockLevel = 20
	cfg := Config{Energy: DefaultEnergy, BalanceVersion: "balance-A"}
	before, _ := json.Marshal(defs)
	want := newBattle(t, defs, DefaultEnergy)
	b, err := New(defs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(defs)
	if string(before) != string(after) {
		t.Fatal("New изменил исходные определения")
	}
	defs[0].AutoSkills[0].Upgrades[0].PowerPermille = 3000
	defs[0].Passive.Upgrades[0].AttackPermille = 300
	cfg.BalanceVersion = "balance-B"
	newer, err := New(defs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if newer.Snapshot().Balance != "balance-B" || newer.Snapshot().Units[0].Attack != 130 || b.Snapshot().Units[0].Attack != 120 {
		t.Fatal("баланс не закреплён")
	}
	for !b.Done() {
		batch := b.Step()
		want.Step()
		// Мутации возвращённых структур не должны менять живое состояние/историю.
		for i := range batch {
			if batch[i].Energy != nil {
				batch[i].Energy["p"] = -999
			}
			if len(batch[i].Targets) > 0 {
				batch[i].Targets[0].HP = -999
			}
			if batch[i].Move != nil {
				batch[i].Move.To.X = -999
			}
		}
		x := b.Snapshot()
		x.Units[0].HP = -999
		if x.Result != nil {
			x.Result.Winner = "invalid"
		}
	}
	got, _ := b.Outcome()
	baseline, _ := want.Outcome()
	baseline.Balance = "balance-A"
	if !reflect.DeepEqual(got, baseline) {
		t.Fatal("мутация конфигурации/снимка изменила начатый бой")
	}
	got.Units[0].AutoSkills[0].Upgrades[0].PowerPermille = -1
	got.Units[0].Passive.AttackPermille = -1
	got.Positions["p"] = Position{-1, -1}
	again, _ := b.Outcome()
	if !reflect.DeepEqual(again, baseline) {
		t.Fatal("результат разделяет память с ядром")
	}
}
