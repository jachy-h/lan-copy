package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	stateFileName  = "state.json"
	backupFileName = "state.backup"
	maxStateBytes  = 16 << 20
)

// Store persists the registry snapshot and secret material. All writes are
// atomic; the previous valid snapshot is kept as a fallback.
type Store struct {
	dir string
	box *SecretBox
	mu  sync.Mutex
}

func OpenStore(dataDir string, box *SecretBox) (*Store, error) {
	dir := filepath.Join(dataDir, "plugins")
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("create plugin state directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o750); err != nil {
		return nil, fmt.Errorf("create plugin data directory: %w", err)
	}
	return &Store{dir: dir, box: box}, nil
}

func (s *Store) Dir() string       { return s.dir }
func (s *Store) TmpDir() string    { return filepath.Join(s.dir, "tmp") }
func (s *Store) DataDir() string   { return filepath.Join(s.dir, "data") }
func (s *Store) StatePath() string { return filepath.Join(s.dir, stateFileName) }

// EventToken derives the lifecycle signing token for one instance.
func (s *Store) EventToken(instanceID string) (string, error) {
	return s.box.EventToken(instanceID)
}

func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadFile(filepath.Join(s.dir, stateFileName))
	if err == nil {
		return state, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		backup, backupErr := s.loadFile(filepath.Join(s.dir, backupFileName))
		if backupErr == nil {
			return backup, nil
		}
		if errors.Is(backupErr, fs.ErrNotExist) {
			return NewState(), nil
		}
		return State{}, fmt.Errorf("load plugin state backup: %w", backupErr)
	}
	backup, backupErr := s.loadFile(filepath.Join(s.dir, backupFileName))
	if backupErr == nil {
		return backup, nil
	}
	return State{}, fmt.Errorf("load plugin state: %w (backup: %v)", err, backupErr)
}

func (s *Store) loadFile(path string) (State, error) {
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateBytes+1))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return State{}, errors.New("unexpected data after plugin state")
	}
	if state.Version != StateVersion {
		return State{}, fmt.Errorf("unsupported plugin state version %d", state.Version)
	}
	if state.HealthRecords == nil {
		state.HealthRecords = map[string][]HealthRecord{}
	}
	if err := s.openSecrets(&state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *Store) Save(state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.Version = StateVersion
	state = trimState(state)
	if err := s.sealSecrets(&state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxStateBytes {
		return fmt.Errorf("plugin state exceeds %d bytes", maxStateBytes)
	}
	path := filepath.Join(s.dir, stateFileName)
	if existing, err := os.ReadFile(path); err == nil {
		if err := writeAtomic(filepath.Join(s.dir, backupFileName), existing); err != nil {
			return fmt.Errorf("write plugin state backup: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write plugin state: %w", err)
	}
	return nil
}

func (s *Store) sealSecrets(state *State) error {
	for instanceIndex := range state.Instances {
		env := state.Instances[instanceIndex].Config.Env
		for envIndex := range env {
			if !env[envIndex].Secret || env[envIndex].Value == "" {
				continue
			}
			sealed, err := s.box.SealString(env[envIndex].Value)
			if err != nil {
				return fmt.Errorf("encrypt plugin secret %s: %w", env[envIndex].Name, err)
			}
			env[envIndex].Value = sealed
		}
	}
	return nil
}

func (s *Store) openSecrets(state *State) error {
	for instanceIndex := range state.Instances {
		env := state.Instances[instanceIndex].Config.Env
		for envIndex := range env {
			if !env[envIndex].Secret || env[envIndex].Value == "" {
				continue
			}
			plain, err := s.box.OpenString(env[envIndex].Value)
			if err != nil {
				return fmt.Errorf("decrypt plugin secret %s for %s: %w", env[envIndex].Name, state.Instances[instanceIndex].PluginID, err)
			}
			env[envIndex].Value = plain
		}
	}
	return nil
}

func trimState(state State) State {
	if len(state.Tasks) > MaxTaskHistory {
		state.Tasks = append([]Task(nil), state.Tasks[len(state.Tasks)-MaxTaskHistory:]...)
	}
	for index := range state.Tasks {
		state.Tasks[index].Message = truncate(state.Tasks[index].Message, MaxTaskMessage)
		state.Tasks[index].Error = truncate(state.Tasks[index].Error, MaxTaskError)
	}
	for key, records := range state.HealthRecords {
		if len(records) > MaxHealthRecords {
			records = append([]HealthRecord(nil), records[len(records)-MaxHealthRecords:]...)
			state.HealthRecords[key] = records
		}
		for index := range records {
			records[index].Error = truncate(records[index].Error, MaxHealthError)
		}
	}
	return state
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "")
}

// RecoverTasks marks tasks that were running when the process stopped.
func RecoverTasks(state *State) bool {
	changed := false
	for index := range state.Tasks {
		if state.Tasks[index].Status == TaskRunning || state.Tasks[index].Status == TaskPending {
			state.Tasks[index].Status = TaskFailed
			state.Tasks[index].Error = "服务重启，任务中断"
			state.Tasks[index].UpdatedAt = nowUTC()
			changed = true
		}
	}
	return changed
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".plugin-state-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDir(dir)
}
