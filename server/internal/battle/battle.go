// Package battle — серверный расчёт боя (прототип, этап 1).
//
// Бой считается целиком на сервере в игровом времени с шагом TickMs и без случайности:
// одинаковые входные данные дают одинаковый результат. Клиент только проигрывает записанные события.
// Параметры героев и коэффициенты энергии — ВРЕМЕННЫЙ баланс (BalanceVersion), будут выноситься в редактируемые данные.
package battle

import "sort"

const (
	TickMs         = 100
	TimeLimitMs    = 90_000
	BalanceVersion = "proto-1"
)

// Energy — настраиваемые коэффициенты начисления энергии (по ТЗ — настраиваются).
type Energy struct {
	Max       float64 // запас для главной способности
	PerDealt  float64 // за единицу нанесённого урона (обычной атакой)
	PerTaken  float64 // за единицу полученного урона
	PerHealed float64 // за единицу фактически восстановленного здоровья
}

var DefaultEnergy = Energy{Max: 100, PerDealt: 0.08, PerTaken: 0.06, PerHealed: 0.05}

type Role string

const (
	Tank    Role = "tank"
	Fighter Role = "fighter"
	Archer  Role = "archer"
	Mage    Role = "mage"
	Healer  Role = "healer"
)

// UnitDef — описание героя на старте боя. Slot: 0,1 — передний ряд, 2,3,4 — задний.
type UnitDef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       Role   `json:"role"`
	Rarity     string `json:"rarity"`
	Side       string `json:"side"` // "player" | "enemy"
	Slot       int    `json:"slot"`
	MaxHP      int    `json:"maxHp"`
	Attack     int    `json:"attack"`
	IntervalMs int    `json:"intervalMs"`
	Melee      bool   `json:"melee"`
	Temporary  bool   `json:"temporary"` // временная модель/герой, не финальный контент
}

type Hit struct {
	Unit   string `json:"unit"`
	Amount int    `json:"amount"` // урон или лечение (фактическое)
	HP     int    `json:"hp"`     // здоровье цели после события
}

type Event struct {
	T       int            `json:"t"`
	Type    string         `json:"type"` // attack | skill | death
	Src     string         `json:"src"`
	Kind    string         `json:"kind,omitempty"` // для skill: damage | heal
	Targets []Hit          `json:"targets,omitempty"`
	Energy  map[string]int `json:"energy,omitempty"` // энергия юнитов, изменившаяся этим событием
}

type Result struct {
	Winner     string `json:"winner"` // player | enemy
	Reason     string `json:"reason"` // elimination | timeout | draw
	DurationMs int    `json:"durationMs"`
}

type Outcome struct {
	Units   []UnitDef `json:"units"`
	Events  []Event   `json:"events"`
	Result  Result    `json:"result"`
	Balance string    `json:"balanceVersion"`
}

type unit struct {
	UnitDef
	hp     int
	energy float64
	nextAt int
	dead   bool
	alive0 bool // жив в начале текущего тика
}

type sim struct {
	cfg     Energy
	units   []*unit
	events  []Event
	t       int
	touched map[string]bool
}

// Run рассчитывает бой.
func Run(defs []UnitDef, cfg Energy) Outcome {
	s := &sim{cfg: cfg, touched: map[string]bool{}}
	for _, d := range defs {
		s.units = append(s.units, &unit{UnitDef: d, hp: d.MaxHP, nextAt: d.IntervalMs})
	}
	var res Result
	for s.t = TickMs; s.t <= TimeLimitMs; s.t += TickMs {
		for _, u := range s.units {
			u.alive0 = !u.dead
		}
		for _, u := range s.units { // порядок — по входному списку: детерминированно
			if !u.alive0 || u.hp <= 0 || s.t < u.nextAt {
				continue
			}
			u.nextAt = s.t + u.IntervalMs
			if u.energy >= cfg.Max {
				s.mainSkill(u)
			} else {
				s.basicAttack(u)
			}
		}
		for _, u := range s.units { // смерти фиксируются в конце тика (одновременные убийства возможны)
			if u.hp <= 0 && !u.dead {
				u.dead = true
				s.events = append(s.events, Event{T: s.t, Type: "death", Src: u.ID})
			}
		}
		p, e := s.alive("player"), s.alive("enemy")
		if p == 0 || e == 0 {
			switch {
			case p > 0:
				res = Result{"player", "elimination", s.t}
			case e > 0:
				res = Result{"enemy", "elimination", s.t}
			default:
				res = Result{"enemy", "draw", s.t} // взаимное уничтожение: атакующий проигрывает
			}
			return s.outcome(defs, res)
		}
	}
	// лимит времени: атакующий (игрок) проигрывает
	return s.outcome(defs, Result{"enemy", "timeout", TimeLimitMs})
}

