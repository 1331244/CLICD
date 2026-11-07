package lxc

import (
	"clicd/internal/config"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Only instances provisioned with the WHMCS module's reserved identity use this policy.
var whmcsTrafficName = regexp.MustCompile(`^whmcs-[1-9][0-9]*-[a-f0-9]{12}$`)
var quotaInterface = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)
var whmcsQuotaCache = map[string]string{} // guarded by lastTrafficSnapshotMu

func WHMCSTrafficManaged(c config.Container) bool {
	return !c.IsKVM() && whmcsTrafficName.MatchString(c.Name)
}
func WHMCSTrafficQuotaAvailable() bool { _, err := exec.LookPath("nft"); return err == nil }

var whmcsQuotaCommand = func(input string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nft", args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("nft quota: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func quotaIdentity(c config.Container) string {
	sum := sha256.Sum256([]byte(c.UUID))
	return hex.EncodeToString(sum[:8])
}
func quotaRate(rate int) int {
	if rate > 0 && rate < 2 {
		return rate * 125000
	}
	return 250000
}
func quotaBudget(limit int, used int64) int64 {
	n := int64(limit)*1073741824 - used
	if n < 0 {
		return 0
	}
	return n
}
func quotaFinite(c config.Container) bool {
	if c.TrafficMode == "in_out" {
		return c.TrafficInGB > 0 || c.TrafficOutGB > 0
	}
	return c.MonthlyTrafficGB > 0
}
func quotaSignature(c config.Container, device string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d|%d|%d|%d|%d|%d", c.UUID, device, c.TrafficResetDate, c.TrafficMode, c.MonthlyTrafficGB, c.TrafficInGB, c.TrafficOutGB, quotaRate(c.NetworkDownMbps), quotaRate(c.NetworkUpMbps), c.TrafficQuotaRevision)))
	return hex.EncodeToString(sum[:])
}
func quotaRules(c config.Container, device string) (string, error) {
	if !quotaInterface.MatchString(device) || c.UUID == "" {
		return "", fmt.Errorf("invalid quota interface or UUID")
	}
	name := "clq_" + quotaIdentity(c)
	var b strings.Builder
	fmt.Fprintf(&b, "table bridge %s {\n comment \"%s\"\n", name, quotaSignature(c, device))
	object := func(key string, n int64) { fmt.Fprintf(&b, " quota %s { over %d bytes; }\n", key, n) }
	inObject, outObject := "total", "total"
	if c.TrafficMode == "in_out" {
		inObject, outObject = "download", "upload"
		if c.TrafficInGB > 0 {
			object(inObject, quotaBudget(c.TrafficInGB, c.TrafficUsedRX))
		}
		if c.TrafficOutGB > 0 {
			object(outObject, quotaBudget(c.TrafficOutGB, c.TrafficUsedTX))
		}
	} else if c.MonthlyTrafficGB > 0 {
		object("total", quotaBudget(c.MonthlyTrafficGB, c.TrafficUsedRX+c.TrafficUsedTX))
	}
	chain := func(name, hook, field, key string, priority, rate int, limited bool) {
		fmt.Fprintf(&b, " chain %s { type filter hook %s priority %d; policy accept;\n", name, hook, priority)
		if limited {
			fmt.Fprintf(&b, " %s %q quota name %q limit rate over %d bytes/second burst 65536 bytes drop\n", field, device, key, quotaRate(rate))
		}
		b.WriteString(" }\n")
	}
	chain("up", "prerouting", "iifname", outObject, -300, c.NetworkUpMbps, c.TrafficMode != "in_out" || c.TrafficOutGB > 0)
	chain("down", "postrouting", "oifname", inObject, 300, c.NetworkDownMbps, c.TrafficMode != "in_out" || c.TrafficInGB > 0)
	b.WriteString("}\n")
	return b.String(), nil
}

// Atomic table replacement touches only this UUID. The shared named quota switches
// BOTH directions on the packet crossing the allowance, independently of polling.
func (m *Manager) syncWHMCSQuotaLocked(c *config.Container, device string, force bool) error {
	if !WHMCSTrafficManaged(*c) {
		return nil
	}
	name := "clq_" + quotaIdentity(*c)
	if device == "" {
		device = m.getContainerVethByNS(c.LxcName())
		if device == "" {
			return fmt.Errorf("cannot find managed LXC quota interface")
		}
	}
	sig := quotaSignature(*c, device)
	if !force && whmcsQuotaCache[c.UUID] == sig {
		return nil
	}
	current, lookupErr := whmcsQuotaCommand("", "-j", "list", "table", "bridge", name)
	if !quotaFinite(*c) {
		if lookupErr == nil {
			if _, err := whmcsQuotaCommand("delete table bridge "+name+"\n", "-f", "-"); err != nil {
				return err
			}
		}
		delete(whmcsQuotaCache, c.UUID)
		return nil
	}
	if !force && lookupErr == nil {
		var data struct {
			Nftables []struct {
				Table *struct {
					Comment string `json:"comment"`
				} `json:"table"`
			} `json:"nftables"`
		}
		if json.Unmarshal(current, &data) == nil {
			for _, entry := range data.Nftables {
				if entry.Table != nil && entry.Table.Comment == sig {
					whmcsQuotaCache[c.UUID] = sig
					return nil
				}
			}
		}
	}
	rules, err := quotaRules(*c, device)
	if err != nil {
		return err
	}
	if lookupErr == nil {
		rules = "delete table bridge " + name + "\n" + rules
	}
	delete(whmcsQuotaCache, c.UUID)
	if _, err = whmcsQuotaCommand(rules, "-f", "-"); err != nil {
		return err
	}
	whmcsQuotaCache[c.UUID] = sig
	return nil
}

// Install the quota BEFORE boot, against a stable host veth name. A guest cannot
// obtain another full-speed allowance by rebooting or restarting the daemon.
func (m *Manager) prepareWHMCSQuotaStart(c *config.Container) error {
	if !WHMCSTrafficManaged(*c) {
		return nil
	}
	path := filepath.Join(m.LxcPath, c.LxcName(), "config")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "lxc.net.0.type = macvlan") {
		return fmt.Errorf("managed traffic quota requires a bridged veth")
	}
	device := "veth" + quotaIdentity(*c)[:11]
	lines := strings.Split(string(data), "\n")
	kept := []string{}
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "lxc.net.0.veth.pair") {
			kept = append(kept, line)
		}
	}
	kept = append(kept, "lxc.net.0.veth.pair = "+device)
	if err = os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0644); err != nil {
		return err
	}
	lastTrafficSnapshotMu.Lock()
	defer lastTrafficSnapshotMu.Unlock()
	if c.TrafficResetDate != time.Now().Format("2006-01") {
		c.TrafficUsedRX = 0
		c.TrafficUsedTX = 0
		c.TrafficResetDate = time.Now().Format("2006-01")
		if err = config.SaveConfig(); err != nil {
			return err
		}
	}
	lastTrafficSnapshot[c.LxcName()] = trafficSample{}
	return m.syncWHMCSQuotaLocked(c, device, false)
}
func (m *Manager) RefreshWHMCSTrafficQuota(id int) error {
	c := config.FindContainer(id)
	if c == nil {
		return fmt.Errorf("container not found")
	}
	if !WHMCSTrafficManaged(*c) {
		return nil
	}
	if m.GetTrafficInfo(id) == nil {
		return fmt.Errorf("container not found")
	}
	lastTrafficSnapshotMu.Lock()
	defer lastTrafficSnapshotMu.Unlock()
	c = config.FindContainer(id)
	if c == nil || c.Status != "running" {
		return nil
	}
	return m.syncWHMCSQuotaLocked(c, "", false)
}
func (m *Manager) removeWHMCSQuota(c *config.Container) {
	if !WHMCSTrafficManaged(*c) {
		return
	}
	lastTrafficSnapshotMu.Lock()
	defer lastTrafficSnapshotMu.Unlock()
	name := "clq_" + quotaIdentity(*c)
	if _, err := whmcsQuotaCommand("", "list", "table", "bridge", name); err == nil {
		_, _ = whmcsQuotaCommand("delete table bridge "+name+"\n", "-f", "-")
	}
	delete(whmcsQuotaCache, c.UUID)
}
