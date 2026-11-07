package lxc

import (
	"clicd/internal/config"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SetWHMCSPublicKey adds a validated key offline. os.Root prevents a malicious
// guest symlink from escaping its rootfs while the node writes as root.
func (m *Manager) SetWHMCSPublicKey(id int, publicKey string) error {
	c := config.FindContainer(id)
	if c == nil || c.Runtime() != "lxc" {
		return fmt.Errorf("LXC instance required")
	}
	status, err := m.GetContainerStatus(c.LxcName())
	if err != nil || status != "stopped" {
		return fmt.Errorf("stop the LXC instance before adding an SSH key")
	}
	key, err := NormalizeSSHPublicKey(publicKey)
	if err != nil || key == "" {
		return fmt.Errorf("valid OpenSSH public key required")
	}
	if err = m.ensureDiskImageMounted(c.LxcName()); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Join(m.LxcPath, c.LxcName(), "rootfs"))
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.MkdirAll("root/.ssh", 0700); err != nil {
		return err
	}
	f, err := root.OpenFile("root/.ssh/authorized_keys", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	contents, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(contents) > 1024*1024 {
		return fmt.Errorf("authorized_keys exceeds safe limit")
	}
	if !strings.Contains("\n"+string(contents), "\n"+key+"\n") {
		if _, err = f.Seek(0, io.SeekEnd); err != nil {
			return err
		}
		if _, err = f.WriteString("\n" + key + "\n"); err != nil {
			return err
		}
	}
	uid, gid, err := unprivilegedIDMap()
	if err != nil {
		return err
	}
	if err = f.Chown(int(uid), int(gid)); err != nil {
		return err
	}
	dir, err := root.Open("root/.ssh")
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Chown(int(uid), int(gid)); err != nil {
		return err
	}
	if err = dir.Chmod(0700); err != nil {
		return err
	}
	// CLICD normally disables public-key auth for password-only provisioning.
	cfg, err := root.OpenFile("etc/ssh/sshd_config", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer cfg.Close()
	data, err := io.ReadAll(io.LimitReader(cfg, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return fmt.Errorf("sshd configuration exceeds limit")
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "pubkeyauthentication ") {
			lines[i] = "PubkeyAuthentication yes"
		}
	}
	text := "PubkeyAuthentication yes\n" + strings.Join(lines, "\n")
	if _, err = cfg.Seek(0, 0); err != nil {
		return err
	}
	if err = cfg.Truncate(0); err != nil {
		return err
	}
	_, err = cfg.WriteString(text)
	return err
}
