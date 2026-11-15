package lxc

import (
	"crypto/sha512"
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

func TestProxmoxImagesUsePinnedHostArchitectureArchives(t *testing.T) {
	arch := defaultTemplateArch()
	var count int
	for _, template := range GetTemplates() {
		if template.Proxmox == nil {
			continue
		}
		count++
		image := template.Proxmox
		digest, err := hex.DecodeString(image.SHA512)
		if template.Custom || template.Arch != arch || !strings.Contains(image.Filename, "_"+arch+".tar.") ||
			image.SizeBytes <= 0 || err != nil || len(digest) != sha512.Size ||
			template.URL != proxmoxImageURL+image.Filename {
			t.Errorf("%s has invalid Proxmox archive metadata", template.ID)
		}
		want := filepath.Join("/var/cache/lxc/download", "proxmox", arch, template.ID, "rootfs.tar")
		if template.rootfsArchivePath() != want {
			t.Errorf("%s has unexpected cache path: %s", template.ID, template.rootfsArchivePath())
		}
	}
	alpine := FindTemplate("alpine-3.24")
	if count != len(proxmoxImages[arch]) || count == 0 || alpine == nil || alpine.Proxmox == nil {
		t.Fatalf("unexpected Proxmox catalog for %s: %d images", arch, count)
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
	sum := sha512.Sum512(data)
	if err := verifyRootfsChecksum(archive, sha512.New(), "SHA-512", hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := verifyRootfsChecksum(archive, sha512.New(), "SHA-512", strings.Repeat("0", 128)); err == nil {
		t.Fatal("wrong SHA-512 passed verification")
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
