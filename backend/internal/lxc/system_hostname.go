package lxc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"clicd/internal/config"
)

const lxcHostnameKey = "lxc.uts.name"

// SetSystemHostname updates the boot settings and, if running, the guest's
// UTS namespace. The caller serializes it with container lifecycle tasks.
func (m *Manager) SetSystemHostname(lxcName, name string) error {
	if err := m.writeSystemHostname(lxcName, name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "lxc-info", "-P", m.LxcPath, "-n", lxcName, "-sH").Output()
	if err != nil {
		return fmt.Errorf("read LXC state before setting hostname: %w", err)
	}
	switch strings.TrimSpace(string(output)) {
	case "STOPPED":
		return nil
	case "RUNNING", "FROZEN":
		return m.setRunningHostname(ctx, lxcName, name)
	default:
		return fmt.Errorf("LXC state changed while setting hostname")
	}
}

func (m *Manager) setRunningHostname(ctx context.Context, lxcName, name string) error {
	output, err := exec.CommandContext(ctx, "lxc-info", "-P", m.LxcPath, "-n", lxcName, "-pH").Output()
	if err != nil {
		return fmt.Errorf("read LXC init process: %w", err)
	}
	pid := strings.TrimSpace(string(output))
	if pid == "" || strings.Trim(pid, "0123456789") != "" || pid == "0" {
		return fmt.Errorf("LXC has no running init process")
	}
	// Pin the guest's UTS namespace and use the host binary. Refuse shared
	// host UTS so a misconfigured container cannot rename its node.
	ns, err := os.Open("/proc/" + pid + "/ns/uts")
	if err != nil {
		return fmt.Errorf("open guest UTS namespace: %w", err)
	}
	defer ns.Close()
	guest, err := ns.Stat()
	if err != nil {
		return err
	}
	host, err := os.Stat("/proc/self/ns/uts")
	if err != nil {
		return err
	}
	if os.SameFile(host, guest) {
		return fmt.Errorf("LXC shares the node UTS namespace")
	}
	hostname, err := exec.LookPath("hostname")
	if err != nil {
		return fmt.Errorf("find node hostname command: %w", err)
	}
	runHostname := func(args ...string) ([]byte, error) {
		args = append([]string{"--uts=/proc/self/fd/3", "--", hostname}, args...)
		cmd := exec.CommandContext(ctx, "nsenter", args...)
		cmd.ExtraFiles = []*os.File{ns}
		return cmd.Output()
	}
	if _, err := runHostname(name); err != nil {
		return fmt.Errorf("set running guest hostname: %w", err)
	}
	actual, err := runHostname()
	if err != nil || strings.TrimSpace(string(actual)) != name {
		return fmt.Errorf("could not verify the running guest hostname")
	}
	return nil
}

func (m *Manager) writeSystemHostname(lxcName, name string) error {
	if !config.ValidSystemHostname(name) {
		return fmt.Errorf("invalid system hostname")
	}
	rootfsPath, err := m.safeRootfsPath(filepath.Join(m.LxcPath, lxcName, "rootfs"))
	if err != nil || filepath.Base(lxcName) != lxcName {
		return fmt.Errorf("invalid LXC path for hostname update")
	}
	container, err := os.OpenRoot(filepath.Dir(rootfsPath))
	if err != nil {
		return fmt.Errorf("open LXC configuration directory: %w", err)
	}
	defer container.Close()
	info, err := container.Lstat("config")
	if err != nil {
		return fmt.Errorf("read LXC configuration metadata: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("LXC config is not a regular file")
	}
	data, err := container.ReadFile("config")
	if err != nil {
		return fmt.Errorf("read LXC configuration: %w", err)
	}
	if err := m.ensureDiskImageMounted(lxcName); err != nil {
		return err
	}
	rootfs, err := container.OpenRoot("rootfs")
	if err != nil {
		return fmt.Errorf("open guest rootfs: %w", err)
	}
	defer rootfs.Close()
	rootInfo, err := rootfs.Stat(".")
	if err != nil {
		return fmt.Errorf("read guest rootfs owner: %w", err)
	}
	etc, err := rootfs.OpenRoot("etc")
	if err != nil {
		return fmt.Errorf("open guest /etc: %w", err)
	}
	defer etc.Close()
	_, uid, gid, _ := fileStatFields(rootInfo)
	if err := replaceHostnameFile(etc, "hostname", name+"\n", 0644, uid, gid); err != nil {
		return fmt.Errorf("write guest /etc/hostname: %w", err)
	}

	bootConfig := lxcBootHostname(string(data), name)
	if bootConfig == string(data) {
		return nil
	}
	_, uid, gid, _ = fileStatFields(info)
	if err := replaceHostnameFile(container, "config", bootConfig, info.Mode().Perm(), uid, gid); err != nil {
		return fmt.Errorf("write LXC boot hostname: %w", err)
	}
	return nil
}

func lxcBootHostname(data, name string) string {
	lines := strings.Split(strings.TrimRight(data, "\n"), "\n")
	kept := lines[:0]
	for _, line := range lines {
		key, _, _ := strings.Cut(line, "=")
		if strings.TrimSpace(key) != lxcHostnameKey {
			kept = append(kept, line)
		}
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	// Keep the configured name after any includes that supply a default.
	return strings.Join(append(kept, lxcHostnameKey+" = "+name, ""), "\n")
}

// A fresh file and an atomic rename avoid following guest-controlled links,
// modifying a hard-linked inode, or leaving a half-written boot config.
func replaceHostnameFile(root *os.Root, path, data string, mode os.FileMode, uid, gid int) error {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	temporary := ".clicd-hostname-" + hex.EncodeToString(random[:])
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	defer file.Close()
	if _, err := file.WriteString(data); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		if err := file.Chown(uid, gid); err != nil {
			return err
		}
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return root.Rename(temporary, path)
}
