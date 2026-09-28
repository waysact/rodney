package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

func updateState(change func(*State) error) (*State, error) {
	if err := os.MkdirAll(stateDir(), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(stateDir(), "state.lock"))
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("lock browser state: %w", err)
	}
	defer lock.Unlock()
	s, err := loadState()
	if err != nil {
		return nil, err
	}
	if err := change(s); err != nil {
		return nil, err
	}
	if err := saveState(s); err != nil {
		return nil, err
	}
	return s, nil
}
