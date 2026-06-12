package store

import (
	"database/sql"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// RelayRepo persists relay-mode state in the global store: each project's
// daemon identity (X25519 private key), the phone devices paired with it, and
// their Web Push subscriptions. Global because private keys are machine-level
// secrets that must never live inside a repo's .fabrika/.
type RelayRepo struct{ db *sql.DB }

// Identity returns the project's X25519 private key, or nil if none exists yet.
func (r *RelayRepo) Identity(projectRoot string) ([]byte, error) {
	var key []byte
	err := r.db.QueryRow(
		`SELECT private_key FROM relay_identities WHERE project_root=?`, projectRoot,
	).Scan(&key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return key, err
}

// SaveIdentity stores the project's private key (first relay enable).
func (r *RelayRepo) SaveIdentity(projectRoot string, privateKey []byte) error {
	_, err := r.db.Exec(
		`INSERT INTO relay_identities (project_root, private_key) VALUES (?, ?)
		 ON CONFLICT(project_root) DO UPDATE SET private_key=excluded.private_key`,
		projectRoot, privateKey,
	)
	return err
}

// Devices lists the phones paired with a daemon, oldest first.
func (r *RelayRepo) Devices(daemonID string) ([]model.RelayDevice, error) {
	rows, err := r.db.Query(
		`SELECT id, daemon_id, pubkey, name, created_at, last_seen
		 FROM relay_devices WHERE daemon_id=? ORDER BY created_at`, daemonID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RelayDevice
	for rows.Next() {
		var d model.RelayDevice
		if err := rows.Scan(&d.ID, &d.DaemonID, &d.PubKey, &d.Name, &d.CreatedAt, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddDevice pairs a phone with a daemon, assigning an ID if absent. Re-pairing
// the same pubkey is an upsert (refreshes name and last_seen, keeps the ID).
func (r *RelayRepo) AddDevice(d *model.RelayDevice) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	return r.db.QueryRow(
		`INSERT INTO relay_devices (id, daemon_id, pubkey, name) VALUES (?, ?, ?, ?)
		 ON CONFLICT(daemon_id, pubkey) DO UPDATE SET name=excluded.name, last_seen=datetime('now')
		 RETURNING id`,
		d.ID, d.DaemonID, d.PubKey, d.Name,
	).Scan(&d.ID)
}

// TouchDevice bumps last_seen (called when the phone connects).
func (r *RelayRepo) TouchDevice(id string) error {
	_, err := r.db.Exec(`UPDATE relay_devices SET last_seen=datetime('now') WHERE id=?`, id)
	return err
}

// DeleteDevice unpairs a phone and removes its push subscriptions.
func (r *RelayRepo) DeleteDevice(id string) error {
	if _, err := r.db.Exec(`DELETE FROM relay_push_subs WHERE device_id=?`, id); err != nil {
		return err
	}
	res, err := r.db.Exec(`DELETE FROM relay_devices WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// PushSubs lists the Web Push subscriptions registered for a daemon.
func (r *RelayRepo) PushSubs(daemonID string) ([]model.RelayPushSub, error) {
	rows, err := r.db.Query(
		`SELECT endpoint, daemon_id, device_id, p256dh, auth, created_at
		 FROM relay_push_subs WHERE daemon_id=? ORDER BY created_at`, daemonID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RelayPushSub
	for rows.Next() {
		var s model.RelayPushSub
		if err := rows.Scan(&s.Endpoint, &s.DaemonID, &s.DeviceID, &s.P256DH, &s.Auth, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AddPushSub registers (or refreshes) a Web Push subscription.
func (r *RelayRepo) AddPushSub(s *model.RelayPushSub) error {
	_, err := r.db.Exec(
		`INSERT INTO relay_push_subs (endpoint, daemon_id, device_id, p256dh, auth) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET daemon_id=excluded.daemon_id,
		     device_id=excluded.device_id, p256dh=excluded.p256dh, auth=excluded.auth`,
		s.Endpoint, s.DaemonID, s.DeviceID, s.P256DH, s.Auth,
	)
	return err
}

// DeletePushSub removes a subscription (unsubscribe, or push service said gone).
func (r *RelayRepo) DeletePushSub(endpoint string) error {
	_, err := r.db.Exec(`DELETE FROM relay_push_subs WHERE endpoint=?`, endpoint)
	return err
}