func (s *sim) outcome(defs []UnitDef, r Result) Outcome {
	return Outcome{Units: defs, Events: s.events, Result: r, Balance: BalanceVersion}
}

func (s *sim) alive(side string) int {
	n := 0
	for _, u := range s.units {
		if u.Side == side && !u.dead {
			n++
		}
	}
	return n
}

func (s *sim) enemiesOf(u *unit) []*unit {
	return s.pick(func(x *unit) bool { return x.Side != u.Side && x.hp > 0 })
}
func (s *sim) alliesOf(u *unit) []*unit {
	return s.pick(func(x *unit) bool { return x.Side == u.Side && x.hp > 0 })
}

func (s *sim) pick(f func(*unit) bool) []*unit {
	var out []*unit
	for _, u := range s.units {
		if f(u) {
			out = append(out, u)
		}
	}
	return out
}

// frontTarget: ближайший — передний ряд раньше заднего, внутри ряда меньший слот.
func frontTarget(c []*unit) *unit {
	if len(c) == 0 {
		return nil
	}
	sort.SliceStable(c, func(i, j int) bool {
		fi, fj := c[i].Slot < 2, c[j].Slot < 2
		if fi != fj {
			return fi
		}
		return c[i].Slot < c[j].Slot
	})
	return c[0]
}

func lowestHP(c []*unit) *unit {
	if len(c) == 0 {
		return nil
	}
	best := c[0]
	for _, u := range c[1:] {
		if u.hp < best.hp {
			best = u
		}
	}
	return best
}

func (s *sim) energyOf(u *unit) int { return int(u.energy) }

func (s *sim) gain(u *unit, v float64) {
	if u.hp <= 0 && u.dead {
		return
	}
	u.energy += v
	if u.energy > s.cfg.Max {
		u.energy = s.cfg.Max
	}
	s.touched[u.ID] = true
}

// damage наносит урон; возвращает фактический. Энергия цели растёт от полученного урона.
func (s *sim) damage(t *unit, amount int) Hit {
	if amount > t.hp {
		amount = t.hp
	}
	t.hp -= amount
	s.gain(t, float64(amount)*s.cfg.PerTaken)
	return Hit{Unit: t.ID, Amount: amount, HP: t.hp}
}

// heal лечит; энергию даёт только фактически восстановленное здоровье (избыточное не считается).
func (s *sim) heal(t *unit, amount int) (Hit, int) {
	room := t.MaxHP - t.hp
	if amount > room {
		amount = room
	}
	t.hp += amount
	return Hit{Unit: t.ID, Amount: amount, HP: t.hp}, amount
}

func (s *sim) flush(e Event) {
	if len(s.touched) > 0 {
		e.Energy = map[string]int{}
		for id := range s.touched {
			for _, u := range s.units {
				if u.ID == id {
					e.Energy[id] = s.energyOf(u)
				}
			}
		}
		s.touched = map[string]bool{}
	}
	s.events = append(s.events, e)
}

func (s *sim) basicAttack(u *unit) {
	tg := frontTarget(s.enemiesOf(u))
	if tg == nil {
		return
	}
	h := s.damage(tg, u.Attack)
	s.gain(u, float64(h.Amount)*s.cfg.PerDealt) // нанесённый урон обычной атаки даёт энергию владельцу
	s.flush(Event{T: s.t, Type: "attack", Src: u.ID, Targets: []Hit{h}})
}

// mainSkill расходует весь запас энергии; урон и лечение главной способности энергию владельцу не возвращают.
func (s *sim) mainSkill(u *unit) {
	u.energy = 0
	s.touched[u.ID] = true
	ev := Event{T: s.t, Type: "skill", Src: u.ID, Kind: "damage"}
	mul := func(k float64) int { return int(float64(u.Attack) * k) }
	switch u.Role {
	case Tank: // удар по ближайшему + лечение себя на 10% (владелец не получает за это энергию)
		if tg := frontTarget(s.enemiesOf(u)); tg != nil {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(1.5)))
		}
		if h, n := s.heal(u, u.MaxHP/10); n > 0 {
			ev.Targets = append(ev.Targets, h)
		}
	case Fighter:
		if tg := frontTarget(s.enemiesOf(u)); tg != nil {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(3)))
		}
	case Archer:
		if tg := lowestHP(s.enemiesOf(u)); tg != nil {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(2.5)))
		}
	case Mage:
		for _, tg := range s.enemiesOf(u) {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(1.2)))
		}
	case Healer:
		ev.Kind = "heal"
		for _, a := range s.alliesOf(u) {
			h, n := s.heal(a, mul(1.5))
			if a != u && n > 0 {
				s.gain(a, float64(n)*s.cfg.PerHealed) // получатели лечения получают энергию, владелец — нет
			}
			ev.Targets = append(ev.Targets, h)
		}
	}
	if len(ev.Targets) == 0 {
		return
	}
	s.flush(ev)
}
