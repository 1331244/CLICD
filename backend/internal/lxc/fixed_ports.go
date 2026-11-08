package lxc

import (
	"errors"
	"fmt"
	"strings"

	"clicd/internal/config"
)

var saveFixedPortConfig = config.SaveConfig
var ErrFixedPortPersistence = errors.New("cannot persist fixed port policy")
var ErrFixedPortApply = errors.New("cannot apply fixed port policy")

func containsPort(ports []int, port int) bool {
	for _, candidate := range ports {
		if candidate == port {
			return true
		}
	}
	return false
}

func samePortNumbers(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i, port := range left {
		if right[i] != port {
			return false
		}
	}
	return true
}

func allocatedPortMappings(c *config.Container) []config.PortMapping {
	ports := append(append([]int(nil), c.NATPortAllocations...), c.IPv6NATPortAllocations...)
	result := make([]config.PortMapping, 0, len(ports))
	for _, port := range ports {
		result = append(result, config.PortMapping{HostPort: port, Protocol: "all"})
	}
	return result
}

// Fixed pools reserve the external number for both TCP and UDP, including
// queued creations. A customer can change protocol without losing ownership.
func fixedPortUnavailableLocked(c *config.Container, owner string) map[int]bool {
	used := config.FixedNATReservedPorts(c.Name)
	if config.AppConfig != nil {
		for _, other := range config.AppConfig.Containers {
			if other.ID == c.ID || strings.EqualFold(other.Name, c.Name) {
				continue
			}
			for _, mapping := range other.PortMappings {
				if mapping.HostIP == "" || (!other.IsKVM() && other.IPv6NATAddress != "" && ipv6MappingsSyncIPv4(&other)) {
					used[mapping.HostPort] = true
				}
			}
			for _, mapping := range other.IPv6PortMappings {
				used[mapping.HostPort] = true
			}
		}
	}
	for id, reservations := range createNATReservations {
		if activeCreateNATOwners[id] == owner {
			continue
		}
		for _, mapping := range reservations {
			used[mapping.HostPort] = true
		}
	}
	for name, reservations := range queuedCreateNATReservations {
		if name == owner {
			continue
		}
		for _, mapping := range reservations {
			used[mapping.HostPort] = true
		}
	}
	return used
}

func fixedPool(existing []int, mappings []config.PortMapping, limit int, unavailable map[int]bool) ([]int, error) {
	if limit < 0 || limit > 64 {
		return nil, fmt.Errorf("fixed NAT port quota must be between 0 and 64")
	}
	required := []int{}
	for _, mapping := range mappings {
		if !containsPort(required, mapping.HostPort) {
			required = append(required, mapping.HostPort)
		}
	}
	if len(required) > limit {
		return nil, fmt.Errorf("fixed NAT port quota is below active external port use")
	}
	preferred := append(append([]int(nil), existing...), required...)
	if len(existing) > limit {
		preferred = append(append([]int(nil), required...), existing...)
	}
	result := make([]int, 0, limit)
	for _, port := range preferred {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid persisted fixed NAT port %d", port)
		}
		if containsPort(result, port) {
			continue
		}
		if unavailable[port] {
			return nil, fmt.Errorf("fixed NAT port %d is already owned by another instance or create task", port)
		}
		if len(result) < limit {
			result = append(result, port)
		}
	}
	start, end := config.NATPortRange()
	for port := start; port <= end && len(result) < limit; port++ {
		if !unavailable[port] && !containsPort(result, port) {
			result = append(result, port)
		}
	}
	if len(result) != limit {
		return nil, fmt.Errorf("not enough free external ports for fixed NAT quota %d", limit)
	}
	for _, port := range required {
		if !containsPort(result, port) {
			return nil, fmt.Errorf("fixed NAT allocation would discard an active mapping")
		}
	}
	return result, nil
}

func planFixedPortPoolsLocked(c *config.Container) error {
	if !c.FixedPortAllocation {
		return nil
	}
	used := fixedPortUnavailableLocked(c, createNATReservationOwner(c.Name))
	var err error
	shared := []config.PortMapping{}
	boundCount := 0
	for _, mapping := range c.PortMappings {
		if mapping.HostIP != "" {
			boundCount++
		} else {
			shared = append(shared, mapping)
		}
	}
	c.NATPortAllocations, err = fixedPool(c.NATPortAllocations, shared, c.PortMappingLimit-boundCount, used)
	if err != nil {
		return err
	}
	if c.IsKVM() || ipv6MappingsSyncIPv4(c) {
		c.IPv6NATPortAllocations = nil
		return nil
	}
	// Reserve an independent IPv6 pool even during an uplink outage. An
	// administratively disabled NAT66 service does not consume an extra pool.
	if config.AppConfig != nil && config.AppConfig.IPv6NAT.Enabled {
		c.IPv6NATPortAllocations, err = fixedPool(c.IPv6NATPortAllocations, c.IPv6PortMappings, ipv6MappingLimit(c), used)
		return err
	}
	return nil
}

