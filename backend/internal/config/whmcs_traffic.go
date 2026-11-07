package config

import (
	"database/sql"
	"fmt"
	"time"
)

// The receipt and both counters commit in the same SQLite transaction.
// This table is independent of SaveConfig's replace-all container writes.
type WHMCSTrafficReceipt struct {
	Key       string `json:"request_key"`
	UUID      string `json:"uuid"`
	Cycle     string `json:"cycle"`
	ClearedAt int64  `json:"cleared_at"`
}

func whmcsTrafficTable() error {
	if db == nil {
		return fmt.Errorf("traffic receipt database unavailable")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS whmcs_traffic_receipts (uuid TEXT NOT NULL,request_key TEXT NOT NULL,cycle TEXT NOT NULL,cleared_at INTEGER NOT NULL,PRIMARY KEY(uuid,request_key))`)
	return err
}
func WHMCSTrafficReceiptFor(uuid, key string) (*WHMCSTrafficReceipt, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if err := whmcsTrafficTable(); err != nil {
		return nil, err
	}
	r := WHMCSTrafficReceipt{UUID: uuid, Key: key}
	err := db.QueryRow(`SELECT cycle,cleared_at FROM whmcs_traffic_receipts WHERE uuid=? AND request_key=?`, uuid, key).Scan(&r.Cycle, &r.ClearedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Caller holds the LXC traffic sampling lock; memory changes before releasing dbMu.
func WHMCSResetTrafficOnce(c *Container, key, cycle string) (*WHMCSTrafficReceipt, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if err := whmcsTrafficTable(); err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r := WHMCSTrafficReceipt{UUID: c.UUID, Key: key, Cycle: cycle, ClearedAt: time.Now().Unix()}
	var savedCycle string
	var savedAt int64
	err = tx.QueryRow(`SELECT cycle,cleared_at FROM whmcs_traffic_receipts WHERE uuid=? AND request_key=?`, c.UUID, key).Scan(&savedCycle, &savedAt)
	if err == nil {
		if savedCycle != cycle {
			return nil, fmt.Errorf("receipt cycle mismatch")
		}
		r.ClearedAt = savedAt
		return &r, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	result, err := tx.Exec(`UPDATE containers SET traffic_used_rx=0,traffic_used_tx=0,traffic_reset_date=? WHERE uuid=?`, cycle, c.UUID)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return nil, fmt.Errorf("persistent LXC identity missing")
	}
	if _, err = tx.Exec(`INSERT INTO whmcs_traffic_receipts(uuid,request_key,cycle,cleared_at) VALUES(?,?,?,?)`, c.UUID, key, cycle, r.ClearedAt); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(whmcsPolicyTable); err != nil {
		return nil, err
	}
	next := *c
	next.TrafficQuotaRevision++
	if err = saveWHMCSPolicy(tx, next); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	c.TrafficUsedRX = 0
	c.TrafficUsedTX = 0
	c.TrafficResetDate = cycle
	c.TrafficQuotaRevision = next.TrafficQuotaRevision
	return &r, nil
}
