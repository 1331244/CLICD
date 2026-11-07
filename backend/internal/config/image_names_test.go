package config

import (
	"reflect"
	"testing"
)

func TestImageNamesPersistAndRollbackTogether(t *testing.T) {
	path := setupHistoryStore(t)
	AppConfig.CustomLXCImages = []CustomLXCImage{{ID: "custom-lxc-named", Name: "Original", Arch: "amd64", URL: "https://example.invalid/rootfs.tar.xz"}}
	if err := SetImageNameAndFilename("custom-lxc-named", "Alpine 3.24", "Alpine 3.24.tar.xz"); err != nil {
		t.Fatal(err)
	}
	if err := SetImageNameAndFilename("alpine-3.21", "Alpine stable", "Alpine stable.tar.xz"); err != nil {
		t.Fatal(err)
	}
	names, files := ImageNameOverrides(), ImageCacheFilenames()
	names["custom-lxc-named"] = "not published"
	files["custom-lxc-named"] = "not published.tar"
	if AppConfig.NameOverrides["custom-lxc-named"] != "Alpine 3.24" || AppConfig.ImageFiles["custom-lxc-named"] != "Alpine 3.24.tar.xz" {
		t.Fatal("getter allowed mutation of image metadata")
	}
	beforeNames, beforeFiles := ImageNameOverrides(), ImageCacheFilenames()
	if _, err := db.Exec(`CREATE TRIGGER reject_image_name BEFORE INSERT ON app_meta WHEN NEW.key = 'image_cache_filenames' BEGIN SELECT RAISE(ABORT,'forced image-name failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SetImageNameAndFilename("custom-lxc-named", "Changed", "Changed.tar.xz"); err == nil {
		t.Fatal("forced metadata failure accepted")
	}
	if !reflect.DeepEqual(ImageNameOverrides(), beforeNames) || !reflect.DeepEqual(ImageCacheFilenames(), beforeFiles) {
		t.Fatal("failed save changed in-memory name or filename")
	}
	loaded, ok, err := loadConfigFromDB()
	if err != nil || !ok || !reflect.DeepEqual(loaded.NameOverrides, beforeNames) || !reflect.DeepEqual(loaded.ImageFiles, beforeFiles) {
		t.Fatalf("failed rename escaped SQLite transaction: %+v %v", loaded, err)
	}
	if removed, err := RemoveCustomLXCImage("custom-lxc-named"); err == nil || removed {
		t.Fatal("source removal accepted forced metadata failure")
	}
	if len(AppConfig.CustomLXCImages) != 1 || !reflect.DeepEqual(ImageNameOverrides(), beforeNames) || !reflect.DeepEqual(ImageCacheFilenames(), beforeFiles) {
		t.Fatal("failed source removal lost source/name/file metadata")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_image_name`); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ImageNameOverrides(), beforeNames) || !reflect.DeepEqual(ImageCacheFilenames(), beforeFiles) {
		t.Fatal("reopening SQLite lost built-in or custom display metadata")
	}
	if removed, err := RemoveCustomLXCImage("custom-lxc-named"); err != nil || !removed {
		t.Fatalf("remove custom image: %v %v", removed, err)
	}
	if _, exists := ImageNameOverrides()["custom-lxc-named"]; exists {
		t.Fatal("custom image removal left a name override")
	}
	if _, exists := ImageCacheFilenames()["custom-lxc-named"]; exists || ImageCacheFilenames()["alpine-3.21"] != "Alpine stable.tar.xz" {
		t.Fatal("custom source removal lost unrelated metadata or retained its filename")
	}
}
