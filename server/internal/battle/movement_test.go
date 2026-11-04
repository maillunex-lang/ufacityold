package battle

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
)

type replayMotion struct {
	start int
	move  Movement
}

// Независимое проигрывание публичной записи: координаты на время события,
// непрерывность, ограничение скорости, дальность всех ударов и финальное состояние.
func checkMovementReplay(t *testing.T, o Outcome) {
	t.Helper()
	defs := map[string]UnitDef{}
	positions := map[string]Position{}
	motions := map[string]replayMotion{}
	dead := map[string]bool{}
	travelled := map[string]float64{}
	at := func(id string, time int) Position {
		m, ok := motions[id]
		if !ok {
			return positions[id]
		}
		weight := min(1.0, max(0.0, float64(time-m.start)/float64(m.move.DurationMs)))
		return Position{
			m.move.From.X + int(math.Round(float64(m.move.To.X-m.move.From.X)*weight)),
			m.move.From.Z + int(math.Round(float64(m.move.To.Z-m.move.From.Z)*weight)),
		}
	}
	for _, d := range o.Units {
		defs[d.ID], positions[d.ID] = d, d.Position
	}
	last := 0
	for _, e := range o.Events {
		if e.T < last || e.T > o.Result.DurationMs {
			t.Fatalf("время события: %+v", e)
		}
		last = e.T
		if dead[e.Src] {
			t.Fatalf("событие после смерти: %+v", e)
		}
		if e.Type == "death" {
			positions[e.Src] = at(e.Src, e.T)
			delete(motions, e.Src)
			dead[e.Src] = true
			continue
		}
		if e.Type == "move" {
			if e.Move == nil || !defs[e.Src].Melee || dead[e.Move.Target] {
				t.Fatalf("недопустимое движение: %+v", e)
			}
			m := *e.Move
			if m.From != at(e.Src, e.T) || m.DurationMs <= 0 || e.T+m.DurationMs > o.Result.DurationMs {
				t.Fatalf("разрыв движения: %+v", e)
			}
			limit := float64(defs[e.Src].RunSpeed) * float64(m.DurationMs) / 1000
			travelled[e.Src] += distance(m.From, m.To)
			if distance(m.From, m.To) > limit+2 {
				t.Fatalf("превышена скорость: %+v", e)
			}
			if travelled[e.Src] > float64(defs[e.Src].RunSpeed)*float64(e.T+m.DurationMs)/1000+0.001 {
				t.Fatalf("суммарный путь превысил бюджет скорости: %+v", e)
			}
			motions[e.Src] = replayMotion{e.T, m}
			continue
		}
		for _, hit := range e.Targets {
			if distance(at(e.Src, e.T), at(hit.Unit, e.T)) > float64(defs[e.Src].AttackRange) {
				t.Fatalf("удар вне дальности: %+v", e)
			}
		}
	}
	for id, want := range o.Positions {
		if got := at(id, o.Result.DurationMs); got != want {
			t.Fatalf("позиция %s: %v != %v", id, got, want)
		}
	}
}

func stationaryTarget(id string, slot int) UnitDef {
	return UnitDef{ID: id, Side: "enemy", Slot: slot, Role: Tank, MaxHP: 1000000, IntervalMs: 100000}
}

func TestMovementReplayAndInputUnchanged(t *testing.T) {
	defs := PrototypeSquads()
	before := append([]UnitDef(nil), defs...)
	o := runBattle(t, defs, DefaultEnergy)
	if !reflect.DeepEqual(before, defs) {
		t.Fatal("расчёт изменил исходные параметры")
	}
	checkMovementReplay(t, o)
	want, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		got, err := json.Marshal(runBattle(t, defs, DefaultEnergy))
		if err != nil || string(want) != string(got) {
			t.Fatal("позиции/события недетерминированы", err)
		}
	}
}

