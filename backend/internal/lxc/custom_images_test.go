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
	var native []Template
	for _, template := range GetTemplates() {
		if !template.DownloadsRootfsArchive() {
			native = append(native, template)
		}
	}
	if len(native) != 10 || native[0].ID != "ubuntu-noble" || native[5].ID != "alpine-3.21" {
		t.Fatalf("unexpected native download catalog: %+v", native)
	}
	for _, id := range []string{"alpine-3.24", "debian-trixie"} {
		want := filepath.Join("/var/cache/lxc/download", "proxmox", defaultTemplateArch(), id, "rootfs.tar")
		if got := RootfsArchivePath(id); got != want {
			t.Fatalf("legacy cache path = %q, want %q", got, want)
		}
	}
}

func TestProxmoxAlpinePresetsUseFixedArchives(t *testing.T) {
	templates := proxmoxAlpineTemplates("amd64")
	want := map[string]string{
		"alpine-3.24": "alpine-3.24-default_20261105_amd64.tar.xz",
		"alpine-3.23": "alpine-3.23-default_20261105_amd64.tar.xz",
		"alpine-3.22": "alpine-3.22-default_20250617_amd64.tar.xz",
	}
	for _, template := range templates {
		file, ok := want[template.ID]
		if !ok || template.Custom || template.Retired || template.Arch != "amd64" ||
			!template.DownloadsRootfsArchive() || template.URL != proxmoxAlpineURL+file ||
			template.ArchiveSizeBytes <= 0 {
			t.Fatalf("unexpected Proxmox preset: %+v", template)
		}
		checksum, err := hex.DecodeString(template.SHA256)
		if err != nil || len(checksum) != sha256.Size {
			t.Fatalf("invalid checksum for %s", template.ID)
		}
		if template.rootfsArchiveForInstall() != template.rootfsArchivePath() {
			t.Fatalf("%s must use its pinned archive for create and reinstall", template.ID)
		}
		delete(want, template.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing presets: %v", want)
	}

	legacy := proxmoxAlpineTemplates("arm64")
	if len(legacy) != 1 || legacy[0].ID != "alpine-3.24" || !legacy[0].Retired ||
		legacy[0].DownloadsRootfsArchive() || legacy[0].Arch != "arm64" {
		t.Fatalf("unexpected arm64 compatibility catalog: %+v", legacy)
	}
}

func TestPinnedRootfsCacheRejectsTruncatedArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "rootfs.tar")
	if err := os.WriteFile(archive, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	template := Template{ArchiveSizeBytes: 12}
	if ready, _ := template.cachedRootfsArchiveInfo(archive); ready {
		t.Fatal("truncated archive was reported as downloaded")
	}
	if err := os.WriteFile(archive, []byte("complete tar"), 0600); err != nil {
		t.Fatal(err)
	}
	if ready, size := template.cachedRootfsArchiveInfo(archive); !ready || size != 12 {
		t.Fatalf("pinned size was not recognized: ready=%t, size=%d", ready, size)
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
