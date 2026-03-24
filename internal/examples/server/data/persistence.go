package data

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

type customConfigStore struct {
	mux   sync.RWMutex
	path  string
	items map[string]string
}

var persistedCustomConfigs = &customConfigStore{items: map[string]string{}}

func InitCustomConfigStore(path string) error {
	if path == "" {
		return nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	persistedCustomConfigs.mux.Lock()
	defer persistedCustomConfigs.mux.Unlock()

	persistedCustomConfigs.path = absPath

	content, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			persistedCustomConfigs.items = map[string]string{}
			return nil
		}
		return err
	}

	if len(content) == 0 {
		persistedCustomConfigs.items = map[string]string{}
		return nil
	}

	items := map[string]string{}
	if err = json.Unmarshal(content, &items); err != nil {
		return err
	}

	persistedCustomConfigs.items = items
	return nil
}

func getPersistedCustomConfig(agentID InstanceId) (string, bool) {
	persistedCustomConfigs.mux.RLock()
	defer persistedCustomConfigs.mux.RUnlock()

	if persistedCustomConfigs.items == nil {
		return "", false
	}

	cfg, ok := persistedCustomConfigs.items[uuid.UUID(agentID).String()]
	return cfg, ok
}

func setPersistedCustomConfig(agentID InstanceId, config string) {
	persistedCustomConfigs.mux.Lock()
	defer persistedCustomConfigs.mux.Unlock()

	if persistedCustomConfigs.items == nil {
		persistedCustomConfigs.items = map[string]string{}
	}

	persistedCustomConfigs.items[uuid.UUID(agentID).String()] = config
	persistedCustomConfigs.saveLocked()

	persistedCustomConfigs.persistAgentConfigFileLocked(agentID, "desired_custom.yaml", config)
}

func persistEffectiveConfigSnapshot(agentID InstanceId, config string) {
	persistedCustomConfigs.mux.Lock()
	defer persistedCustomConfigs.mux.Unlock()

	persistedCustomConfigs.persistAgentConfigFileLocked(agentID, "effective_snapshot.yaml", config)
}

func (s *customConfigStore) saveLocked() {
	if s.path == "" {
		return
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Printf("cannot create custom config storage directory %q: %v", dir, err)
		return
	}

	content, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		logger.Printf("cannot marshal custom configs: %v", err)
		return
	}

	if err = os.WriteFile(s.path, content, 0o644); err != nil {
		logger.Printf("cannot persist custom configs to %q: %v", s.path, err)
	}
}

func (s *customConfigStore) persistAgentConfigFileLocked(
	agentID InstanceId,
	fileName string,
	content string,
) {
	if s.path == "" {
		return
	}

	agentDir := filepath.Join(filepath.Dir(s.path), "agents", uuid.UUID(agentID).String())
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		logger.Printf("cannot create persisted config directory %q: %v", agentDir, err)
		return
	}

	filePath := filepath.Join(agentDir, fileName)
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		logger.Printf("cannot persist %s for agent %s to %q: %v", fileName, agentID, filePath, err)
	}
}
