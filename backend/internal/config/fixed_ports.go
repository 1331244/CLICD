package config

import "strings"

// FixedNATReservedPorts includes persisted create tasks, so a restart does not
// make a queued instance's port allocation available to another workload.
func FixedNATReservedPorts(exceptName string) map[int]bool {
	ports := map[int]bool{}
	if AppConfig == nil {
		return ports
	}
	for _, c := range AppConfig.Containers {
		if !c.FixedPortAllocation || strings.EqualFold(c.Name, exceptName) {
			continue
		}
		for _, port := range c.NATPortAllocations {
			ports[port] = true
		}
		for _, port := range c.IPv6NATPortAllocations {
			ports[port] = true
		}
	}
	for _, task := range AppConfig.Tasks {
		if task.Type != "create" || (task.Status != "pending" && task.Status != "running") {
			continue
		}
		cfg := parseSavedTaskConfig(task.Config)
		if !cfg.FixedPortAllocation || strings.EqualFold(cfg.Name, exceptName) {
			continue
		}
		for _, port := range cfg.NATPortAllocations {
			ports[port] = true
		}
		for _, port := range cfg.IPv6NATPortAllocations {
			ports[port] = true
		}
	}
	return ports
}