// ChangeFixedPortPolicy serializes policy changes with every mapping and create
// reservation. Both the candidate state and its port ownership commit together.
func ChangeFixedPortPolicy(current *config.Container, next config.Container, apply ...func(*config.Container) error) error {
	portMappingMutationMu.Lock()
	defer portMappingMutationMu.Unlock()
	if current == nil {
		return fmt.Errorf("container is required")
	}
	if current.FixedPortAllocation && !next.FixedPortAllocation {
		return fmt.Errorf("fixed port allocation cannot be disabled while the instance exists")
	}
	createNATReservationMu.Lock()
	err := planFixedPortPoolsLocked(&next)
	createNATReservationMu.Unlock()
	if err != nil {
		return err
	}
	if err := validateIPv6MappingInventory(&next); err != nil {
		return err
	}
	previous := *current
	*current = next
	if err := saveFixedPortConfig(); err != nil {
		*current = previous
		return fmt.Errorf("%w: %v", ErrFixedPortPersistence, err)
	}
	if len(apply) > 0 && apply[0] != nil {
		if err := apply[0](current); err != nil {
			*current = previous
			rollbackSave := saveFixedPortConfig()
			rollbackApply := apply[0](current)
			return errors.Join(fmt.Errorf("%w: %v", ErrFixedPortApply, err), rollbackSave, rollbackApply)
		}
	}
	return nil
}

func fixedCreateCandidate(cfg ContainerConfig) config.Container {
	return config.Container{Name: cfg.Name, Virtualization: config.NormalizeVirtualization(cfg.Virtualization), FixedPortAllocation: cfg.FixedPortAllocation,
		PortMappingLimit: cfg.PortMappingCount, IPv6PortMappingLimit: cfg.IPv6PortMappingCount, IPv6NATSyncIPv4: cfg.IPv6NATSyncIPv4,
		NATPortAllocations: append([]int(nil), cfg.NATPortAllocations...), IPv6NATPortAllocations: append([]int(nil), cfg.IPv6NATPortAllocations...)}
}

func planFixedCreatePortsLocked(cfg *ContainerConfig) error {
	c := fixedCreateCandidate(*cfg)
	if err := planFixedPortPoolsLocked(&c); err != nil {
		return err
	}
	cfg.NATPortAllocations, cfg.IPv6NATPortAllocations = c.NATPortAllocations, c.IPv6NATPortAllocations
	return nil
}

// Direct manager callers use the same durable create plan as batch requests.
func EnsureFixedCreatePortPlan(cfg *ContainerConfig) error {
	if cfg == nil || !cfg.FixedPortAllocation {
		return nil
	}
	v6Limit := 0
	if config.AppConfig != nil && config.AppConfig.IPv6NAT.Enabled && config.NormalizeVirtualization(cfg.Virtualization) != config.VirtualizationKVM && cfg.IPv6NATSyncIPv4 != nil && !*cfg.IPv6NATSyncIPv4 {
		v6Limit = cfg.IPv6PortMappingCount
		if v6Limit == 0 {
			v6Limit = cfg.PortMappingCount
		}
	}
	if len(cfg.NATPortAllocations) == cfg.PortMappingCount && len(cfg.IPv6NATPortAllocations) == v6Limit {
		return nil
	}
	planned, err := ReserveBatchCreateNATPorts([]ContainerConfig{*cfg})
	if err != nil {
		return err
	}
	*cfg = planned[0]
	return nil
}

func validateFixedMapping(c *config.Container, mapping config.PortMapping, ipv6 bool) error {
	port := mapping.HostPort
	if c.FixedPortAllocation {
		if ipv6 && ipv6MappingsSyncIPv4(c) {
			for _, old := range c.PortMappings {
				if old.HostIP != "" && old.HostPort == port && old.Protocol == mapping.Protocol && old.ContainerPort == mapping.ContainerPort {
					return nil
				}
			}
		}
		if !ipv6 && mapping.HostIP != "" {
			for _, old := range c.PortMappings {
				if old.HostIP == mapping.HostIP && old.HostPort == port {
					return nil
				}
			}
			return fmt.Errorf("new fixed NAT mappings cannot change their host address binding")
		}
		pool := c.NATPortAllocations
		if ipv6 && !ipv6MappingsSyncIPv4(c) {
			pool = c.IPv6NATPortAllocations
		}
		if !containsPort(pool, port) {
			return fmt.Errorf("external port %d is not allocated to this instance", port)
		}
	}
	if config.FixedNATReservedPorts(c.Name)[port] {
		return fmt.Errorf("external port %d is reserved by another instance", port)
	}
	return nil
}
