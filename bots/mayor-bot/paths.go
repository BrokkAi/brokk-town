package mayorbot

import (
	"errors"
	"os"
	"path/filepath"
)

func stateHome() (string, error) {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("XDG_STATE_HOME must be absolute")
		}
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}
