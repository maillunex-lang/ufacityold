package battle

import "testing"

func manualPair() []UnitDef {
	return []UnitDef{
		{ID: "p", Name: "Воин", Side: "player", Role: Fighter, Slot: 0, MaxHP: 8000, Attack: 250, IntervalMs: 200},
		{ID: "e", Name: "Страж", Side: "enemy", Role: Fighter, Slot: 0, MaxHP: 500000, Attack: 40, IntervalMs: 400},
	}
}

func countSkills(s *Battle, id string) int {
	n := 0
	for _, e := range s.events {
		if e.Type == "skill" && e.Src == id {
			n++
		}
	}
	return n
}

// D3: при выключенном авто главная способность игрока ждёт команду.
// До исправления оба прогона совпадают — команда ничего не меняет.
func TestManualMainSkillChangesBattleWhenAutoOff(t *testing.T) {
	with, err := New(manualPair(), Config{Energy: DefaultEnergy})
	if err != nil {
		t.Fatal(err)
	}
	without, err := New(manualPair(), Config{Energy: DefaultEnergy})
	if err != nil {
		t.Fatal(err)
	}
	with.SetPlayerAuto(false)
	without.SetPlayerAuto(false)
	asked := false
	for i := 0; i < 800 && !with.Done() && !without.Done(); i++ {
		if !asked {
			for _, u := range with.Snapshot().Units {
				if u.ID == "p" && u.Energy >= int(DefaultEnergy.Max) {
					if !with.RequestMainSkill("p") {
						t.Fatal("команда своего героя с полной энергией отклонена")
					}
					asked = true
				}
			}
		}
		with.Step()
		without.Step()
		if asked && countSkills(with, "p") > 0 && countSkills(without, "e") > 0 {
			break
		}
	}
	if !asked {
		t.Fatal("энергия игрока так и не заполнилась")
	}
	if countSkills(without, "p") != 0 {
		t.Fatalf("без авто и без команды главная способность игрока сработала %d раз", countSkills(without, "p"))
	}
	if countSkills(with, "p") == 0 {
		t.Fatal("команда не запустила главную способность")
	}
	if countSkills(without, "e") == 0 {
		t.Fatal("у врага при выключенном авто игрока главная способность должна оставаться автоматической")
	}
}

func TestForceAutoCastsPlayerSkillWithoutCommand(t *testing.T) {
	s, err := New(manualPair(), Config{Energy: DefaultEnergy})
	if err != nil {
		t.Fatal(err)
	}
	s.SetPlayerAuto(false)
	s.SetForceAuto(true)
	for i := 0; i < 800 && !s.Done(); i++ {
		s.Step()
		if countSkills(s, "p") > 0 {
			return
		}
	}
	t.Fatal("при обрыве связи полный автомат не запустил главную способность игрока")
}

// D4: команда главной способности не принимается для врага и чужого id.
func TestRequestMainSkillRejectsEnemy(t *testing.T) {
	s, err := New(manualPair(), Config{Energy: DefaultEnergy})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range s.units {
		u.energy = s.cfg.Max
		u.dead = false
	}
	if s.RequestMainSkill("e") {
		t.Fatal("принята команда для вражеского героя")
	}
	if s.RequestMainSkill("nope") {
		t.Fatal("принята команда для неизвестного героя")
	}
	if !s.RequestMainSkill("p") {
		t.Fatal("команда своего героя отклонена")
	}
}
