package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWHMCSPolicyAndQuotaRevisionSurviveSQLiteReload(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	path := filepath.Join(dir, "config.json")
	SetConfigPath(path)
	cycle := time.Now().Format("2006-01")
	original := Container{ID: 1, UUID: "policy-owned", Name: "whmcs-59-aabbccddeeff", Virtualization: "lxc", CPUPercent: 25, SwapMB: 64, SwapConfigured: true, BillingRestrictions: map[string]bool{"whmcs-1234567890abcdef-59": true}, TrafficQuotaRevision: 7, MonthlyTrafficGB: 1, TrafficResetDate: cycle, TrafficUsedRX: 123}
	raw, _ := json.Marshal(ClicdConfig{DataDir: dir, AdminUser: "fixture", AdminPassHash: "hash", JWTSecret: "fixture", Containers: []Container{original}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	c := FindContainerByUUID(original.UUID)
	if c == nil || c.CPUPercent != 25 || c.SwapMB != 64 || !c.SwapConfigured || !c.BillingBlocked() || c.TrafficQuotaRevision != 7 {
		t.Fatalf("policy lost on reload: %+v", c)
	}
	if _, err := WHMCSResetTrafficOnce(c, "receipt-one", cycle); err != nil {
		t.Fatal(err)
	}
	if c.TrafficQuotaRevision != 8 {
		t.Fatal("reset did not advance quota revision")
	}
	if _, err := WHMCSResetTrafficOnce(c, "receipt-one", cycle); err != nil || c.TrafficQuotaRevision != 8 {
		t.Fatal("replay advanced quota revision", err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	if c = FindContainerByUUID(original.UUID); c.TrafficQuotaRevision != 8 || c.TrafficUsedRX != 0 || !c.BillingBlocked() {
		t.Fatal("reset counters and policy were not committed together")
	}
}