func TestMeleeApproachesOnceAndRetargetsAfterDeath(t *testing.T) {
	defs := []UnitDef{
		{ID: "p", Side: "player", Role: Fighter, MaxHP: 100000, Attack: 100, IntervalMs: 500, Melee: true},
		stationaryTarget("a", 0), stationaryTarget("b", 2),
	}
	defs[1].MaxHP = 400
	o := runBattle(t, defs, Energy{Max: 1000000})
	checkMovementReplay(t, o)
	firstAttack, death, movesBefore, movesAfter := 0, 0, 0, 0
	for _, e := range o.Events {
		if e.Type == "death" && e.Src == "a" {
			death = e.T
		}
		if e.Src != "p" {
			continue
		}
		if e.Type == "attack" {
			if firstAttack == 0 {
				firstAttack = e.T
			}
			want := "a"
			if death > 0 {
				want = "b"
			}
			if e.Targets[0].Unit != want {
				t.Fatalf("цель не удерживается: %+v", e)
			}
		}
		if e.Type == "move" {
			if death == 0 {
				movesBefore++
				if firstAttack > 0 {
					t.Fatal("ближний возвращается/бежит повторно до гибели цели")
				}
				if e.Move.Target != "a" {
					t.Fatal("сменилась живая цель")
				}
			} else {
				movesAfter++
			}
		}
	}
	if firstAttack <= 500 || death == 0 || movesBefore == 0 || movesAfter == 0 {
		t.Fatalf("нет сближения/переключения: %+v", o.Result)
	}
}

func TestSeveralAttackersUseDistinctApproaches(t *testing.T) {
	for _, meleeTarget := range []bool{false, true} {
		t.Run(fmt.Sprint("цель_ближняя_", meleeTarget), func(t *testing.T) {
			defs := []UnitDef{stationaryTarget("target", 0)}
			defs[0].Melee = meleeTarget
			for slot := range 5 {
				defs = append(defs, UnitDef{ID: fmt.Sprint("p", slot), Side: "player", Slot: slot, Role: Fighter,
					MaxHP: 1000000, Attack: 1, IntervalMs: 1000, Melee: true})
			}
			o := runBattle(t, defs, Energy{Max: 1000000})
			checkMovementReplay(t, o)
			attacked := map[string]bool{}
			for _, e := range o.Events {
				if e.Type == "attack" {
					attacked[e.Src] = true
				}
			}
			for i := range 5 {
				id := fmt.Sprint("p", i)
				if !attacked[id] {
					t.Fatalf("%s не добрался до цели", id)
				}
				for j := 0; j < i; j++ {
					if distance(o.Positions[id], o.Positions[fmt.Sprint("p", j)]) < 500 {
						t.Fatalf("атакующие слиплись: %v", o.Positions)
					}
				}
			}
		})
	}
}

func TestTravelTimeUsesSpeedAndRange(t *testing.T) {
	for _, tc := range []struct{ speed, reach int }{{1000, 1100}, {3000, 1100}, {1000, 2200}} {
		t.Run(fmt.Sprintf("скорость_%d_дальность_%d", tc.speed, tc.reach), func(t *testing.T) {
			defs := []UnitDef{
				{ID: "p", Side: "player", Role: Fighter, MaxHP: 1000000, Attack: 1, IntervalMs: 100,
					Melee: true, RunSpeed: tc.speed, AttackRange: tc.reach, Position: Position{-10000, 100}},
				stationaryTarget("e", 0),
			}
			defs[1].Position = Position{10000, 100}
			o := runBattle(t, defs, DefaultEnergy)
			checkMovementReplay(t, o)
			distanceToTravel := 20000 - int(float64(tc.reach)*0.9)
			want := int(math.Ceil(float64(distanceToTravel)/float64(tc.speed*TickMs/1000))) * TickMs
			for _, e := range o.Events {
				if e.Src == "p" && e.Type == "attack" {
					if e.T != want {
						t.Fatalf("перебежка %d мс, ожидалось %d", e.T, want)
					}
					return
				}
			}
			t.Fatal("ближний не атаковал после перебежки")
		})
	}
}

