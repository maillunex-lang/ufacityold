package battle

// SpeedAccess — только серверные данные аккаунта. Уровень демонстрационных героев
// и параметры запроса не дают доступа к ускорению.
type SpeedAccess struct {
	TutorialCompleted bool
	VIPLevel          int
}

type PlaybackOptions struct {
	Speed         int   `json:"speed"`
	AllowedSpeeds []int `json:"allowedSpeeds"`
}

func ValidSpeed(speed int) bool { return speed == 1 || speed == 2 || speed == 4 }

func (a SpeedAccess) Allows(speed int) bool {
	switch speed {
	case 1:
		return true
	case 2:
		return a.TutorialCompleted
	case 4:
		return a.VIPLevel >= 2
	default:
		return false
	}
}

func (a SpeedAccess) Options(speed int) PlaybackOptions {
	allowed := []int{1}
	for _, candidate := range []int{2, 4} {
		if a.Allows(candidate) {
			allowed = append(allowed, candidate)
		}
	}
	if !a.Allows(speed) {
		speed = 1
	}
	return PlaybackOptions{Speed: speed, AllowedSpeeds: allowed}
}
