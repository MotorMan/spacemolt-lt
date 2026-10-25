package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DeepMerge merges src into dst, returning the result. Maps are merged recursively;
// slices and scalars from src overwrite dst.
func DeepMerge(dst, src map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(dst))
	for k, v := range dst {
		result[k] = v
	}
	for k, sv := range src {
		dv, exists := dst[k]
		if !exists {
			result[k] = sv
			continue
		}
		dm, dOk := dv.(map[string]interface{})
		sm, sOk := sv.(map[string]interface{})
		if dOk && sOk {
			result[k] = DeepMerge(dm, sm)
		} else {
			result[k] = sv
		}
	}
	return result
}

// ReadJSONFile reads a JSON file into v.
func ReadJSONFile(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// WriteJSONFile writes v as JSON to path atomically (tmp + rename).
func WriteJSONFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SyncFilePaths returns the list of standard data files that can be synced.
func SyncFilePaths(dataDir string) []string {
	return []string{
		filepath.Join(dataDir, "catalog.json"),
		filepath.Join(dataDir, "map.json"),
		filepath.Join(dataDir, "market_details.json"),
		filepath.Join(dataDir, "player_names.json"),
		filepath.Join(dataDir, "settings.json"),
	}
}