func TestDeathDuringApproachStopsMovement(t *testing.T) {
	defs := []UnitDef{
		{ID: "p", Side: "player", Role: Fighter, MaxHP: 1, Attack: 100, IntervalMs: 100,
			Melee: true, RunSpeed: 500},
		{ID: "e", Side: "enemy", Role: Archer, MaxHP: 100000, Attack: 1, IntervalMs: 100},
	}
	o := runBattle(t, defs, DefaultEnergy)
	checkMovementReplay(t, o)
	if o.Result.Winner != "enemy" || o.Result.DurationMs != TickMs {
		t.Fatal("боец не погиб во время подхода")
	}
	for _, e := range o.Events {
		if e.Src == "p" && (e.Type == "attack" || e.Type == "skill") {
			t.Fatal("боец атаковал до прибытия")
		}
	}
}

func TestSlowMovementDoesNotStallOnRounding(t *testing.T) {
	defs := []UnitDef{
		{ID: "p", Side: "player", Role: Fighter, MaxHP: 100000, Attack: 1, IntervalMs: 100,
			Melee: true, RunSpeed: 1, Position: Position{-2000, 100}},
		stationaryTarget("e", 0),
	}
	defs[1].Position = Position{2000, 100}
	o := runBattle(t, defs, DefaultEnergy)
	checkMovementReplay(t, o)
	travelled := o.Positions["p"].X - defs[0].Position.X
	if travelled < 89 || travelled > 90 {
		t.Fatalf("медленное движение за 90 с: %d вместо ~90", travelled)
	}
}

func TestMovingTargetsAndDeaths(t *testing.T) {
	defs := []UnitDef{
		{ID: "p", Side: "player", Role: Fighter, MaxHP: 100000, Attack: 100, IntervalMs: 500, Melee: true},
		{ID: "e", Side: "enemy", Role: Fighter, MaxHP: 1000, Attack: 100, IntervalMs: 500, Melee: true},
	}
	o := runBattle(t, defs, DefaultEnergy)
	checkMovementReplay(t, o)
	if o.Result.Reason != "elimination" {
		t.Fatal("взаимное сближение не завершилось боем", o.Result)
	}
}

func TestRangedStaysAndRespectsRange(t *testing.T) {
	defs := []UnitDef{
		{ID: "p", Side: "player", Role: Archer, MaxHP: 1000, Attack: 100, IntervalMs: 100, AttackRange: 500},
		stationaryTarget("e", 0),
	}
	o := runBattle(t, defs, DefaultEnergy)
	checkMovementReplay(t, o)
	if len(o.Events) != 0 || o.Result.Reason != "timeout" {
		t.Fatal("дальний подбежал или атаковал вне дальности")
	}
	defs[0].AttackRange = 4000
	o = runBattle(t, defs, DefaultEnergy)
	checkMovementReplay(t, o)
	if len(o.Events) == 0 {
		t.Fatal("дальний не атакует в пределах дальности")
	}
	for _, e := range o.Events {
		if e.Type == "move" {
			t.Fatal("дальний перемещается")
		}
	}
}

func TestMeleeSkillWaitsForRangeWithoutSpendingEnergy(t *testing.T) {
	defs := []UnitDef{
		{ID: "p", Side: "player", Role: Fighter, MaxHP: 100000, Attack: 100, IntervalMs: 100, Melee: true, RunSpeed: 500},
		{ID: "e", Side: "enemy", Role: Archer, MaxHP: 1000000, Attack: 1, IntervalMs: 100},
	}
	o := runBattle(t, defs, Energy{Max: 100, PerTaken: 100})
	checkMovementReplay(t, o)
	for _, e := range o.Events {
		if e.Src == "p" && (e.Type == "attack" || e.Type == "skill") {
			if e.Type != "skill" || e.T < 4000 || e.Energy["p"] != 0 {
				t.Fatalf("энергия/способность до сближения: %+v", e)
			}
			return
		}
	}
	t.Fatal("главная способность не применена после сближения")
}
