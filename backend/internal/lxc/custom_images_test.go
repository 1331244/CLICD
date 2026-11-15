package lxc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"clicd/internal/config"
)

func TestGetTemplatesIncludesHostArchitectureCustomLXCImage(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.ClicdConfig{CustomLXCImages: []config.CustomLXCImage{
		{
			ID: "custom-lxc-host", Name: "Host Rootfs", Distro: "alpine",
			Release: "3.21", Arch: runtime.GOARCH, URL: "https://example.test/rootfs.tar.xz",
		},
		{
			ID: "custom-lxc-other", Name: "Other Rootfs", Distro: "alpine",
			Release: "3.21", Arch: "not-" + runtime.GOARCH, URL: "https://example.test/other.tar.xz",
		},
	}}

	template := FindTemplate("custom-lxc-host")
	if template == nil || !template.Custom || template.URL == "" {
		t.Fatalf("custom LXC template was not exposed correctly: %+v", template)
	}
	if FindTemplate("custom-lxc-other") != nil {
		t.Fatal("custom LXC template for another architecture was exposed")
	}
}

func TestRootfsArchivePathUsesAllowlistedID(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.ClicdConfig{}

	for _, id := range []string{"", ".", "..", "../../etc/passwd", "/absolute", "unknown"} {
		if got := RootfsArchivePath(id); got != "" {
			t.Fatalf("RootfsArchivePath(%q) = %q", id, got)
		}
	}
}

func TestNativePresetsKeepExistingMirrorCachePaths(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.ClicdConfig{}
	available := GetTemplates()
	if len(available) != 10 || available[0].ID != "ubuntu-noble" || available[5].ID != "alpine-3.21" {
		t.Fatalf("unexpected built-in download catalog: %+v", available)
	}
	for _, template := range available {
		if template.Retired || template.URL != "" {
			t.Fatalf("template %s is not a native download", template.ID)
		}
	}
	old := FindTemplate("alpine-3.24")
	if old == nil || !old.Retired || old.URL != "" {
		t.Fatalf("expected retired cache-only metadata: %+v", old)
	}
	for _, id := range []string{old.ID, "debian-trixie"} {
		want := filepath.Join("/var/cache/lxc/download", "proxmox", defaultTemplateArch(), id, "rootfs.tar")
		if got := RootfsArchivePath(id); got != want {
			t.Fatalf("legacy cache path = %q, want %q", got, want)
		}
	}
}

func TestRootfsArchiveAcceptsPreviouslyRenamedCache(t *testing.T) {
	directory := t.TempDir()
	data := []byte("existing image archive")
	const oldName = "Alpine Legacy.tar.xz"
	if err := os.WriteFile(filepath.Join(directory, oldName), data, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(directory, "rootfs.tar")
	if err := os.Symlink(oldName, archive); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if ready, size := rootfsArchiveInfo(archive); !ready || size != int64(len(data)) {
		t.Fatal("previously renamed archive is unavailable through its cache path")
	}
	sum := sha256.Sum256(data)
	if err := verifyRootfsSHA256(archive, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := verifyRootfsSHA256(archive, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong SHA-256 passed verification")
	}
}

func TestValidateCustomRootfsEntries(t *testing.T) {
	if err := validateCustomRootfsEntries([]string{"./etc/", "./bin/", "./bin/sh"}); err != nil {
		t.Fatalf("valid rootfs entries failed: %v", err)
	}
	for _, entries := range [][]string{
		{},
		{"etc/passwd"},
		{"/etc/passwd", "bin/sh"},
		{"../../etc/passwd", "bin/sh"},
	} {
		if err := validateCustomRootfsEntries(entries); err == nil {
			t.Fatalf("unsafe rootfs entries unexpectedly passed: %#v", entries)
		}
	}
}
