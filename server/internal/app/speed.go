package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"lunexgame/internal/battle"
	"lunexgame/internal/store"
)

type battleReply struct {
	BattleID string          `json:"battleId"`
	Battle   json.RawMessage `json:"battle,omitempty"`
	battle.PlaybackOptions
}

func readSpeed(w http.ResponseWriter, r *http.Request, allowEmpty bool) (int, error) {
	var payload *struct {
		Speed *int `json:"speed"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		if err == io.EOF && allowEmpty {
			return 1, nil
		}
		return 0, err
	}
	if payload == nil {
		return 0, errors.New("expected object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return 0, errors.New("trailing body")
	}
	if payload.Speed == nil {
		if allowEmpty {
			return 1, nil
		}
		return 0, errors.New("speed required")
	}
	if !battle.ValidSpeed(*payload.Speed) {
		return 0, store.ErrInvalidSpeed
	}
	return *payload.Speed, nil
}

func writeSpeedError(w http.ResponseWriter, options battle.PlaybackOptions, err error) {
	switch err {
	case store.ErrNotFound:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case store.ErrInvalidSpeed:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_speed"})
	case store.ErrSpeedForbidden:
		writeJSON(w, http.StatusForbidden, struct {
			Error string `json:"error"`
			battle.PlaybackOptions
		}{"speed_locked", options})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	}
}
