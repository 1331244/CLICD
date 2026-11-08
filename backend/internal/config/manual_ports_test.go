package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteCreateTaskKeepsManualAndLegacyPortAllocationModes(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	path := filepath.Join(dir, "config.json")
	SetConfigPath(path)
	legacy := ClicdConfig{AdminUser: "fixture", AdminPassHash: "hash", JWTSecret: "fixture", DataDir: dir, Tasks: []SavedTask{
		{ID: "task-1", Type: "create", ContainerName: "manual", Status: "pending", Config: `{"name":"manual","port_mapping_count":1,"auto_port_mappings":false}`},
		{ID: "task-2", Type: "create", ContainerName: "legacy", Status: "pending", Config: `{"name":"legacy","port_mapping_count":2}`},
		{ID: "task-3", Type: "create", ContainerName: "automatic", Status: "pending", Config: `{"name":"automatic","port_mapping_count":2,"auto_port_mappings":true}`},
	}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for reload := 0; reload < 2; reload++ {
		cfg, err := InitConfig()
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Tasks) != 3 {
			t.Fatalf("reload %d lost pending create tasks: %+v", reload, cfg.Tasks)
		}
		for _, task := range cfg.Tasks {
			mode := parseSavedTaskConfig(task.Config)
			switch task.ContainerName {
			case "manual":
				if mode.AutoPortMappings == nil || *mode.AutoPortMappings || mode.PortMappingCount != 1 {
					t.Fatalf("manual port allocation lost on reload %d: %s", reload, task.Config)
				}
			case "legacy":
				if mode.AutoPortMappings != nil || mode.PortMappingCount != 2 {
					t.Fatalf("legacy allocation changed on reload %d: %s", reload, task.Config)
				}
			case "automatic":
				if mode.AutoPortMappings == nil || !*mode.AutoPortMappings {
					t.Fatalf("explicit automatic allocation lost: %s", task.Config)
				}
			}
		}
		if err := SaveConfig(); err != nil {
			t.Fatal(err)
		}
		resetConfigStoreForTest(t)
		SetConfigPath(path)
	}
}

func TestSQLiteManualPortColumnMigrationPreservesLegacyAutomaticDefault(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	path := filepath.Join(dir, "config.json")
	SetConfigPath(path)
	raw, err := json.Marshal(ClicdConfig{AdminUser: "fixture", AdminPassHash: "hash", JWTSecret: "fixture", DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Tasks = []SavedTask{{ID: "task-1", Type: "create", ContainerName: "legacy", Status: "pending", Config: `{"name":"legacy","port_mapping_count":2}`}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	// Reproduce the tasks schema used by existing installations before upgrade.
	if _, err := db.Exec("ALTER TABLE tasks DROP COLUMN cfg_auto_port_mappings"); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tasks) != 1 || parseSavedTaskConfig(cfg.Tasks[0].Config).AutoPortMappings != nil {
		t.Fatalf("upgrade changed old pending task allocation semantics: %+v", cfg.Tasks)
	}
}
