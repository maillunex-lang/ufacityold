package campaign

import (
	"errors"
	"fmt"
)

var ErrLocked = errors.New("campaign stage locked")

type Stage struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Open bool   `json:"open"`
	Boss bool   `json:"boss"`
}
type Home struct {
	Gold       int         `json:"gold"`
	HeroXP     int         `json:"heroXp"`
	Cleared    int         `json:"campaignCleared"`
	Heroes     []string    `json:"heroes"`
	HeroStates []HeroState `json:"heroStates"`
}
type HeroState struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	XP    int    `json:"xp"`
}

func Stages(cleared int) []Stage {
	out := make([]Stage, 10)
	for i := range out {
		id := i + 1
		out[i] = Stage{ID: id, Name: fmt.Sprintf("Этап %d", id), Open: id <= cleared+1, Boss: id == 5 || id == 10}
	}
	return out
}
