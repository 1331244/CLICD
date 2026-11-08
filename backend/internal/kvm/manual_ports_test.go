package kvm

import (
	"reflect"
	"testing"

	"clicd/internal/config"
)

func TestReinstallKeepsCustomerManagedPortsAndDoesNotAllocateMissingManagementRule(t *testing.T) {
	previous := config.AppConfig
	config.AppConfig = &config.ClicdConfig{NATPortStart: 30000, NATPortEnd: 30010, NextSSHPort: 30000}
	t.Cleanup(func() { config.AppConfig = previous })
	for _, mappings := range [][]config.PortMapping{
		nil,
		{{HostPort: 30001, ContainerPort: 80, Protocol: "tcp", Description: "web"}},
		{{HostPort: 30001, ContainerPort: 22, Protocol: "tcp", Description: "console"}},
	} {
		c := &config.Container{Virtualization: "kvm", Template: "kvm-windows-11", PortMappingLimit: 4,
			PortMappings: append([]config.PortMapping(nil), mappings...), SSHPort: 30009}
		normalizeKVMManagementPortMapping(c)
		if !reflect.DeepEqual(c.PortMappings, mappings) || c.SSHPort != 0 || config.AppConfig.NextSSHPort != 30000 {
			t.Fatalf("reinstall changed customer rules or allocated a port: %+v", c)
		}
	}
}

func TestReinstallAdaptsLegacyAutomaticManagementRuleWithoutLosingHostIP(t *testing.T) {
	c := &config.Container{Virtualization: "kvm", Template: "kvm-windows-11", PortMappings: []config.PortMapping{
		{HostIP: "198.51.100.7", HostPort: 30001, ContainerPort: 22, Protocol: "tcp", Description: "SSH"},
		{HostPort: 30002, ContainerPort: 80, Protocol: "tcp", Description: "web"},
	}}
	normalizeKVMManagementPortMapping(c)
	if len(c.PortMappings) != 2 || c.PortMappings[0].ContainerPort != 3389 || c.PortMappings[0].Description != "RDP" || c.PortMappings[0].HostIP != "198.51.100.7" || c.SSHPort != 30001 {
		t.Fatalf("legacy management rule was not adapted in place: %+v", c)
	}
	c.Template = "kvm-debian-12"
	normalizeKVMManagementPortMapping(c)
	if c.PortMappings[0].ContainerPort != 22 || c.PortMappings[0].Description != "SSH" || c.SSHPort != 30001 {
		t.Fatalf("legacy RDP rule was not adapted back to SSH: %+v", c)
	}
}
