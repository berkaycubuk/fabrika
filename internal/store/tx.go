package store

import "database/sql"

// WithTx runs fn inside a single transaction on db. It begins a transaction,
// invokes fn(tx), and:
//
//   - if fn returns an error OR panics, the transaction is rolled back and that
//     error is returned (a panic is re-raised after the rollback);
//   - otherwise the transaction is committed and the commit error (if any) is
//     returned.
//
// On success every write fn performed is durable as one unit; on any failure
// none of fn's writes are visible. This makes a multi-row state transition
// crash-atomic: a kill mid-write can only leave the pre- or post-state, never a
// partial one.
func WithTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	// The defer rolls back unless we've committed. It covers the error return
	// below AND a panic from fn (which re-raises after the rollback runs), so a
	// crashing closure can't leave a dangling transaction holding the write lock.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// WithProjectTx runs fn inside a single transaction on the per-project database.
// This is the handle the engine uses to make its multi-row task/plan/merge
// state transitions atomic.
func (s *Store) WithProjectTx(fn func(*sql.Tx) error) error {
	return WithTx(s.project, fn)
}
