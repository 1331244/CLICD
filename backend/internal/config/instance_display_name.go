package config

import (
	"fmt"
	"strings"
)

// ValidContainerDisplayName accepts a hostname without changing the stable
// container name used by tasks, access grants and the runtime. Empty clears it.
func ValidContainerDisplayName(name string) bool {
	if name == "" {
		return true
	}
	if len(name) > 253 {
		return false
	}
	alphaNumeric := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || !alphaNumeric(label[0]) || !alphaNumeric(label[len(label)-1]) {
			return false
		}
		for i := range label {
			if !alphaNumeric(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

// SetContainerDisplayName persists only the independent panel label. The old
// in-memory value is restored if SQLite cannot commit the configuration.
func SetContainerDisplayName(uuid, name string) (bool, error) {
	if !ValidContainerDisplayName(name) {
		return false, fmt.Errorf("invalid instance display hostname")
	}
	allocationMu.Lock()
	defer allocationMu.Unlock()
	c := FindContainerByUUID(uuid)
	if c == nil {
		return false, fmt.Errorf("instance not found")
	}
	if c.DisplayName == name {
		return false, nil
	}
	previous := c.DisplayName
	c.DisplayName = name
	if err := SaveConfig(); err != nil {
		c.DisplayName = previous
		return false, err
	}
	return true, nil
}
