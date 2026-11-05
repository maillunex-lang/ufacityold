// Package battle — серверный расчёт боя с позициями и движением.
//
// Пошаговое ядро и полный автомат Run используют одинаковые тики TickMs без случайности:
// одинаковые входные данные дают одинаковый результат. HTTP-клиент пока проигрывает полную запись.
// Параметры героев и коэффициенты энергии — ВРЕМЕННЫЙ баланс (BalanceVersion), будут выноситься в редактируемые данные.
package battle

import "sort"

const (
	TickMs         = 100
	TimeLimitMs    = 90_000
	BalanceVersion = "proto-3-skills"
)

// Energy — настраиваемые коэффициенты начисления энергии (по ТЗ — настраиваются).
type Energy struct {
	Max       float64 // запас для главной способности
	PerDealt  float64 // за единицу фактического урона обычной атаки/автонавыка
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
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Role          Role         `json:"role"`
	Rarity        string       `json:"rarity"`
	Side          string       `json:"side"` // "player" | "enemy"
	Slot          int          `json:"slot"`
	MaxHP         int          `json:"maxHp"`
	Attack        int          `json:"attack"`
	IntervalMs    int          `json:"intervalMs"`
	Melee         bool         `json:"melee"`
	Temporary     bool         `json:"temporary"` // временная модель/герой, не финальный контент
	Position      Position     `json:"position"`  // стартовая позиция, в тысячных долях единицы Godot
	AttackRange   int          `json:"attackRange"`
	RunSpeed      int          `json:"runSpeed"` // тысячных долей единицы в секунду; дальние не перемещаются
	Level         int          `json:"level"`
	AutoSkills    [2]AutoSkill `json:"autoSkills"`
	Passive       *Passive     `json:"passive,omitempty"`
	PassiveActive bool         `json:"passiveActive"` // рассчитано сервером; MaxHP/Attack уже учитывают пассив
}

type Hit struct {
	Unit   string `json:"unit"`
	Amount int    `json:"amount"` // урон или лечение (фактическое)
	HP     int    `json:"hp"`     // здоровье цели после события
}

type Event struct {
	T         int            `json:"t"`
	Type      string         `json:"type"` // move | attack | skill (главная) | auto_skill | passive | death
	Src       string         `json:"src"`
	Kind      string         `json:"kind,omitempty"` // для skill: damage | heal
	Targets   []Hit          `json:"targets,omitempty"`
	Energy    map[string]int `json:"energy,omitempty"` // энергия юнитов, изменившаяся этим событием
	Move      *Movement      `json:"move,omitempty"`
	SkillID   string         `json:"skillId,omitempty"`
	SkillSlot int            `json:"skillSlot,omitempty"` // 1 или 2 только для автонавыка
}

type Result struct {
	Winner     string `json:"winner"` // player | enemy
	Reason     string `json:"reason"` // elimination | timeout | draw
	DurationMs int    `json:"durationMs"`
}

type Outcome struct {
	Units     []UnitDef           `json:"units"`
	Events    []Event             `json:"events"`
	Result    Result              `json:"result"`
	Balance   string              `json:"balanceVersion"`
	Positions map[string]Position `json:"positions"` // позиции в момент завершения боя, до победной стойки
}

type unit struct {
	UnitDef
	hp          int
	energy      float64
	nextAt      int
	dead        bool
	pos         Position
	target      *unit
	approach    int
	approaching bool
	moveCredit  float64 // неиспользованная длина шага: медленный герой не застревает на округлении координат
	autoAt      [2]int
}

// Battle принадлежит одному исполнителю. Для разных боёв создаются независимые экземпляры.
type Battle struct {
	cfg             Energy
	minHeroDistance int
	units           []*unit
	events          []Event
	t               int
	touched         map[string]bool
	defs            []UnitDef
	balance         string
	result          *Result
	delivered       int
	manual          map[string]bool
	playerAuto      bool // главная способность героев игрока сама; враги всегда на автомате
	forceAuto       bool // обрыв связи: игрок тоже на полном автомате, пока не вернулся
}

