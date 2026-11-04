package battle

// PrototypeSquads возвращает отряды ВРЕМЕННОГО прототипа (этап 1): игрок и враг по пять героев.
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
	}
	for i := range e {
		e[i].Side, e[i].Temporary, e[i].MaxHP = "enemy", true, e[i].MaxHP*hpScale
	}
	return append(p, e...)
}
