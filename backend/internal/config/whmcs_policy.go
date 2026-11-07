package config

import (
	"database/sql"
	"encoding/json"
)

type whmcsPolicy struct {
	CPUPercent           int             `json:"cpu_percent"`
	SwapMB               int             `json:"swap_mb"`
	SwapConfigured       bool            `json:"swap_configured"`
	BillingRestrictions  map[string]bool `json:"billing_restrictions"`
	TrafficQuotaRevision int64           `json:"traffic_quota_revision"`
}

const whmcsPolicyTable = `CREATE TABLE IF NOT EXISTS whmcs_container_policy(uuid TEXT PRIMARY KEY,policy TEXT NOT NULL)`

func saveWHMCSPolicy(tx *sql.Tx, c Container) error {
	p := whmcsPolicy{c.CPUPercent, c.SwapMB, c.SwapConfigured, c.BillingRestrictions, c.TrafficQuotaRevision}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO whmcs_container_policy(uuid,policy) VALUES(?,?) ON CONFLICT(uuid) DO UPDATE SET policy=excluded.policy`, c.UUID, string(data))
	return err
}
func saveWHMCSPolicies(tx *sql.Tx) error {
	if _, err := tx.Exec(whmcsPolicyTable); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM whmcs_container_policy`); err != nil {
		return err
	}
	for _, c := range AppConfig.Containers {
		if err := saveWHMCSPolicy(tx, c); err != nil {
			return err
		}
	}
	return nil
}
func loadWHMCSPolicies(containers []Container) error {
	if _, err := db.Exec(whmcsPolicyTable); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT uuid,policy FROM whmcs_container_policy`)
	if err != nil {
		return err
	}
	defer rows.Close()
	policies := map[string]whmcsPolicy{}
	for rows.Next() {
		var uuid, data string
		if err = rows.Scan(&uuid, &data); err != nil {
			return err
		}
		var p whmcsPolicy
		if err = json.Unmarshal([]byte(data), &p); err != nil {
			return err
		}
		policies[uuid] = p
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range containers {
		c := &containers[i]
		if p, ok := policies[c.UUID]; ok {
			c.CPUPercent = p.CPUPercent
			c.SwapMB = p.SwapMB
			c.SwapConfigured = p.SwapConfigured
			c.BillingRestrictions = p.BillingRestrictions
			c.TrafficQuotaRevision = p.TrafficQuotaRevision
		}
	}
	return nil
}
