package game

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func Save(path string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".enterprise-save-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Load(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || s.ConversationID == "" || s.Clues == nil || s.HP < 0 || s.HP > Data().MaxHP || s.DroneHP < 0 || s.DroneHP > 10 || s.Turn < 0 {
		return s, errors.New("invalid or unsupported save file")
	}
	if s.Location != "bridge" && s.Location != "sickbay" && s.Location != "engineering" {
		return s, errors.New("invalid saved location")
	}
	if s.Combat && (s.Location != "engineering" || s.DroneHP == 0) {
		return s, errors.New("invalid saved combat state")
	}
	return s, nil
}
