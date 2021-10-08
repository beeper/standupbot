package store

import (
	"time"

	log "github.com/sirupsen/logrus"
	mid "maunium.net/go/mautrix/id"
)

// Setting which room to look for as the config room for a given user.
func (store *StateStore) SetConfigRoom(userID mid.UserID, roomID mid.RoomID) {
	store.UserConfigRoomCache[userID] = roomID
}

func (store *StateStore) GetConfigRoomId(userID mid.UserID) mid.RoomID {
	return store.UserConfigRoomCache[userID]
}

// Notification time handling

func (store *StateStore) SetTimezone(userID mid.UserID, timezone string) {
	store.UserTimezoneCache[userID] = timezone
}

func (store *StateStore) SetNotify(userID mid.UserID, minutesAfterMidnight int) {
	store.UserNotifyTimeCache[userID] = minutesAfterMidnight
}

func (store *StateStore) SetSendRoomId(userID mid.UserID, sendRoomID mid.RoomID) {
	store.UserSendRoomCache[userID] = sendRoomID
}

func (store *StateStore) GetSendRoomId(userID mid.UserID) (mid.RoomID, error) {
	return store.UserSendRoomCache[userID], nil
}

func (store *StateStore) GetCurrentWeekdayInUserTimezone(userID mid.UserID) time.Weekday {
	timezone, found := store.UserTimezoneCache[userID]
	if !found {
		return time.Now().UTC().Weekday()
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Now().UTC().Weekday()
	}
	return time.Now().In(location).Weekday()
}

func (store *StateStore) GetNotifyUsersForMinutesAfterUtcForToday() map[int]map[mid.UserID]mid.RoomID {
	notifyTimes := make(map[int]map[mid.UserID]mid.RoomID)

	for userID := range store.UserConfigRoomCache {
		timezone := store.UserTimezoneCache[userID]
		location, err := time.LoadLocation(timezone)
		if err != nil {
			continue
		}
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)

		// Don't add a notification time if it's on the weekend
		if midnight.Weekday() == time.Saturday || midnight.Weekday() == time.Sunday {
			log.Debugf("It is the weekend in %s, not including the notification time in the dictionary.", location)
			continue
		}

		minutesAfterMidnight := store.UserNotifyTimeCache[userID]
		notifyTime := midnight.Add(time.Duration(minutesAfterMidnight) * time.Minute)

		h, m, _ := notifyTime.UTC().Clock()
		minutesAfterUtcMidnight := h*60 + m

		if _, exists := notifyTimes[minutesAfterUtcMidnight]; !exists {
			notifyTimes[minutesAfterUtcMidnight] = make(map[mid.UserID]mid.RoomID)
		}
		roomID := store.UserConfigRoomCache[userID]
		notifyTimes[minutesAfterUtcMidnight][userID] = roomID
	}

	return notifyTimes
}
