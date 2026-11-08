package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFixedPoolsAndQueuedPlansPersistInSameSQLiteSnapshot(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	fixture := ClicdConfig{AdminUser: "fixture", AdminPassHash: "hash", JWTSecret: "fixture", DataDir: dir, NATPortStart: 30000, NATPortEnd: 30010, NextSSHPort: 30000,
		Containers: []Container{{ID: 1, UUID: "fixed-one", Name: "fixed-one", FixedPortAllocation: true, PortMappingLimit: 2, NATPortAllocations: []int{30000, 30001}, IPv6NATPortAllocations: []int{30002}, DataDisks: []DataDisk{}}},
		Tasks:      []SavedTask{{ID: "fixed-task", Type: "create", ContainerName: "pending", Status: "pending", Config: `{"name":"pending","fixed_port_allocation":true,"auto_port_mappings":false,"port_mapping_count":2,"nat_port_allocations":[30003,30004],"ipv6_nat_port_allocations":[30005]}`}}}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for reload := 0; reload < 2; reload++ {
		SetConfigPath(path)
		cfg, err := InitConfig()
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Containers) != 1 || !cfg.Containers[0].FixedPortAllocation || !reflect.DeepEqual(cfg.Containers[0].NATPortAllocations, []int{30000, 30001}) || !reflect.DeepEqual(cfg.Containers[0].IPv6NATPortAllocations, []int{30002}) {
			t.Fatalf("container pool lost on reload %d: %+v", reload, cfg.Containers)
		}
		plan := parseSavedTaskConfig(cfg.Tasks[0].Config)
		if !plan.FixedPortAllocation || plan.AutoPortMappings == nil || *plan.AutoPortMappings || !reflect.DeepEqual(plan.NATPortAllocations, []int{30003, 30004}) || !reflect.DeepEqual(plan.IPv6NATPortAllocations, []int{30005}) {
			t.Fatalf("pending fixed plan lost: %s", cfg.Tasks[0].Config)
		}
		if port, err := PreviewSSHPortExcluding(nil); err != nil || port != 30006 {
			t.Fatalf("restored fixed ports available to allocator: %d %v", port, err)
		}
		if err := SaveConfig(); err != nil {
			t.Fatal(err)
		}
		resetConfigStoreForTest(t)
	}
}

func TestFixedContainerSaveFailureDoesNotPublishAnUncommittedPortOwner(t *testing.T) {
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	previous := AppConfig
	AppConfig = &ClicdConfig{}
	t.Cleanup(func() { AppConfig = previous })
	err := AddContainerPersisted(Container{ID: 1, Name: "failed-create", FixedPortAllocation: true, NATPortAllocations: []int{30000}})
	if err == nil || len(AppConfig.Containers) != 0 || len(FixedNATReservedPorts("")) != 0 {
		t.Fatalf("uncommitted create retained its fixed pool: %v %+v", err, AppConfig.Containers)
	}
}
