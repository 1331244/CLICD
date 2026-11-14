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

// ValidSystemHostname additionally checks the Linux UTS name length.
func ValidSystemHostname(name string) bool {
	return name != "" && len(name) <= 64 && ValidContainerDisplayName(name)
}

// SetContainerDisplayName persists only the independent panel label. The old
// in-memory value is restored if SQLite cannot commit the configuration.
func SetContainerDisplayName(uuid, name string) (bool, error) {
	return setContainerHostnames(uuid, name, nil)
}

func SetContainerHostnames(uuid, name, systemHostname string) (bool, error) {
	if !ValidSystemHostname(systemHostname) {
		return false, fmt.Errorf("invalid system hostname")
	}
	return setContainerHostnames(uuid, name, &systemHostname)
}

func setContainerHostnames(uuid, name string, systemHostname *string) (bool, error) {
	if !ValidContainerDisplayName(name) {
		return false, fmt.Errorf("invalid instance display hostname")
	}
	allocationMu.Lock()
	defer allocationMu.Unlock()
	c := FindContainerByUUID(uuid)
	if c == nil {
		return false, fmt.Errorf("instance not found")
	}
	if c.DisplayName == name && (systemHostname == nil || c.SystemHostname == *systemHostname) {
		return false, nil
	}
	previousName, previousSystemHostname := c.DisplayName, c.SystemHostname
	c.DisplayName = name
	if systemHostname != nil {
		c.SystemHostname = *systemHostname
	}
	if err := SaveConfig(); err != nil {
		c.DisplayName, c.SystemHostname = previousName, previousSystemHostname
		return false, err
	}
	return true, nil
}
