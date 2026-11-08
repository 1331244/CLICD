package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
)

func insertFixedPortAllocation(tx *sql.Tx, kind, id string, ports, ipv6Ports []int) error {
	v4, err := json.Marshal(ports)
	if err != nil {
		return err
	}
	v6, err := json.Marshal(ipv6Ports)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO fixed_port_allocations(owner_kind,owner_id,nat_ports,ipv6_nat_ports) VALUES (?,?,?,?)`, kind, id, string(v4), string(v6))
	return err
}

func saveFixedPortAllocations(tx *sql.Tx) error {
	for _, c := range AppConfig.Containers {
		if c.FixedPortAllocation {
			if err := insertFixedPortAllocation(tx, "container", strconv.Itoa(c.ID), c.NATPortAllocations, c.IPv6NATPortAllocations); err != nil {
				return err
			}
		}
	}
	return saveFixedTaskPortAllocations(tx)
}

func saveFixedTaskPortAllocations(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM fixed_port_allocations WHERE owner_kind='task'`); err != nil {
		return err
	}
	for _, task := range AppConfig.Tasks {
		cfg := parseSavedTaskConfig(task.Config)
		if cfg.FixedPortAllocation {
			if err := insertFixedPortAllocation(tx, "task", task.ID, cfg.NATPortAllocations, cfg.IPv6NATPortAllocations); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadFixedPortAllocation(kind, id string) (bool, []int, []int, error) {
	var v4, v6 string
	err := db.QueryRow(`SELECT nat_ports,ipv6_nat_ports FROM fixed_port_allocations WHERE owner_kind=? AND owner_id=?`, kind, id).Scan(&v4, &v6)
	if err == sql.ErrNoRows {
		return false, nil, nil, nil
	}
	if err != nil {
		return false, nil, nil, err
	}
	var ports, ipv6Ports []int
	if err = json.Unmarshal([]byte(v4), &ports); err != nil {
		return false, nil, nil, fmt.Errorf("invalid fixed NAT allocation: %w", err)
	}
	if err = json.Unmarshal([]byte(v6), &ipv6Ports); err != nil {
		return false, nil, nil, fmt.Errorf("invalid fixed IPv6 allocation: %w", err)
	}
	return true, ports, ipv6Ports, nil
}
