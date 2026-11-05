package battle

// Config закрепляется при создании боя. Изменение исходных определений/версии
// после New действует только на следующий бой; соединение WebSocket здесь не создаётся.
type Config struct {
	Energy          Energy
	BalanceVersion  string
	MaxLevel        int // 0 — стартовый предел 100
	MinHeroDistance int // минимальная дистанция живых героев, в тысячных долях единицы
}

type UnitState struct {
	ID          string   `json:"id"`
	HP          int      `json:"hp"`
	MaxHP       int      `json:"maxHp"`
	Attack      int      `json:"attack"`
	Energy      int      `json:"energy"`
	Position    Position `json:"position"`
	Target      string   `json:"target,omitempty"`
	AutoReadyAt [2]int   `json:"autoReadyAt"`
	Dead        bool     `json:"dead"`
}

type State struct {
	T       int         `json:"t"`
	Units   []UnitState `json:"units"`
	Balance string      `json:"balanceVersion"`
	Result  *Result     `json:"result,omitempty"`
}

// New копирует и проверяет серверные данные, затем однократно применяет пассивы.
func New(defs []UnitDef, cfg Config) (*Battle, error) {
	defs = prepare(defs)
	if cfg.MaxLevel == 0 {
		cfg.MaxLevel = 100
	}
	if cfg.BalanceVersion == "" {
		cfg.BalanceVersion = BalanceVersion
	}
	if err := validate(defs, cfg); err != nil {
		return nil, err
	}
	if cfg.MinHeroDistance == 0 {
		cfg.MinHeroDistance = 600
	}
	s := &Battle{cfg: cfg.Energy, minHeroDistance: cfg.MinHeroDistance, touched: map[string]bool{}, manual: map[string]bool{}, balance: cfg.BalanceVersion, playerAuto: true}
	for i := range defs {
		d := &defs[i]
		d.PassiveActive = false // входной флаг не авторитетен
		if d.Passive != nil && d.Level >= d.Passive.UnlockLevel {
			bonus := d.Passive.bonus(d.Level)
			d.Attack += int(int64(d.Attack) * int64(bonus.AttackPermille) / 1000)
			d.MaxHP += int(int64(d.MaxHP) * int64(bonus.HPPermille) / 1000)
			if d.Attack > maxStat || d.MaxHP > maxStat {
				return nil, invalid(d.ID, "пассив превышает предел характеристик")
			}
			d.PassiveActive = true
			s.events = append(s.events, Event{T: 0, Type: "passive", Src: d.ID, SkillID: d.Passive.ID})
		}
	}
	counts := map[string]map[Faction]int{"player": {}, "enemy": {}}
	for i := range defs {
		if defs[i].Faction != "" {
			counts[defs[i].Side][defs[i].Faction]++
		}
	}
	for i := range defs {
		d := &defs[i]
		bonus := 0
		if n := counts[d.Side][d.Faction]; n >= 5 {
			bonus = 200
		} else if n >= 4 {
			bonus = 150
		} else if n >= 3 {
			bonus = 100
		}
		if bonus > 0 {
			d.Attack += d.Attack * bonus / 1000
			d.MaxHP += d.MaxHP * bonus / 1000
		}
		if d.Attack > maxStat || d.MaxHP > maxStat {
			return nil, invalid(d.ID, "фракционный бонус превышает предел характеристик")
		}
		u := &unit{UnitDef: *d, hp: d.MaxHP, nextAt: d.IntervalMs, pos: d.Position, approach: -1}
		for slot, skill := range d.AutoSkills {
			if skill.ID != "" && d.Level >= skill.UnlockLevel {
				u.autoAt[slot] = skill.CooldownMs
			}
		}
		s.units = append(s.units, u)
	}
	s.defs = defs
	return s, nil
}

// Run — ускоренный полный автомат над тем же пошаговым ядром.
func Run(defs []UnitDef, cfg Energy) (Outcome, error) {
	s, err := New(defs, Config{Energy: cfg})
	if err != nil {
		return Outcome{}, err
	}
	for !s.Done() {
		s.Step()
	}
	return s.outcome(), nil
}

func (s *Battle) Done() bool { return s.result != nil }

func (s *Battle) SetPlayerAuto(on bool) { s.playerAuto = on }
func (s *Battle) PlayerAuto() bool      { return s.playerAuto }
func (s *Battle) SetForceAuto(on bool)  { s.forceAuto = on }

