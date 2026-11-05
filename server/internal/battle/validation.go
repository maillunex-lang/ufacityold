package battle

import (
	"fmt"
	"math"
)

const maxStat = 1_000_000_000

func invalid(id, message string) error { return fmt.Errorf("герой %q: %s", id, message) }

func validate(defs []UnitDef, cfg Config) error {
	if cfg.MaxLevel < 1 || cfg.MaxLevel > 10000 || len(cfg.BalanceVersion) > 100 {
		return fmt.Errorf("неверные параметры баланса")
	}
	for i, value := range []float64{cfg.Energy.Max, cfg.Energy.PerDealt, cfg.Energy.PerTaken, cfg.Energy.PerHealed} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > maxStat || (i == 0 && value == 0) {
			return fmt.Errorf("неверные коэффициенты энергии")
		}
	}
	ids, slots := map[string]bool{}, map[string]bool{}
	sides := map[string]int{}
	for _, d := range defs {
		if d.ID == "" || ids[d.ID] {
			return invalid(d.ID, "пустой или повторный ID")
		}
		ids[d.ID] = true
		if d.Side != "player" && d.Side != "enemy" {
			return invalid(d.ID, "неверная сторона")
		}
		key := fmt.Sprintf("%s/%d", d.Side, d.Slot)
		if d.Slot < 0 || d.Slot > 4 || slots[key] {
			return invalid(d.ID, "неверный или занятый слот")
		}
		slots[key] = true
		sides[d.Side]++
		switch d.Role {
		case Tank, Fighter, Archer, Mage, Healer:
		default:
			return invalid(d.ID, "неизвестная роль")
		}
		if d.Faction != "" {
			switch d.Faction {
			case FactionDawn, FactionTide, FactionGrove, FactionEmber:
			default:
				return invalid(d.ID, "неизвестная фракция")
			}
		}
		if d.Level < 1 || d.Level > cfg.MaxLevel || d.MaxHP < 1 || d.MaxHP > maxStat || d.Attack < 0 || d.Attack > maxStat ||
			d.IntervalMs < 1 || d.IntervalMs > maxStat || d.AttackRange < 1 || d.AttackRange > maxStat || d.RunSpeed < 1 || d.RunSpeed > maxStat ||
			d.Position.X < -maxStat || d.Position.X > maxStat || d.Position.Z < -maxStat || d.Position.Z > maxStat {
			return invalid(d.ID, "уровень или характеристики вне допустимых границ")
		}
		skillIDs := map[string]bool{}
		for _, skill := range d.AutoSkills {
			if skill.ID == "" {
				if skill.Name != "" || skill.UnlockLevel != 0 || skill.CooldownMs != 0 || skill.Kind != "" || skill.Target != "" || skill.PowerPermille != 0 || len(skill.Upgrades) != 0 {
					return invalid(d.ID, "неполное описание автонавыка")
				}
				continue
			}
			if skillIDs[skill.ID] {
				return invalid(d.ID, "повторный ID навыка")
			}
			skillIDs[skill.ID] = true
			if skill.UnlockLevel < 1 || skill.UnlockLevel > cfg.MaxLevel || skill.CooldownMs < 1 || skill.CooldownMs > maxStat ||
				skill.PowerPermille < 1 || skill.PowerPermille > 10000 {
				return invalid(d.ID, "неверные параметры автонавыка")
			}
			switch skill.Kind {
			case "damage":
				if skill.Target != CurrentEnemy && skill.Target != LowestEnemy && skill.Target != AllEnemies {
					return invalid(d.ID, "неверная цель урона")
				}
			case "heal":
				if skill.Target != Self && skill.Target != LowestAlly && skill.Target != AllAllies {
					return invalid(d.ID, "неверная цель лечения")
				}
			default:
				return invalid(d.ID, "неизвестный эффект автонавыка")
			}
			levels := map[int]bool{}
			for _, upgrade := range skill.Upgrades {
				if upgrade.Level <= skill.UnlockLevel || upgrade.Level > cfg.MaxLevel || levels[upgrade.Level] || upgrade.PowerPermille < 1 || upgrade.PowerPermille > 10000 {
					return invalid(d.ID, "неверное улучшение автонавыка")
				}
				levels[upgrade.Level] = true
			}
		}
		if p := d.Passive; p != nil {
			if p.ID == "" || skillIDs[p.ID] || p.UnlockLevel < 1 || p.UnlockLevel > cfg.MaxLevel || !validBonus(p.AttackPermille, p.HPPermille) {
				return invalid(d.ID, "неверный пассив")
			}
			levels := map[int]bool{}
			for _, upgrade := range p.Upgrades {
				if upgrade.Level <= p.UnlockLevel || upgrade.Level > cfg.MaxLevel || levels[upgrade.Level] || !validBonus(upgrade.AttackPermille, upgrade.HPPermille) {
					return invalid(d.ID, "неверное улучшение пассива")
				}
				levels[upgrade.Level] = true
			}
		}
	}
	if sides["player"] == 0 || sides["enemy"] == 0 {
		return fmt.Errorf("каждая сторона должна иметь от 1 до 5 героев")
	}
	return nil
}

func validBonus(attack, hp int) bool {
	return attack >= 0 && attack <= 10000 && hp >= 0 && hp <= 10000 && attack+hp > 0
}
