package store

import (
	log "github.com/sirupsen/logrus"
)

func (store *StateStore) GetAccessToken() (string, error) {
	row := store.DB.QueryRow("SELECT access_token FROM standupbot_meta")
	var access_token string
	if err := row.Scan(&access_token); err != nil {
		return "", err
	}

	return access_token, nil
}

func (store *StateStore) SetAccessToken(accessToken string) error {
	row := store.DB.QueryRow("SELECT COUNT(*) AS count FROM standupbot_meta")

	var rowCount int64
	if err := row.Scan(&rowCount); err != nil {
		return err
	}

	if rowCount == 0 {
		log.Info("Inserting row into standupbot_meta")
		insert := "INSERT INTO standupbot_meta VALUES (?)"
		if _, err := store.DB.Exec(insert, accessToken); err != nil {
			return err
		}
	} else {
		log.Info("Updating row into standupbot_meta")
		update := "UPDATE standupbot_meta SET access_token = ?"
		if _, err := store.DB.Exec(update, accessToken); err != nil {
			return err
		}
	}

	return nil
}
