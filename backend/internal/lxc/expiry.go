package lxc

import (
	"fmt"
	"time"

	"clicd/internal/config"
)

// IsExpired checks if a container has passed its expiration date
func IsExpired(c config.Container) bool {
	return isContainerExpired(c, time.Now())
}

// StopExpiredContainers stops running containers whose expiration date has passed.
func (m *Manager) StopExpiredContainers(now time.Time) {
	for _, container := range config.AppConfig.Containers {
		if container.IsKVM() || !isContainerExpired(container, now) {
			continue
		}

		status, err := m.GetContainerStatus(container.LxcName())
		if err != nil {
			status = container.Status
		}
		if status != "running" {
			continue
		}

		fmt.Printf("Container %s (ID=%d) expired at %s, stopping...\n", container.Name, container.ID, container.ExpiresAt)
		if err := m.StopContainer(container.ID); err != nil {
			fmt.Printf("Warning: failed to stop expired container %s: %v\n", container.Name, err)
		}
	}
}

// StartExpiryScanner runs a background loop that tracks traffic & stops expired/over-traffic containers every 30 seconds
func (m *Manager) StartExpiryScanner() {
	m.AccumulateTraffic()
	go func() {
		for {
			time.Sleep(30 * time.Second)
			now := time.Now()
			m.AccumulateTraffic() // track network traffic deltas
			m.StopExpiredContainers(now)
			m.StopTrafficExceededContainers(now)
		}
	}()
}

// StopTrafficExceededContainers stops running containers that have exceeded their monthly traffic limit
func (m *Manager) StopTrafficExceededContainers(now time.Time) {
	lastTrafficSnapshotMu.Lock()
	defer lastTrafficSnapshotMu.Unlock()
	currentMonth := now.Format("2006-01")
	saved := false
	for i := range config.AppConfig.Containers {
		c := &config.AppConfig.Containers[i]
		if c.IsKVM() || c.Status != "running" {
			continue
		}

		if WHMCSTrafficManaged(*c) {
			continue
		}
		// Reset traffic if new month
		if c.TrafficResetDate != currentMonth {
			c.TrafficUsedRX = 0
			c.TrafficUsedTX = 0
			c.TrafficResetDate = currentMonth
			saved = true
			continue
		}

		// Check traffic limits
		if isTrafficExceeded(*c) {
			fmt.Printf("Container %s (ID=%d) exceeded traffic limit, stopping...\n", c.Name, c.ID)
			if err := m.StopContainer(c.ID); err != nil {
				fmt.Printf("Warning: failed to stop traffic-exceeded container %s: %v\n", c.Name, err)
			}
		}
	}
	if saved {
		config.SaveConfig()
	}
}

func isTrafficExceeded(c config.Container) bool {
	if c.TrafficMode == "in_out" {
		inLimit := int64(c.TrafficInGB) * 1073741824
		outLimit := int64(c.TrafficOutGB) * 1073741824
		if inLimit > 0 && c.TrafficUsedRX >= inLimit {
			return true
		}
		if outLimit > 0 && c.TrafficUsedTX >= outLimit {
			return true
		}
		return false
	}
	totalLimit := int64(c.MonthlyTrafficGB) * 1073741824
	return totalLimit > 0 && (c.TrafficUsedRX+c.TrafficUsedTX) >= totalLimit
}

// ResetTraffic resets traffic counters for a container
func (m *Manager) ResetTraffic(id int) error {
	lastTrafficSnapshotMu.Lock()
	defer lastTrafficSnapshotMu.Unlock()
	c := config.FindContainer(id)
	if c == nil {
		return fmt.Errorf("container not found: %d", id)
	}
	oldRX, oldTX, oldDate, oldRevision := c.TrafficUsedRX, c.TrafficUsedTX, c.TrafficResetDate, c.TrafficQuotaRevision
	c.TrafficUsedRX = 0
	c.TrafficUsedTX = 0
	c.TrafficResetDate = time.Now().Format("2006-01")
	c.TrafficQuotaRevision++
	if err := config.SaveConfig(); err != nil {
		c.TrafficUsedRX = oldRX
		c.TrafficUsedTX = oldTX
		c.TrafficResetDate = oldDate
		c.TrafficQuotaRevision = oldRevision
		return err
	}
	if c.Status == "running" {
		rx, tx := m.getContainerNetworkBytes(c.LxcName())
		lastTrafficSnapshot[c.LxcName()] = trafficSample{RXBytes: rx, TXBytes: tx}
	} else {
		delete(lastTrafficSnapshot, c.LxcName())
	}
	if c.Status == "running" {
		if err := m.syncWHMCSQuotaLocked(c, "", true); err != nil {
			return err
		}
	}
	return nil
}

// IsTrafficExceeded checks if a container has exceeded its traffic limit
func IsTrafficExceeded(c config.Container) bool {
	return isTrafficExceeded(c)
}

func isContainerExpired(container config.Container, now time.Time) bool {
	expiresAt, ok := ParseExpiration(container.ExpiresAt)
	return ok && !now.Before(expiresAt)
}

// ParseExpiration parses an expiration string. A YYYY-MM-DD value expires at the
// end of that local day, while RFC3339 values are treated as exact timestamps.
func ParseExpiration(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}

	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}

	if parsed, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return parsed.Add(24 * time.Hour), true
	}

	return time.Time{}, false
}
