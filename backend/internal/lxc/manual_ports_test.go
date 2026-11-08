package lxc

import (
	"encoding/json"
	"reflect"
	"testing"

	"clicd/internal/config"
)

func setupManualPortTest(t *testing.T) {
	t.Helper()
	previous := config.AppConfig
	config.AppConfig = &config.ClicdConfig{NATPortStart: 30000, NATPortEnd: 30010, NextSSHPort: 30000}
	t.Cleanup(func() { config.AppConfig = previous })
	createNATReservationMu.Lock()
	oldReservations, oldQueued := createNATReservations, queuedCreateNATReservations
	oldOwners, oldMirrors, oldQueuedMirrors := activeCreateNATOwners, activeCreateNATMirrorsIPv6, queuedCreateNATMirrorsIPv6
	createNATReservations = map[uint64][]config.PortMapping{}
	queuedCreateNATReservations = map[string][]config.PortMapping{}
	activeCreateNATOwners = map[uint64]string{}
	activeCreateNATMirrorsIPv6 = map[uint64]bool{}
	queuedCreateNATMirrorsIPv6 = map[string]bool{}
	createNATReservationMu.Unlock()
	t.Cleanup(func() {
		createNATReservationMu.Lock()
		defer createNATReservationMu.Unlock()
		createNATReservations, queuedCreateNATReservations = oldReservations, oldQueued
		activeCreateNATOwners, activeCreateNATMirrorsIPv6, queuedCreateNATMirrorsIPv6 = oldOwners, oldMirrors, oldQueuedMirrors
	})
}

