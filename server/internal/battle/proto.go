package battle

// PrototypeSquads возвращает отряды ВРЕМЕННОГО прототипа: игрок и враг по пять героев уровня 40.
// Герои и числа — заглушки для проверки механики, не финальный контент и не финальный баланс.
func PrototypeSquads() []UnitDef {
	p := []UnitDef{
		{ID: "p0", Name: "Страж", Role: Tank, Rarity: "rare", Slot: 0, MaxHP: 1500, Attack: 60, IntervalMs: 1500, Melee: true},
		{ID: "p1", Name: "Воин", Role: Fighter, Rarity: "rare", Slot: 1, MaxHP: 1000, Attack: 100, IntervalMs: 1200, Melee: true},
		{ID: "p2", Name: "Лучник", Role: Archer, Rarity: "epic", Slot: 2, MaxHP: 750, Attack: 95, IntervalMs: 1100},
		{ID: "p3", Name: "Маг", Role: Mage, Rarity: "epic", Slot: 3, MaxHP: 700, Attack: 85, IntervalMs: 1400},
		{ID: "p4", Name: "Целитель", Role: Healer, Rarity: "legendary", Slot: 4, MaxHP: 800, Attack: 70, IntervalMs: 1600},
	}
	e := []UnitDef{
		{ID: "e0", Name: "Каменный страж", Role: Tank, Rarity: "rare", Slot: 0, MaxHP: 1400, Attack: 55, IntervalMs: 1500, Melee: true},
		{ID: "e1", Name: "Налётчик", Role: Fighter, Rarity: "rare", Slot: 1, MaxHP: 950, Attack: 95, IntervalMs: 1200, Melee: true},
		{ID: "e2", Name: "Стрелок", Role: Archer, Rarity: "rare", Slot: 2, MaxHP: 700, Attack: 90, IntervalMs: 1100},
		{ID: "e3", Name: "Колдун", Role: Mage, Rarity: "rare", Slot: 3, MaxHP: 650, Attack: 80, IntervalMs: 1400},
		{ID: "e4", Name: "Знахарь", Role: Healer, Rarity: "rare", Slot: 4, MaxHP: 750, Attack: 65, IntervalMs: 1600},
	}
	const hpScale = 3 // временная настройка длительности: бой ~30–60 с, чтобы были видны способности
	for i := range p {
		p[i].Side, p[i].Temporary, p[i].MaxHP = "player", true, p[i].MaxHP*hpScale
		p[i].Level = 40
		p[i].AutoSkills, p[i].Passive = prototypeSkills(p[i].Role)
	}
	for i := range e {
		e[i].Side, e[i].Temporary, e[i].MaxHP = "enemy", true, e[i].MaxHP*hpScale
		e[i].Level = 40
		e[i].AutoSkills, e[i].Passive = prototypeSkills(e[i].Role)
	}
	return append(p, e...)
}

// Наборы для проверки механики, не утверждённые игровые способности и баланс.
func prototypeSkills(role Role) ([2]AutoSkill, *Passive) {
	makeSkill := func(name string, level, cooldown int, kind string, target TargetRule, power int) AutoSkill {
		return AutoSkill{Name: name, UnlockLevel: level, CooldownMs: cooldown, Kind: kind, Target: target, PowerPermille: power}
	}
	var skills [2]AutoSkill
	switch role {
	case Tank:
		skills = [2]AutoSkill{makeSkill("Удар щитом", 10, 5000, "damage", CurrentEnemy, 1200), makeSkill("Передышка", 20, 9000, "heal", Self, 3000)}
	case Fighter:
		skills = [2]AutoSkill{makeSkill("Сильный удар", 10, 5000, "damage", CurrentEnemy, 1600), makeSkill("Рассечение", 20, 9000, "damage", CurrentEnemy, 2200)}
	case Archer:
		skills = [2]AutoSkill{makeSkill("Прицельный выстрел", 10, 5000, "damage", LowestEnemy, 1600), makeSkill("Залп", 20, 9000, "damage", AllEnemies, 700)}
	case Mage:
		skills = [2]AutoSkill{makeSkill("Вспышка", 10, 6000, "damage", AllEnemies, 800), makeSkill("Сгусток", 20, 10000, "damage", LowestEnemy, 2200)}
	case Healer:
		skills = [2]AutoSkill{makeSkill("Исцеление", 10, 5000, "heal", LowestAlly, 2200), makeSkill("Общее лечение", 20, 9000, "heal", AllAllies, 1000)}
	}
	for slot := range skills {
		suffix, upgradeLevel := "-auto1", 30
		if slot == 1 {
			suffix, upgradeLevel = "-auto2", 50
		}
		skills[slot].ID = string(role) + suffix
		skills[slot].Upgrades = []SkillUpgrade{{Level: upgradeLevel, PowerPermille: skills[slot].PowerPermille * 12 / 10}}
	}
	p := &Passive{ID: string(role) + "-passive", Name: "Временная стойкость", UnlockLevel: 40, HPPermille: 100,
		Upgrades: []PassiveBonus{{Level: 70, HPPermille: 200}}}
	if role != Tank {
		p.Name, p.HPPermille, p.AttackPermille = "Временная сила", 0, 100
		p.Upgrades = []PassiveBonus{{Level: 70, AttackPermille: 200}}
	}
	return skills, p
}
