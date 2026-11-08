package lxc

import (
	"errors"
	"reflect"
	"testing"

	"clicd/internal/config"
)

func fixedPortTest(t *testing.T) {
	setupManualPortTest(t)
	previous := saveFixedPortConfig
	saveFixedPortConfig = func() error { return nil }
	t.Cleanup(func() { saveFixedPortConfig = previous })
}

func TestFixedPoolsReserveQuotaWithoutRulesAndCompeteAcrossBatch(t *testing.T) {
	fixedPortTest(t)
	manual := false
	inputs := []ContainerConfig{{Name: "fixed-a", FixedPortAllocation: true, AutoPortMappings: &manual, PortMappingCount: 3}, {Name: "fixed-b", FixedPortAllocation: true, AutoPortMappings: &manual, PortMappingCount: 2}}
	for i := range inputs {
		if err := inputs[i].NormalizeCreateNATMappings(); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := ReserveBatchCreateNATPorts(inputs)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, cfg := range plans {
		if len(cfg.NATPortAllocations) != cfg.PortMappingCount || len(cfg.NATPortMappings) != 0 || cfg.ManagementPort != 0 {
			t.Fatalf("incorrect fixed plan: %+v", cfg)
		}
		for _, port := range cfg.NATPortAllocations {
			if seen[port] {
				t.Fatal("fixed batches share a port")
			}
			seen[port] = true
		}
		port, release, err := ReserveCreateNATPorts(cfg)
		if err != nil || port != 0 {
			t.Fatalf("claim: %d %v", port, err)
		}
		if len(createNATReservations) == 0 {
			t.Fatal("fixed plan lost its active reservation")
		}
		release()
	}
	if len(queuedCreateNATReservations) != 0 || len(createNATReservations) != 0 {
		t.Fatal("reservation leaked")
	}
}

func TestFixedPoolMigrationIsStablePreservesBoundMappingsAndProtectsPorts(t *testing.T) {
	fixedPortTest(t)
	config.AppConfig.Containers = []config.Container{
		{ID: 1, Name: "fixed", PortMappingLimit: 3, PortMappings: []config.PortMapping{{HostPort: 30004, ContainerPort: 22, Protocol: "tcp"}, {HostIP: "192.0.2.10", HostPort: 80, ContainerPort: 80, Protocol: "tcp"}}, PublicIPv4s: []config.PublicIPv4Assignment{{Address: "192.0.2.10"}}},
		{ID: 2, Name: "bound-other", PortMappingLimit: 1, PortMappings: []config.PortMapping{{HostIP: "192.0.2.11", HostPort: 80, ContainerPort: 80, Protocol: "tcp"}}, PublicIPv4s: []config.PublicIPv4Assignment{{Address: "192.0.2.11"}}},
	}
	c := &config.AppConfig.Containers[0]
	next := *c
	next.FixedPortAllocation = true
	if err := ChangeFixedPortPolicy(c, next); err != nil {
		t.Fatal(err)
	}
	if len(c.NATPortAllocations) != 2 || !containsPort(c.NATPortAllocations, 30004) || containsPort(c.NATPortAllocations, 80) {
		t.Fatalf("bound IP migration changed semantics: %+v", c)
	}
	before := append([]int(nil), c.NATPortAllocations...)
	if err := ChangeFixedPortPolicy(c, *c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, c.NATPortAllocations) {
		t.Fatal("idempotent policy reassigned fixed ports")
	}
	other := &config.AppConfig.Containers[1]
	if _, err := NewManager().AddPortMapping(other.ID, config.PortMapping{HostPort: before[0], ContainerPort: 443, Protocol: "udp"}); err == nil {
		t.Fatal("another instance stole a reserved fixed UDP port")
	}
	if _, err := normalizePortMapping(c, 1, config.PortMapping{HostIP: "192.0.2.10", HostPort: 80, ContainerPort: 8080, Protocol: "tcp"}); err != nil {
		t.Fatal("legacy bound mapping cannot be edited:", err)
	}
	if _, err := NewManager().AddPortMapping(c.ID, config.PortMapping{HostPort: 30010, ContainerPort: 443, Protocol: "tcp"}); err == nil {
		t.Fatal("unallocated external port accepted")
	}
	if _, err := NewManager().DeletePortMapping(c.ID, 0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, c.NATPortAllocations) {
		t.Fatal("deleting a rule released the fixed pool")
	}
	if HostPortAvailable(other, "", before[0], "tcp") {
		t.Fatal("deleted mapping's fixed port became available")
	}
	if _, err := NewManager().AddPortMapping(c.ID, config.PortMapping{HostPort: before[0], ContainerPort: 2222, Protocol: "tcp"}); err != nil {
		t.Fatal("fixed owner cannot reuse its port:", err)
	}
	if _, err := NewManager().DeletePortMapping(c.ID, 0); err != nil {
		t.Fatal(err)
	}
	if len(c.NATPortAllocations) != c.PortMappingLimit {
		t.Fatal("deleting the legacy bound mapping did not restore full fixed quota")
	}
}

func TestFixedPolicyAndMappingPersistenceFailureRollBackOwnership(t *testing.T) {
	fixedPortTest(t)
	config.AppConfig.Containers = []config.Container{{ID: 1, Name: "fail", PortMappingLimit: 2}}
	c := &config.AppConfig.Containers[0]
	before := *c
	next := *c
	next.FixedPortAllocation = true
	saveFixedPortConfig = func() error { return errors.New("fixture disk failure") }
	if err := ChangeFixedPortPolicy(c, next); !errors.Is(err, ErrFixedPortPersistence) {
		t.Fatalf("expected persistence error: %v", err)
	}
	if !reflect.DeepEqual(before, *c) || len(config.FixedNATReservedPorts("")) != 0 {
		t.Fatal("failed save retained new fixed ownership")
	}
	c.FixedPortAllocation = true
	c.NATPortAllocations = []int{30001, 30002}
	before = *c
	if _, err := NewManager().AddPortMapping(c.ID, config.PortMapping{HostPort: 30001, ContainerPort: 80, Protocol: "tcp"}); err == nil {
		t.Fatal("failed mapping save was reported successful")
	}
	if !reflect.DeepEqual(before, *c) {
		t.Fatal("failed mapping save changed live rules")
	}
}

func TestFixedPolicyRuntimeFailureRollsBackPoolBeforeUnlock(t *testing.T) {
	fixedPortTest(t)
	config.AppConfig.Containers = []config.Container{{ID: 1, Name: "rollback", FixedPortAllocation: true, PortMappingLimit: 2, NATPortAllocations: []int{30000, 30001}, CPUPercent: 10}}
	c := &config.AppConfig.Containers[0]
	before := *c
	next := *c
	next.PortMappingLimit = 1
	next.CPUPercent = 50
	calls := 0
	err := ChangeFixedPortPolicy(c, next, func(current *config.Container) error {
		calls++
		if calls == 1 {
			return errors.New("fixture runtime rejection")
		}
		if !reflect.DeepEqual(*current, before) {
			t.Fatal("runtime rollback did not receive original allocation")
		}
		return nil
	})
	if !errors.Is(err, ErrFixedPortApply) || calls != 2 || !reflect.DeepEqual(*c, before) {
		t.Fatalf("policy did not roll back atomically: %v %+v", err, c)
	}
}

func TestFixedIPv6IndependentPoolAndSyncChangesPreserveOwnership(t *testing.T) {
	fixedPortTest(t)
	setupIPv6PortMappingTest(t, true)
	config.AppConfig.NATPortStart, config.AppConfig.NATPortEnd = 30000, 30010
	config.AppConfig.Containers = []config.Container{{ID: 1, Name: "dual-fixed", FixedPortAllocation: true, PortMappingLimit: 2, IPv6PortMappingLimit: 3, NATPortAllocations: []int{30000, 30001}, IPv6NATAddress: "fd00::2", PortMappings: []config.PortMapping{{HostPort: 30000, ContainerPort: 22, Protocol: "tcp"}}}, {ID: 2, Name: "other", PortMappingLimit: 2, IPv6NATAddress: "fd00::3"}}
	c := &config.AppConfig.Containers[0]
	if _, err := NewManager().SetIPv6NATSync(c.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(c.IPv6NATPortAllocations) != 3 || !containsPort(c.IPv6NATPortAllocations, 30000) {
		t.Fatalf("independent pool did not preserve mirrored port: %+v", c)
	}
	if _, err := NewManager().AddIPv6PortMapping(c.ID, config.PortMapping{HostPort: 30010, ContainerPort: 443, Protocol: "tcp"}); err == nil {
		t.Fatal("unallocated independent IPv6 port accepted")
	}
	if _, err := NewManager().DeleteIPv6PortMapping(c.ID, 0); err != nil {
		t.Fatal(err)
	}
	if len(c.IPv6NATPortAllocations) != 3 || IPv6HostPortAvailable(&config.AppConfig.Containers[1], c.IPv6NATPortAllocations[2], "udp") {
		t.Fatal("deleting IPv6 rule released its fixed pool")
	}
	if _, err := NewManager().SetIPv6NATSync(c.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(c.IPv6NATPortAllocations) != 0 || !reflect.DeepEqual(c.NATPortAllocations, []int{30000, 30001}) {
		t.Fatal("synchronization changed the IPv4 pool")
	}
}

func TestFixedPoolQuotaChangesPreserveActiveMappingsAndRefuseUnsafeShrink(t *testing.T) {
	fixedPortTest(t)
	config.AppConfig.Containers = []config.Container{{ID: 1, Name: "quota", FixedPortAllocation: true, PortMappingLimit: 3, NATPortAllocations: []int{30000, 30001, 30002}, PortMappings: []config.PortMapping{{HostPort: 30002, ContainerPort: 80, Protocol: "tcp"}}}}
	c := &config.AppConfig.Containers[0]
	next := *c
	next.PortMappingLimit = 1
	if err := ChangeFixedPortPolicy(c, next); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.NATPortAllocations, []int{30002}) {
		t.Fatalf("shrink discarded active external port: %v", c.NATPortAllocations)
	}
	before := *c
	next = *c
	next.PortMappingLimit = 0
	if err := ChangeFixedPortPolicy(c, next); err == nil {
		t.Fatal("shrink below active mapping was accepted")
	}
	if !reflect.DeepEqual(before, *c) {
		t.Fatal("failed shrink changed ownership")
	}
	next = *c
	next.PortMappingLimit = 4
	if err := ChangeFixedPortPolicy(c, next); err != nil {
		t.Fatal(err)
	}
	if c.NATPortAllocations[0] != 30002 || len(c.NATPortAllocations) != 4 {
		t.Fatal("growth changed existing allocation")
	}
}

func TestPersistedFixedCreatePlansReservePortsBeforeQueueRecovery(t *testing.T) {
	fixedPortTest(t)
	config.AppConfig.Tasks = []config.SavedTask{{ID: "pending-fixed", Type: "create", ContainerName: "planned", Status: "pending", Config: `{"name":"planned","fixed_port_allocation":true,"auto_port_mappings":false,"port_mapping_count":2,"nat_port_allocations":[30000,30001]}`}}
	if port, err := config.PreviewSSHPortExcluding(nil); err != nil || port < 30002 {
		t.Fatalf("allocator stole persisted plan: %d %v", port, err)
	}
	manual := false
	plans, err := ReserveBatchCreateNATPorts([]ContainerConfig{{Name: "new", FixedPortAllocation: true, AutoPortMappings: &manual, PortMappingCount: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range plans[0].NATPortAllocations {
		if port < 30002 {
			t.Fatal("new fixed plan stole queued persistent port")
		}
	}
	ReleaseQueuedCreateNATPorts("new")
	c := &config.Container{ID: 3, Name: "other", PortMappingLimit: 2}
	if HostPortAvailable(c, "", 30000, "udp") {
		t.Fatal("manual allocation stole persisted plan")
	}
	config.AppConfig.Tasks[0].Status = "failed"
	if !HostPortAvailable(c, "", 30000, "udp") {
		t.Fatal("terminal create intent did not release fixed ports")
	}
}
