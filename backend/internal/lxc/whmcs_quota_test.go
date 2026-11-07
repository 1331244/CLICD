package lxc

import (
	"clicd/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quotaFixture() config.Container {
	return config.Container{UUID: "quota-owned-uuid", Name: "whmcs-59-aabbccddeeff", Virtualization: "lxc", TrafficMode: "total", MonthlyTrafficGB: 1, TrafficResetDate: "2026-11", NetworkDownMbps: 100, NetworkUpMbps: 100}
}
func TestWHMCSQuotaSharedBytesAndImmediatePolicing(t *testing.T) {
	c := quotaFixture()
	c.TrafficUsedRX = 100
	c.TrafficUsedTX = 200
	rules, err := quotaRules(c, "veth123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(rules, "quota total {") != 1 || strings.Count(rules, "quota name \"total\"") != 2 || !strings.Contains(rules, "over 1073741524 bytes") || strings.Count(rules, "250000 bytes/second burst 65536 bytes drop") != 2 {
		t.Fatal(rules)
	}
	c.TrafficUsedRX = 1073741824
	rules, _ = quotaRules(c, "veth123")
	if !strings.Contains(rules, "over 0 bytes") {
		t.Fatal("exhausted instance received a new allowance")
	}
	c.NetworkDownMbps = 1
	rules, _ = quotaRules(c, "veth123")
	if !strings.Contains(rules, "125000 bytes/second") {
		t.Fatal("overage raised a slower package rate")
	}
	c.TrafficMode = "in_out"
	c.TrafficInGB = 1
	c.TrafficOutGB = 0
	rules, _ = quotaRules(c, "veth123")
	if strings.Contains(rules, "quota upload") || !strings.Contains(rules, "quota download") {
		t.Fatal("unlimited direction received a byte budget")
	}
	if _, err = quotaRules(c, "bad; flush ruleset"); err == nil {
		t.Fatal("unsafe interface accepted")
	}
}
func TestWHMCSQuotaDaemonRestartPreservesConsumedKernelBudget(t *testing.T) {
	oldCommand, oldCache := whmcsQuotaCommand, whmcsQuotaCache
	defer func() { whmcsQuotaCommand, whmcsQuotaCache = oldCommand, oldCache }()
	c := quotaFixture()
	sig := quotaSignature(c, "veth123")
	writes := 0
	whmcsQuotaCache = map[string]string{}
	whmcsQuotaCommand = func(input string, args ...string) ([]byte, error) {
		if input != "" {
			writes++
			return nil, nil
		}
		return []byte(fmt.Sprintf(`{"nftables":[{"table":{"comment":%q}}]}`, sig)), nil
	}
	if err := NewManager().syncWHMCSQuotaLocked(&c, "veth123", false); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatal("daemon restart reset the existing kernel quota")
	}
	c.Name = "cs"
	c.UUID = "another-uuid"
	if err := NewManager().syncWHMCSQuotaLocked(&c, "veth123", true); err != nil || writes != 0 {
		t.Fatal("unrelated guest modified")
	}
}
func TestWHMCSQuotaResetFailureRetriesWithoutAnotherTrafficClear(t *testing.T) {
	oldCommand, oldCache := whmcsQuotaCommand, whmcsQuotaCache
	defer func() { whmcsQuotaCommand, whmcsQuotaCache = oldCommand, oldCache }()
	c := quotaFixture()
	oldSig := quotaSignature(c, "veth123")
	c.TrafficQuotaRevision++
	writes := 0
	whmcsQuotaCache = map[string]string{}
	whmcsQuotaCommand = func(input string, args ...string) ([]byte, error) {
		if input != "" {
			writes++
			if writes == 1 {
				return nil, fmt.Errorf("interrupted nft transaction")
			}
			return nil, nil
		}
		return []byte(fmt.Sprintf(`{"nftables":[{"table":{"comment":%q}}]}`, oldSig)), nil
	}
	m := NewManager()
	if err := m.syncWHMCSQuotaLocked(&c, "veth123", true); err == nil {
		t.Fatal("failed kernel update reported success")
	}
	c.TrafficUsedRX = 123
	if err := m.syncWHMCSQuotaLocked(&c, "veth123", false); err != nil || writes != 2 || c.TrafficUsedRX != 123 {
		t.Fatal("committed reset did not safely recover", err, writes)
	}
}
func TestWHMCSQuotaInstalledBeforeGuestBoot(t *testing.T) {
	oldCommand, oldCache := whmcsQuotaCommand, whmcsQuotaCache
	defer func() { whmcsQuotaCommand, whmcsQuotaCache = oldCommand, oldCache }()
	c := quotaFixture()
	c.ID = 1
	c.LXCName = "ct-1"
	c.TrafficResetDate = time.Now().Format("2006-01")
	m := &Manager{LxcPath: t.TempDir()}
	dir := filepath.Join(m.LxcPath, c.LxcName())
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("lxc.net.0.type = veth\nlxc.net.0.link = lxcbr0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	applied := ""
	whmcsQuotaCache = map[string]string{}
	whmcsQuotaCommand = func(input string, args ...string) ([]byte, error) {
		if input == "" {
			return nil, fmt.Errorf("table absent")
		}
		applied = input
		return nil, nil
	}
	if err := m.prepareWHMCSQuotaStart(&c); err != nil {
		t.Fatal(err)
	}
	device := "veth" + quotaIdentity(c)[:11]
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "lxc.net.0.veth.pair = "+device) || !strings.Contains(applied, device) || lastTrafficSnapshot[c.LxcName()].RXBytes != 0 || lastTrafficSnapshot[c.LxcName()].TXBytes != 0 {
		t.Fatal("boot was not protected by the stable interface quota and zero counter baseline")
	}
}
