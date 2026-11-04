package battle

type TargetRule string

const (
	CurrentEnemy TargetRule = "current_enemy"
	LowestEnemy  TargetRule = "lowest_hp_enemy"
	AllEnemies   TargetRule = "all_enemies"
	Self         TargetRule = "self"
	LowestAlly   TargetRule = "lowest_hp_ally"
	AllAllies    TargetRule = "all_allies"
)

type SkillUpgrade struct {
	Level         int `json:"level"`
	PowerPermille int `json:"powerPermille"`
}

// AutoSkill — серверные параметры одного из двух слотов. Пустой ID отключает слот.
// PowerPermille: 1000 = 100% атаки. Дальность всех действий задаётся героем.
type AutoSkill struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	UnlockLevel   int            `json:"unlockLevel"`
	CooldownMs    int            `json:"cooldownMs"`
	Kind          string         `json:"kind"` // damage | heal
	Target        TargetRule     `json:"target"`
	PowerPermille int            `json:"powerPermille"`
	Upgrades      []SkillUpgrade `json:"upgrades,omitempty"`
}

type PassiveBonus struct {
	Level          int `json:"level"`
	AttackPermille int `json:"attackPermille"`
	HPPermille     int `json:"hpPermille"`
}

// Passive — один постоянный бонус характеристик с момента старта боя.
// Это первый поддержанный тип пассива; новые типы триггеров потребуют расширения ядра.
type Passive struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	UnlockLevel    int            `json:"unlockLevel"`
	AttackPermille int            `json:"attackPermille"`
	HPPermille     int            `json:"hpPermille"`
	Upgrades       []PassiveBonus `json:"upgrades,omitempty"`
}

func (a AutoSkill) power(level int) int {
	power, rank := a.PowerPermille, a.UnlockLevel
	for _, upgrade := range a.Upgrades {
		if upgrade.Level <= level && upgrade.Level > rank {
			power, rank = upgrade.PowerPermille, upgrade.Level
		}
	}
	return power
}

func (p Passive) bonus(level int) PassiveBonus {
	bonus := PassiveBonus{p.UnlockLevel, p.AttackPermille, p.HPPermille}
	for _, upgrade := range p.Upgrades {
		if upgrade.Level <= level && upgrade.Level > bonus.Level {
			bonus = upgrade
		}
	}
	return bonus
}

func (s *Battle) skillTargets(u *unit, skill AutoSkill) []*unit {
	reach := u.AttackRange
	filter := func(candidates []*unit) []*unit {
		var out []*unit
		for _, candidate := range candidates {
			if distance(u.pos, candidate.pos) <= float64(reach) {
				out = append(out, candidate)
			}
		}
		return out
	}
	one := func(target *unit) []*unit {
		if target == nil {
			return nil
		}
		return []*unit{target}
	}
	switch skill.Target {
	case CurrentEnemy:
		if u.Melee {
			if u.target == nil || u.target.hp <= 0 {
				return nil
			}
			return filter(one(u.target))
		}
		return one(frontTarget(filter(s.enemiesOf(u))))
	case LowestEnemy:
		return one(lowestHP(filter(s.enemiesOf(u))))
	case AllEnemies:
		return filter(s.enemiesOf(u))
	case Self:
		return one(u)
	case AllAllies:
		return filter(s.alliesOf(u))
	case LowestAlly:
		// Для лечения сравниваем долю здоровья; равенство решает порядок отряда.
		allies := filter(s.alliesOf(u))
		if len(allies) == 0 {
			return nil
		}
		best := allies[0]
		for _, ally := range allies[1:] {
			if int64(ally.hp)*int64(best.MaxHP) < int64(best.hp)*int64(ally.MaxHP) {
				best = ally
			}
		}
		return one(best)
	}
	return nil // неизвестные правила отсекаются в New
}

func (s *Battle) autoSkill(u *unit, slot int) bool {
	skill := u.AutoSkills[slot]
	if skill.ID == "" || u.Level < skill.UnlockLevel || s.t < u.autoAt[slot] {
		return false
	}
	targets := s.skillTargets(u, skill)
	amount := int(int64(u.Attack) * int64(skill.power(u.Level)) / 1000)
	if skill.Kind == "heal" {
		injured := targets[:0]
		for _, target := range targets {
			if target.hp < target.MaxHP && amount > 0 {
				injured = append(injured, target)
			}
		}
		targets = injured
	}
	if len(targets) == 0 {
		return false
	}
	ev := Event{T: s.t, Type: "auto_skill", Src: u.ID, Kind: skill.Kind, SkillID: skill.ID, SkillSlot: slot + 1}
	for _, target := range targets {
		if skill.Kind == "damage" {
			hit := s.damage(target, amount)
			s.gain(u, float64(hit.Amount)*s.cfg.PerDealt)
			ev.Targets = append(ev.Targets, hit)
		} else {
			hit, restored := s.heal(target, amount)
			s.gain(u, float64(restored)*s.cfg.PerHealed)
			if target != u {
				s.gain(target, float64(restored)*s.cfg.PerHealed)
			}
			ev.Targets = append(ev.Targets, hit)
		}
	}
	u.autoAt[slot] = s.t + skill.CooldownMs
	s.flush(ev)
	return true
}
