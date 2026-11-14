package lxc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSystemHostnameBootConfiguration(t *testing.T) {
	lxcPath := t.TempDir()
	etc := filepath.Join(lxcPath, "ct-77", "rootfs", "etc")
	if err := os.MkdirAll(etc, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(lxcPath, "ct-77", "config")
	original := "# template\nlxc.uts.name=old\nlxc.include = /etc/lxc/default.conf\nlxc.uts.name = ct-77\n\n"
	if err := os.WriteFile(configPath, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	hostnamePath := filepath.Join(etc, "hostname")
	if err := os.WriteFile(hostnamePath, []byte("ct-77\n"), 0644); err != nil {
		t.Fatal(err)
	}

	manager := Manager{LxcPath: lxcPath}
	const wanted = "AppNode42"
	expectFile := func(path, want string) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, want %q (err %v)", filepath.Base(path), got, want, err)
		}
	}
	for range 2 {
		if err := manager.writeSystemHostname("ct-77", wanted); err != nil {
			t.Fatal(err)
		}
		expectFile(hostnamePath, wanted+"\n")
		expectFile(configPath, "# template\nlxc.include = /etc/lxc/default.conf\nlxc.uts.name = AppNode42\n")
	}
	// Reinstall replaces the rootfs with the new image's hostname file.
	if err := os.Remove(hostnamePath); err != nil {
		t.Fatal(err)
	}
	if err := manager.writeSystemHostname("ct-77", wanted); err != nil {
		t.Fatal(err)
	}
	expectFile(hostnamePath, wanted+"\n")
	for _, invalid := range []string{"", "hello\nworld", strings.Repeat("a", 63) + ".example"} {
		if err := manager.writeSystemHostname("ct-77", invalid); err == nil {
			t.Fatalf("accepted invalid system hostname %q", invalid)
		}
		expectFile(hostnamePath, wanted+"\n")
	}
}

func TestSystemHostnameWritesTrackOnlyTheGuestFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux links and file ownership")
	}
	lxcPath := t.TempDir()
	etc := filepath.Join(lxcPath, "ct-77", "rootfs", "etc")
	if err := os.MkdirAll(etc, 0755); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(lxcPath, "ct-77", "config")
	if err := os.WriteFile(conf, []byte("lxc.uts.name = ct-77\n"), 0640); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "hostname")
	if err := os.WriteFile(outside, []byte("outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hostname := filepath.Join(etc, "hostname")
	if err := os.Symlink(outside, hostname); err != nil {
		t.Fatal(err)
	}
	m := Manager{LxcPath: lxcPath}
	if err := m.writeSystemHostname("ct-77", "Guest77"); err != nil {
		t.Fatal(err)
	}
	if value, err := os.ReadFile(outside); err != nil || string(value) != "outside\n" {
		t.Fatalf("outside value changed: %q %v", value, err)
	}
	if value, err := os.ReadFile(hostname); err != nil || string(value) != "Guest77\n" {
		t.Fatalf("guest file was not replaced: %q %v", value, err)
	}
}