func TestManualCreateNATPreservesQuotaAndEgressWithoutReservingPorts(t *testing.T) {
	setupManualPortTest(t)
	manual := false
	for _, virtualization := range []string{"lxc", "kvm"} {
		cfg := ContainerConfig{Name: "manual-" + virtualization, Virtualization: virtualization, PortMappingCount: 1, AutoPortMappings: &manual}
		if err := cfg.NormalizeCreateNATMappings(); err != nil {
			t.Fatal(err)
		}
		if !cfg.WantsNAT() || cfg.PortMappingCount != 1 {
			t.Fatalf("manual allocation changed NAT or quota: %+v", cfg)
		}
		port, release, err := ReserveCreateNATPorts(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if port != 0 || config.AppConfig.NextSSHPort != 30000 {
			t.Fatalf("manual create allocated a management port: port=%d next=%d", port, config.AppConfig.NextSSHPort)
		}
		for _, reserved := range createNATReservations {
			if len(reserved) != 0 {
				t.Fatalf("manual create reserved host ports: %+v", reserved)
			}
		}
		release()
		container := &config.Container{PortMappingLimit: cfg.PortMappingCount}
		mappings, err := SetupCreatePortMappings(container, cfg)
		if err != nil || len(mappings) != 0 {
			t.Fatalf("manual create installed mappings: %+v, %v", mappings, err)
		}
		if !containerAllowsPublicIPv4Egress(container) {
			t.Fatal("manual allocation disabled NAT egress")
		}
	}
}

func TestManualCreateNATRejectsContradictoryInitialMappings(t *testing.T) {
	setupManualPortTest(t)
	manual := false
	for _, cfg := range []ContainerConfig{
		{PortMappingCount: 1, ManagementPort: 30001},
		{PortMappingCount: 2, ExtraPorts: []int{30001}},
		{PortMappingCount: 2, NATPortMappings: []config.PortMapping{{HostPort: 30001, ContainerPort: 80, Protocol: "tcp"}}},
		{PortMappingCount: 0}, {PortMappingCount: -1}, {PortMappingCount: 65},
	} {
		cfg.AutoPortMappings = &manual
		if err := cfg.NormalizeCreateNATMappings(); err == nil {
			t.Fatalf("inconsistent manual create was accepted: %+v", cfg)
		}
	}
}

func TestBatchManualPortPlansRemainEmptyAndKeepAutomaticCreatesCompatible(t *testing.T) {
	setupManualPortTest(t)
	manual := false
	configs := []ContainerConfig{
		{Name: "manual-one", AutoPortMappings: &manual, PortMappingCount: 1},
		{Name: "manual-many", AutoPortMappings: &manual, PortMappingCount: 8},
		{Name: "automatic", PortMappingCount: 2},
	}
	for i := range configs {
		if err := configs[i].NormalizeCreateNATMappings(); err != nil {
			t.Fatal(err)
		}
	}
	planned, err := ReserveBatchCreateNATPorts(configs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if planned[i].ManagementPort != 0 || len(planned[i].NATPortMappings) != 0 || planned[i].PortMappingCount != configs[i].PortMappingCount {
			t.Fatalf("manual batch plan changed: %+v", planned[i])
		}
	}
	if planned[2].ManagementPort == 0 || len(planned[2].NATPortMappings) != 1 {
		t.Fatalf("legacy automatic batch plan was changed: %+v", planned[2])
	}
	for _, cfg := range planned {
		port, release, err := ReserveCreateNATPorts(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if port != cfg.ManagementPort {
			t.Fatalf("claimed management port=%d want=%d", port, cfg.ManagementPort)
		}
		release()
	}
	if len(queuedCreateNATReservations) != 0 || len(createNATReservations) != 0 {
		t.Fatal("create reservations leaked")
	}
}

func TestManualPortConfigJSONPreservesExplicitFalseAndLegacyOmission(t *testing.T) {
	for _, raw := range []string{`{"port_mapping_count":3,"auto_port_mappings":false}`, `{"port_mapping_count":3}`} {
		var cfg ContainerConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var restored ContainerConfig
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg, restored) || cfg.AutomaticallyMapPorts() != restored.AutomaticallyMapPorts() {
			t.Fatalf("allocation mode changed during queue serialization: %s", encoded)
		}
	}
}

func TestManualSSHMappingUpdatesAndClearsEndpointWhileRemainingDeletable(t *testing.T) {
	setupManualPortTest(t)
	config.AppConfig.Containers = []config.Container{{ID: 1, Status: "stopped", PortMappingLimit: 1}}
	m := NewManager()
	if _, err := m.AddPortMapping(1, config.PortMapping{HostPort: 30001, ContainerPort: 22, Protocol: "tcp", Description: "console"}); err != nil {
		t.Fatal(err)
	}
	c := config.FindContainer(1)
	if c.SSHPort != 30001 {
		t.Fatalf("manual SSH mapping did not update endpoint: %+v", c)
	}
	if _, err := m.AddPortMapping(1, config.PortMapping{HostPort: 30002, ContainerPort: 80, Protocol: "tcp"}); err == nil {
		t.Fatal("manual mapping quota was bypassed")
	}
	if _, err := m.UpdatePortMapping(1, 0, config.PortMapping{HostPort: 30002, ContainerPort: 22, Protocol: "tcp", Description: "console"}); err != nil {
		t.Fatal(err)
	}
	if c.SSHPort != 30002 || c.PortMappings[0].Description != "console" {
		t.Fatal("manual SSH edit changed identity or left a stale endpoint")
	}
	if _, err := m.UpdatePortMapping(1, 0, config.PortMapping{HostPort: 30002, ContainerPort: 22, Protocol: "udp", Description: "console"}); err != nil {
		t.Fatal(err)
	}
	if c.SSHPort != 0 {
		t.Fatal("UDP port 22 was incorrectly advertised as SSH")
	}
	if _, err := m.UpdatePortMapping(1, 0, config.PortMapping{HostPort: 30003, ContainerPort: 22, Protocol: "tcp", Description: "console"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeletePortMapping(1, 0); err != nil {
		t.Fatal(err)
	}
	if c.SSHPort != 0 || len(c.PortMappings) != 0 {
		t.Fatalf("removed manual mapping left a stale SSH endpoint: %+v", c)
	}
}

func TestManualKVMRDPMappingSynchronizesEndpoint(t *testing.T) {
	c := &config.Container{Virtualization: "kvm", Template: "kvm-windows-11", PortMappings: []config.PortMapping{{HostPort: 30001, ContainerPort: 3389, Protocol: "tcp", Description: "desktop"}}}
	syncContainerSSHPort(c)
	if c.SSHPort != 30001 {
		t.Fatal("manual RDP mapping was not advertised")
	}
	c.PortMappings = nil
	syncContainerSSHPort(c)
	if c.SSHPort != 0 {
		t.Fatal("missing RDP mapping retained a stale endpoint")
	}
}

func TestManualPortEditsRespectActiveAndQueuedCreateReservations(t *testing.T) {
	setupManualPortTest(t)
	config.AppConfig.Containers = []config.Container{{ID: 1, Name: "customer", Status: "stopped", PortMappingLimit: 2}}
	active := ContainerConfig{Name: "active-create", ManagementPort: 30001, PortMappingCount: 2}
	if err := active.NormalizeCreateNATMappings(); err != nil {
		t.Fatal(err)
	}
	_, release, err := ReserveCreateNATPorts(active)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	m := NewManager()
	mapping := config.PortMapping{HostPort: 30001, ContainerPort: 80, Protocol: "tcp"}
	if _, err := m.AddPortMapping(1, mapping); err == nil {
		t.Fatal("manual add stole an active create's management port")
	}
	udpMapping := mapping
	udpMapping.Protocol = "udp"
	if _, err := m.AddPortMapping(1, udpMapping); err != nil {
		t.Fatalf("TCP reservation unnecessarily blocked UDP: %v", err)
	}
	if _, err := m.DeletePortMapping(1, 0); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := m.AddPortMapping(1, mapping); err != nil {
		t.Fatalf("released active reservation still blocks customer: %v", err)
	}
	queued := ContainerConfig{Name: "queued-create", ManagementPort: 30002, PortMappingCount: 2}
	if err := queued.NormalizeCreateNATMappings(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveBatchCreateNATPorts([]ContainerConfig{queued}); err != nil {
		t.Fatal(err)
	}
	defer ReleaseQueuedCreateNATPorts(queued.Name)
	mapping.HostPort = 30002
	if _, err := m.UpdatePortMapping(1, 0, mapping); err == nil {
		t.Fatal("manual edit stole a queued create's management port")
	}
	if c := config.FindContainer(1); len(c.PortMappings) != 1 || c.PortMappings[0].HostPort != 30001 {
		t.Fatal("rejected edit changed the customer's existing rule")
	}
	ReleaseQueuedCreateNATPorts(queued.Name)
	if _, err := m.UpdatePortMapping(1, 0, mapping); err != nil {
		t.Fatalf("released queued reservation still blocks customer: %v", err)
	}
}