// wantsMainSkill: враги всегда тратят полную энергию сами. Герои игрока — только
// в полном автомате, по команде или пока связь оборвана (сервер продолжает бой).
func (s *Battle) wantsMainSkill(u *unit) bool {
	if s.manual[u.ID] {
		return true
	}
	if u.energy < s.cfg.Max {
		return false
	}
	if u.Side != "player" {
		return true
	}
	return s.playerAuto || s.forceAuto
}

func (s *Battle) RequestMainSkill(id string) bool {
	if s.Done() {
		return false
	}
	for _, u := range s.units {
		if u.ID != id {
			continue
		}
		// Только живой герой стороны игрока. Враги выпускают способность сами.
		if u.Side != "player" || u.dead || u.energy < s.cfg.Max {
			return false
		}
		s.manual[id] = true
		return true
	}
	return false
}

// Step продвигает ровно один тик игрового времени. Повтор после завершения — без
// новых событий и побочных эффектов. Приоритет: главная, автонавык 1, автонавык 2, атака.
// У автонавыков свои часы; обычная атака не сбрасывает их перезарядку.
func (s *Battle) Step() []Event {
	if s.Done() {
		return nil
	}
	s.t += TickMs
	s.moveUnits()
	for _, u := range s.units {
		if u.dead || u.hp <= 0 {
			continue
		}
		if u.Melee && (u.approaching || u.target == nil || u.target.hp <= 0 || !inRange(u, u.target)) {
			continue
		}
		if s.wantsMainSkill(u) && s.mainSkill(u) {
			delete(s.manual, u.ID)
			continue
		}
		if s.autoSkill(u, 0) || s.autoSkill(u, 1) {
			continue
		}
		if s.t >= u.nextAt {
			s.basicAttack(u)
		}
	}
	for _, u := range s.units {
		if u.hp <= 0 && !u.dead {
			u.dead = true
			s.events = append(s.events, Event{T: s.t, Type: "death", Src: u.ID})
		}
	}
	p, e := s.alive("player"), s.alive("enemy")
	switch {
	case p == 0 && e == 0:
		s.result = &Result{"enemy", "draw", s.t}
	case p == 0:
		s.result = &Result{"enemy", "elimination", s.t}
	case e == 0:
		s.result = &Result{"player", "elimination", s.t}
	case s.t >= TimeLimitMs:
		s.result = &Result{"enemy", "timeout", TimeLimitMs}
	}
	batch := cloneEvents(s.events[s.delivered:])
	s.delivered = len(s.events)
	return batch
}

// Outcome доступен только после завершения; возвращаемая запись не владеет памятью ядра.
func (s *Battle) Outcome() (Outcome, bool) {
	if !s.Done() {
		return Outcome{}, false
	}
	return s.outcome(), true
}

func (s *Battle) Snapshot() State {
	state := State{T: s.t, Balance: s.balance, Units: make([]UnitState, 0, len(s.units))}
	if s.result != nil {
		r := *s.result
		state.Result = &r
	}
	for _, u := range s.units {
		x := UnitState{ID: u.ID, HP: u.hp, MaxHP: u.MaxHP, Attack: u.Attack, Energy: s.energyOf(u),
			Position: u.pos, AutoReadyAt: u.autoAt, Dead: u.dead}
		if u.target != nil && u.target.hp > 0 {
			x.Target = u.target.ID
		}
		state.Units = append(state.Units, x)
	}
	return state
}

// Roster — состав, закреплённый при создании боя (после пассивов). Снимок юнитов
// его не содержит: клиенту живого боя нужны роль, имя, сторона и слот.
func (s *Battle) Roster() []UnitDef {
	return cloneDefs(s.defs)
}

func cloneDefs(defs []UnitDef) []UnitDef {
	out := append([]UnitDef(nil), defs...)
	for i := range out {
		for slot := range out[i].AutoSkills {
			out[i].AutoSkills[slot].Upgrades = append([]SkillUpgrade(nil), out[i].AutoSkills[slot].Upgrades...)
		}
		if out[i].Passive != nil {
			p := *out[i].Passive
			p.Upgrades = append([]PassiveBonus(nil), p.Upgrades...)
			out[i].Passive = &p
		}
	}
	return out
}

func cloneEvents(events []Event) []Event {
	out := append([]Event(nil), events...)
	for i := range out {
		out[i].Targets = append([]Hit(nil), out[i].Targets...)
		if out[i].Move != nil {
			m := *out[i].Move
			out[i].Move = &m
		}
		if out[i].Energy != nil {
			out[i].Energy = make(map[string]int, len(events[i].Energy))
			for id, value := range events[i].Energy {
				out[i].Energy[id] = value
			}
		}
	}
	return out
}
