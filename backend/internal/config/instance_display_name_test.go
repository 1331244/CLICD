package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestInstanceDisplayNamePersistenceMigrationAndRollback(t *testing.T) {
	path := setupHistoryStore(t)
	original := Container{ID: 77, UUID: "display-name-owned", Name: "whmcs-77-original-intent", LXCName: "ct-77", Virtualization: "lxc", Status: "running", PortMappings: []PortMapping{{HostPort: 22077, ContainerPort: 8080, Protocol: "tcp"}}, BillingRestrictions: map[string]bool{"other-owner": true}}
	AppConfig.Containers = []Container{original}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	// Emulate an existing installation created before this additive column.
	if _, err := db.Exec(`ALTER TABLE containers DROP COLUMN display_name`); err != nil {
		t.Fatal(err)
	}
	if err := ensureSchemaMigrations(); err != nil {
		t.Fatal(err)
	}
	if err := ensureSchemaMigrations(); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := loadConfigFromDB()
	if err != nil || !ok || len(loaded.Containers) != 1 || loaded.Containers[0].DisplayName != "" {
		t.Fatalf("legacy record did not migrate with an empty label: %v %v %+v", ok, err, loaded)
	}
	AppConfig = loaded
	before := loaded.Containers[0]
	if changed, err := SetContainerDisplayName(original.UUID, "vm77.example.invalid"); err != nil || !changed {
		t.Fatalf("save display name: %v %v", changed, err)
	}
	current := *FindContainerByUUID(original.UUID)
	current.DisplayName = ""
	if !reflect.DeepEqual(current, before) {
		t.Fatal("display name changed unrelated identity or rules")
	}
	if current.Name != original.Name || current.UUID != original.UUID || current.LXCName != original.LXCName || !reflect.DeepEqual(current.PortMappings, original.PortMappings) || !reflect.DeepEqual(current.BillingRestrictions, original.BillingRestrictions) {
		t.Fatal("display name changed instance identity or configuration")
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_display_name_save BEFORE INSERT ON containers BEGIN SELECT RAISE(ABORT,'forced display-name failure'); END`); err != nil {
		t.Fatal(err)
	}
	if changed, err := SetContainerDisplayName(original.UUID, "vm77.example.invalid"); err != nil || changed {
		t.Fatalf("repeating the same label performed a write: %v %v", changed, err)
	}
	if changed, err := SetContainerDisplayName(original.UUID, "changed.example.invalid"); err == nil || changed {
		t.Fatal("forced persistence failure accepted")
	}
	if FindContainerByUUID(original.UUID).DisplayName != "vm77.example.invalid" {
		t.Fatal("failed save left changed in-memory label")
	}
	loaded, ok, err = loadConfigFromDB()
	if err != nil || !ok || loaded.Containers[0].DisplayName != "vm77.example.invalid" {
		t.Fatalf("failed save changed SQLite: %v %v", ok, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_display_name_save`); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	if FindContainerByUUID(original.UUID).DisplayName != "vm77.example.invalid" {
		t.Fatal("reopening SQLite lost the display name")
	}
	if changed, err := SetContainerDisplayName(original.UUID, ""); err != nil || !changed {
		t.Fatalf("clear display name: %v %v", changed, err)
	}
	loaded, ok, err = loadConfigFromDB()
	if err != nil || !ok || loaded.Containers[0].DisplayName != "" || loaded.Containers[0].Name != original.Name {
		t.Fatal("clearing display name lost the stable internal name", err)
	}
	if _, err := SetContainerDisplayName("unrelated-uuid", "vm77"); err == nil {
		t.Fatal("updating a missing UUID succeeded")
	}
}

func TestInstanceDisplayNameHostnameValidation(t *testing.T) {
	maxHost := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, name := range []string{"", "server", "9", "VM77.example.invalid", "vm-77", maxHost} {
		if !ValidContainerDisplayName(name) {
			t.Errorf("valid hostname refused: %q", name)
		}
	}
	for _, name := range []string{" a", "a ", "a\tb", "a\x00b", "a_b", "a..b", "a.", ".a", "-a", "a-", "a.-b", "a.b-", "https://a", "a:8999", "中文", strings.Repeat("a", 64), maxHost + "e"} {
		if ValidContainerDisplayName(name) {
			t.Errorf("invalid hostname accepted: %q", name)
		}
	}
}
