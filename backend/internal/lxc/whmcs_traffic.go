package lxc

import (
	"clicd/internal/config"
	"fmt"
	"time"
)

func (m *Manager) WHMCSTrafficState(id int, key string) (map[string]interface{}, error) {
	if m.GetTrafficInfo(id) == nil {
		return nil, fmt.Errorf("LXC not found")
	}
	lastTrafficSnapshotMu.Lock()
	defer lastTrafficSnapshotMu.Unlock()
	c := config.FindContainer(id)
	if c == nil || c.IsKVM() {
		return nil, fmt.Errorf("LXC not found")
	}
	now := time.Now()
	end := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	var receipt *config.WHMCSTrafficReceipt
	var err error
	if key != "" {
		receipt, err = config.WHMCSTrafficReceiptFor(c.UUID, key)
		if err != nil {
			return nil, err
		}
	}
	return map[string]interface{}{"uuid": c.UUID, "cycle": now.Format("2006-01"), "cycle_end": end.Unix(), "server_time": now.Unix(), "total_used_bytes": c.TrafficUsedRX + c.TrafficUsedTX, "receipt": receipt}, nil
}
func (m *Manager) WHMCSResetPaidTraffic(id int, key, cycle string) (*config.WHMCSTrafficReceipt, error) {
	lastTrafficSnapshotMu.Lock()
	defer lastTrafficSnapshotMu.Unlock()
	c := config.FindContainer(id)
	if c == nil || c.IsKVM() {
		return nil, fmt.Errorf("LXC not found")
	}
	// Receipt lookup precedes period validation so an old successful request remains recoverable.
	receipt, err := config.WHMCSTrafficReceiptFor(c.UUID, key)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		if receipt.Cycle != cycle {
			return nil, fmt.Errorf("receipt cycle mismatch")
		}
		if c.Status == "running" {
			if err = m.syncWHMCSQuotaLocked(c, "", false); err != nil {
				return nil, err
			}
		}
		return receipt, nil
	}
	if cycle != time.Now().Format("2006-01") {
		return nil, fmt.Errorf("traffic cycle ended; new cycle was not reset")
	}
	if c.BillingBlocked() || IsExpired(*c) {
		return nil, fmt.Errorf("instance is restricted or expired")
	}
	if c.MonthlyTrafficGB <= 0 {
		return nil, fmt.Errorf("finite monthly quota required")
	}
	receipt, err = config.WHMCSResetTrafficOnce(c, key, cycle)
	if err != nil {
		return nil, err
	}
	// New traffic starts at the actual network counters observed at this reset.
	if c.Status == "running" {
		rx, tx := m.getContainerNetworkBytes(c.LxcName())
		lastTrafficSnapshot[c.LxcName()] = trafficSample{RXBytes: rx, TXBytes: tx}
	} else {
		delete(lastTrafficSnapshot, c.LxcName())
	}
	if c.Status == "running" {
		if err = m.syncWHMCSQuotaLocked(c, "", true); err != nil {
			return nil, err
		}
	}
	return receipt, nil
}
