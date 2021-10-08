package store

import (
	"database/sql"

	"maunium.net/go/mautrix"
	mid "maunium.net/go/mautrix/id"
)

type StateStore struct {
	DB              *sql.DB
	Client          *mautrix.Client
	UserConfigRooms map[mid.UserID]mid.RoomID

	// Caches for configuration.
	// If these become too large, we can make these LRU caches, but for
	// now, they are small enough they don't matter.
	userTimezoneCache   map[mid.UserID]string
	userNotifyTimeCache map[mid.UserID]int
	userSendRoomCache   map[mid.UserID]mid.RoomID
	userUseThreadsCache map[mid.UserID]bool
}

func NewStateStore(db *sql.DB) *StateStore {
	return &StateStore{
		DB:              db,
		UserConfigRooms: map[mid.UserID]mid.RoomID{},

		userTimezoneCache:   map[mid.UserID]string{},
		userNotifyTimeCache: map[mid.UserID]int{},
		userSendRoomCache:   map[mid.UserID]mid.RoomID{},
		userUseThreadsCache: map[mid.UserID]bool{},
	}
}

func (store *StateStore) CreateTables() error {
	tx, err := store.DB.Begin()
	if err != nil {
		return err
	}

	queries := []string{
		`
		CREATE TABLE IF NOT EXISTS standupbot_meta (
			meta_id       INTEGER PRIMARY KEY,
			access_token  VARCHAR(255)
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS user_filter_ids (
			user_id    VARCHAR(255) PRIMARY KEY,
			filter_id  VARCHAR(255)
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS user_batch_tokens (
			user_id           VARCHAR(255) PRIMARY KEY,
			next_batch_token  VARCHAR(255)
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS rooms (
			room_id           VARCHAR(255) PRIMARY KEY,
			encryption_event  VARCHAR(65535) NULL
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS room_members (
			room_id  VARCHAR(255),
			user_id  VARCHAR(255),
			PRIMARY KEY (room_id, user_id)
		)
		`,
		`
		DROP TABLE IF EXISTS user_config_room
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