func (s *Battle) outcome() Outcome {
	positions := make(map[string]Position, len(s.units))
	for _, u := range s.units {
		positions[u.ID] = u.pos
	}
	return Outcome{Units: cloneDefs(s.defs), Events: cloneEvents(s.events), Result: *s.result, Balance: s.balance, Positions: positions}
}

func (s *Battle) alive(side string) int {
	n := 0
	for _, u := range s.units {
		if u.Side == side && !u.dead {
			n++
		}
	}
	return n
}

func (s *Battle) enemiesOf(u *unit) []*unit {
	return s.pick(func(x *unit) bool { return x.Side != u.Side && x.hp > 0 })
}
func (s *Battle) alliesOf(u *unit) []*unit {
	return s.pick(func(x *unit) bool { return x.Side == u.Side && x.hp > 0 })
}

func (s *Battle) pick(f func(*unit) bool) []*unit {
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

func (s *Battle) energyOf(u *unit) int { return int(u.energy) }

func (s *Battle) gain(u *unit, v float64) {
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
func (s *Battle) damage(t *unit, amount int) Hit {
	if amount > t.hp {
		amount = t.hp
	}
	t.hp -= amount
	s.gain(t, float64(amount)*s.cfg.PerTaken)
	return Hit{Unit: t.ID, Amount: amount, HP: t.hp}
}

// heal лечит; энергию даёт только фактически восстановленное здоровье (избыточное не считается).
func (s *Battle) heal(t *unit, amount int) (Hit, int) {
	room := t.MaxHP - t.hp
	if amount > room {
		amount = room
	}
	t.hp += amount
	return Hit{Unit: t.ID, Amount: amount, HP: t.hp}, amount
}

func (s *Battle) flush(e Event) {
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

func (s *Battle) basicAttack(u *unit) {
	tg := u.target
	if !u.Melee {
		tg = frontTarget(s.inReach(u, s.enemiesOf(u)))
	}
	if tg == nil {
		return
	}
	u.nextAt = s.t + u.IntervalMs
	h := s.damage(tg, u.Attack)
	s.gain(u, float64(h.Amount)*s.cfg.PerDealt) // нанесённый урон обычной атаки даёт энергию владельцу
	s.flush(Event{T: s.t, Type: "attack", Src: u.ID, Targets: []Hit{h}})
}

// mainSkill расходует весь запас энергии; урон и лечение главной способности энергию владельцу не возвращают.
func (s *Battle) mainSkill(u *unit) bool {
	// Цели фиксируются до траты энергии. Ближний использует удерживаемую цель.
	enemies := s.inReach(u, s.enemiesOf(u))
	if u.Role != Healer && len(enemies) == 0 {
		return false
	}
	u.nextAt = s.t + u.IntervalMs
	u.energy = 0
	s.touched[u.ID] = true
	ev := Event{T: s.t, Type: "skill", Src: u.ID, Kind: "damage"}
	mul := func(k float64) int { return int(float64(u.Attack) * k) }
	switch u.Role {
	case Tank: // удар по ближайшему + лечение себя на 10% (владелец не получает за это энергию)
		if tg := s.primaryTarget(u, enemies); tg != nil {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(1.5)))
		}
		if h, n := s.heal(u, u.MaxHP/10); n > 0 {
			ev.Targets = append(ev.Targets, h)
		}
	case Fighter:
		if tg := s.primaryTarget(u, enemies); tg != nil {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(3)))
		}
	case Archer:
		if tg := lowestHP(enemies); tg != nil {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(2.5)))
		}
	case Mage:
		for _, tg := range enemies {
			ev.Targets = append(ev.Targets, s.damage(tg, mul(1.2)))
		}
	case Healer:
		ev.Kind = "heal"
		for _, a := range s.inReach(u, s.alliesOf(u)) {
			h, n := s.heal(a, mul(1.5))
			if a != u && n > 0 {
				s.gain(a, float64(n)*s.cfg.PerHealed) // получатели лечения получают энергию, владелец — нет
			}
			ev.Targets = append(ev.Targets, h)
		}
	}
	if len(ev.Targets) == 0 {
		return false
	}
	s.flush(ev)
	return true
}
