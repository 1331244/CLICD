package lxc

import (
	"strings"
	"testing"
)

// These passwords are local fixtures. No system accounts or container commands
// are used: this locks the exact value passed to chpasswd's standard input.
func TestSSHPasswordColonPreserved(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{"middle", "Alpha9:Beta"},
		{"leading", ":Alpha9Beta"},
		{"trailing", "Alpha9Beta:"},
		{"repeated", "Alpha9::Beta:::"},
		{"shell_punctuation", "A9:'\"\\$&<>+./=@#%^*()-_[]"},
		{"minimum_length", "Ab1:cdef"},
		{"maximum_length", "A1:" + strings.Repeat("x", 61)},
		{"mostly_colons", "A1::::::"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCustomSSHPassword(tc.password); err != nil {
				t.Fatalf("custom password unexpectedly rejected: %v", err)
			}
			if err := validateRootPassword(tc.password); err != nil {
				t.Fatalf("root password unexpectedly rejected: %v", err)
			}
			for _, mode := range []string{SSHAuthPassword, ""} {
				access, err := ResolveCreateSSHAccess(ContainerConfig{SSHAuthMode: mode, SSHPassword: tc.password})
				if err != nil || access.Mode != SSHAuthPassword || access.Password != tc.password {
					t.Fatal("creation did not preserve the complete colon password")
				}
				access, err = ResolveReinstallSSHAccess("Previous9!", ContainerConfig{SSHAuthMode: mode, SSHPassword: tc.password})
				if err != nil || access.Mode != SSHAuthPassword || access.Password != tc.password {
					t.Fatal("reinstallation did not preserve the complete custom colon password")
				}
			}
			access, err := ResolveReinstallSSHAccess(tc.password, ContainerConfig{SSHAuthMode: SSHAuthKeep})
			if err != nil || access.Mode != SSHAuthKeep || access.Password != tc.password {
				t.Fatal("reinstallation did not retain the complete current colon password")
			}
			input := rootPasswordInput(tc.password)
			if input != "root:"+tc.password+"\n" {
				t.Fatal("chpasswd input changed the password bytes")
			}
			username, remainder, found := strings.Cut(input, ":")
			if !found || username != "root" || remainder != tc.password+"\n" {
				t.Fatal("the first delimiter did not retain the full password in chpasswd input")
			}
		})
	}
	for _, tc := range []struct {
		name     string
		password string
	}{
		{"line_feed", "Alpha9:\nBeta"},
		{"carriage_return", "Alpha9:\rBeta"},
		{"nul", "Alpha9:\x00Beta"},
	} {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			if validateRootPassword(tc.password) == nil || ValidateCustomSSHPassword(tc.password) == nil {
				t.Fatal("unsafe control characters were accepted")
			}
			if _, err := ResolveCreateSSHAccess(ContainerConfig{SSHAuthMode: SSHAuthPassword, SSHPassword: tc.password}); err == nil {
				t.Fatal("creation accepted an unsafe control character")
			}
			if _, err := ResolveReinstallSSHAccess("Previous9!", ContainerConfig{SSHAuthMode: SSHAuthPassword, SSHPassword: tc.password}); err == nil {
				t.Fatal("reinstallation accepted an unsafe control character")
			}
		})
	}
}
