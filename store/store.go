package store

import "database/sql"

type StateStore struct {
	DB *sql.DB
}

func NewStateStore(db *sql.DB) *StateStore {
	return &StateStore{DB: db}
}

func (store *StateStore) CreateTables() error {
	tx, err := store.DB.Begin()
	if err != nil {
		return err
	}

	queries := []string{
		`
		CREATE TABLE IF NOT EXISTS standupbot_meta (
			access_token VARCHAR(255)
		)
		`,
	}

	for _, query := range queries {
		if _, err := tx.Exec(query); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}
