package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWHMCSTrafficResetReceiptAtomicAndPersistent(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	path := filepath.Join(dir, "config.json")
	SetConfigPath(path)
	cycle := time.Now().Format("2006-01")
	legacy := ClicdConfig{DataDir: dir, AdminUser: "fixture", AdminPassHash: "hash", JWTSecret: "fixture", Containers: []Container{{ID: 1, UUID: "paid-lxc", Name: "paid-lxc", Virtualization: "lxc", TrafficUsedRX: 123, TrafficUsedTX: 456, TrafficResetDate: cycle, MonthlyTrafficGB: 1}}}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	c := FindContainerByUUID("paid-lxc")
	if c == nil {
		t.Fatal("fixture missing")
	}
	receipt, err := WHMCSResetTrafficOnce(c, "purchase-one", cycle)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ClearedAt <= 0 || c.TrafficUsedRX != 0 || c.TrafficUsedTX != 0 {
		t.Fatal("reset not committed")
	}
	c.TrafficUsedRX = 99
	c.TrafficUsedTX = 88
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	replay, err := WHMCSResetTrafficOnce(c, "purchase-one", cycle)
	if err != nil || replay.ClearedAt != receipt.ClearedAt || c.TrafficUsedRX != 99 || c.TrafficUsedTX != 88 {
		t.Fatal("replay cleared new traffic", err)
	}
	if _, err := WHMCSResetTrafficOnce(c, "purchase-one", "2099-01"); err == nil {
		t.Fatal("receipt reused for a different cycle")
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	saved, err := WHMCSTrafficReceiptFor("paid-lxc", "purchase-one")
	if err != nil || saved == nil || saved.ClearedAt != receipt.ClearedAt {
		t.Fatal("receipt lost on restart", err)
	}
	c = FindContainerByUUID("paid-lxc")
	if c.TrafficUsedRX != 99 || c.TrafficUsedTX != 88 {
		t.Fatal("new usage not persistent")
	}
	before := c.TrafficUsedRX
	if _, err := WHMCSResetTrafficOnce(&Container{UUID: "missing"}, "purchase-two", cycle); err == nil {
		t.Fatal("missing container reset")
	}
	if r, _ := WHMCSTrafficReceiptFor("missing", "purchase-two"); r != nil || c.TrafficUsedRX != before {
		t.Fatal("failed write produced receipt or changed counters")
	}
}
