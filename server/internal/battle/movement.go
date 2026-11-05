package battle

import "math"

// Position — целочисленные координаты плоскости поля (1000 = одна единица Godot).
// Высота не участвует в расчёте боя.
type Position struct {
	X int `json:"x"`
	Z int `json:"z"`
}

// Movement описывает пройденный за тик отрезок. T события — начало, а не конец движения.
// Следующий отрезок заменяет предыдущий; смерть прекращает движение.
type Movement struct {
	From       Position `json:"from"`
	To         Position `json:"to"`
	DurationMs int      `json:"durationMs"`
	Target     string   `json:"target"`
}

// prepare не меняет входные данные. Нулевые параметры сохраняют совместимость
// с прототипом и небольшими отрядами в тестах; позиции задаются серверной расстановкой.
func prepare(defs []UnitDef) []UnitDef {
	out := cloneDefs(defs)
	for i := range out {
		d := &out[i]
		if d.Position == (Position{}) {
			x, z := 1600, -1600
			switch d.Slot {
			case 1:
				z = 1600
			case 2:
				x, z = 3500, -3200
			case 3:
				x, z = 3500, 0
			case 4:
				x, z = 3500, 3200
			}
			if d.Side == "player" {
				x = -x
			}
			d.Position = Position{x, z}
		}
		if d.Level == 0 {
			d.Level = 1
		}
		if d.AttackRange == 0 {
			d.AttackRange = 12000
			if d.Melee {
				d.AttackRange = 1100
			}
		}
		if d.RunSpeed == 0 {
			d.RunSpeed = 3000
		}
	}
	return out
}

func distance(a, b Position) float64 {
	return math.Hypot(float64(a.X-b.X), float64(a.Z-b.Z))
}

func inRange(u, target *unit) bool { return distance(u.pos, target.pos) <= float64(u.AttackRange) }

func (s *Battle) inReach(u *unit, candidates []*unit) []*unit {
	var out []*unit
	for _, target := range candidates {
		if inRange(u, target) {
			out = append(out, target)
		}
	}
	return out
}

func (s *Battle) primaryTarget(u *unit, candidates []*unit) *unit {
	if u.Melee {
		return u.target
	}
	return frontTarget(candidates)
}

// Кольцо мест вокруг цели, с приоритетом стороны атакующего. Пять союзников
// получают разные направления. Место удерживается до гибели цели/атакующего.
var approachOffsets = [...]Position{{-1000, 0}, {-309, -951}, {-309, 951}, {809, -588}, {809, 588}}

func (s *Battle) moveUnits() {
	// Сначала выбираем цели всем: результат движения не зависит от порядка обновления координат.
	for _, u := range s.units {
		if u.hp <= 0 || !u.Melee {
			continue
		}
		if u.target != nil && u.target.hp > 0 {
			continue
		}
		u.target, u.approach = frontTarget(s.enemiesOf(u)), -1
		if u.target == nil {
			continue
		}
		for slot := range approachOffsets {
			used := false
			for _, other := range s.units {
				if other != u && other.hp > 0 && other.target == u.target && other.approach == slot {
					used = true
					break
				}
			}
			if !used {
				u.approach = slot
				break
			}
		}
	}
	// Снимок позиций в начале тика: взаимно атакующие бойцы сближаются одновременно.
	next := make([]Position, len(s.units))
	for i, u := range s.units {
		next[i] = u.pos
		u.approaching = false
		if u.hp <= 0 || !u.Melee || u.target == nil || u.approach < 0 {
			continue
		}
		// Взаимно сближающаяся пара останавливается при контакте. Иначе оба
		// каждый тик обходили бы друг друга, пытаясь занять движущиеся места.
		if u.target.target == u && inRange(u, u.target) {
			continue
		}
		goal := s.approachGoal(u, u.target, u.approach)
		for slot := 0; slot < len(approachOffsets); slot++ {
			candidate := s.approachGoal(u, u.target, slot)
			clear := true
			for j := 0; j < i; j++ {
				if s.units[j].hp > 0 && distance(candidate, next[j]) < float64(s.minHeroDistance) {
					clear = false
					break
				}
			}
			if clear {
				u.approach, goal = slot, candidate
				break
			}
		}
		dist := distance(u.pos, goal)
		step := float64(u.RunSpeed)*TickMs/1000 + u.moveCredit
		if dist <= step {
			next[i] = goal
			u.moveCredit = 0
		} else {
			// Сохраняем остаток пути после округления к целым координатам.
			// Квантование шага — меньше 2 тысячных единицы, суммарный путь не превышает бюджет скорости.
			next[i] = Position{u.pos.X + int(float64(goal.X-u.pos.X)*step/dist), u.pos.Z + int(float64(goal.Z-u.pos.Z)*step/dist)}
			u.moveCredit = step - distance(u.pos, next[i])
			u.approaching = true
		}
	}
	for i, u := range s.units {
		if next[i] == u.pos {
			continue
		}
		s.events = append(s.events, Event{T: s.t - TickMs, Type: "move", Src: u.ID,
			Move: &Movement{From: u.pos, To: next[i], DurationMs: TickMs, Target: u.target.ID}})
		u.pos = next[i]
	}
}

func (s *Battle) approachGoal(u, target *unit, slot int) Position {
	offset := approachOffsets[slot%len(approachOffsets)]
	if u.Side == "enemy" {
		offset.X = -offset.X
	}
	radius := float64(u.AttackRange) * 0.9
	return Position{target.pos.X + int(float64(offset.X)*radius/1000), target.pos.Z + int(float64(offset.Z)*radius/1000)}
}

// separate детерминированно раздвигает живых героев. Порядок входного отряда
// задаёт приоритет: более поздний герой сдвигается от уже размещённых.
func (s *Battle) separate(next []Position) {
	min := float64(s.minHeroDistance)
	for i, a := range s.units {
		if a.hp <= 0 {
			continue
		}
		for j := i + 1; j < len(s.units); j++ {
			b := s.units[j]
			if b.hp <= 0 {
				continue
			}
			dx, dz := float64(next[j].X-next[i].X), float64(next[j].Z-next[i].Z)
			d := math.Hypot(dx, dz)
			if d >= min {
				continue
			}
			if d < 0.001 {
				dx, dz = float64((j-i)%2*2-1), 0
				d = 1
			}
			push := (min - d) / d
			budget := float64(b.RunSpeed)*TickMs/1000 + b.moveCredit - distance(b.pos, next[j])
			if budget < 0 {
				budget = 0
			}
			if push > budget {
				push = budget
			}
			candidate := Position{next[j].X + int(dx*push), next[j].Z + int(dz*push)}
			fullBudget := float64(b.RunSpeed)*TickMs/1000 + b.moveCredit
			if moved := distance(b.pos, candidate); moved > fullBudget && moved > 0 {
				ratio := fullBudget / moved
				candidate = Position{b.pos.X + int(float64(candidate.X-b.pos.X)*ratio), b.pos.Z + int(float64(candidate.Z-b.pos.Z)*ratio)}
			}
			if b.target != nil {
				max := float64(b.AttackRange) * 0.95
				if distance(candidate, b.target.pos) > max {
					continue
				}
			}
			next[j] = candidate
		}
	}
}
