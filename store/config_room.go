package store

import (
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gitlab.com/beeper/standupbot/types"
	mid "maunium.net/go/mautrix/id"
)

// Setting which room to look for as the config room for a given user.
func (store *StateStore) SetConfigRoom(userID mid.UserID, roomID mid.RoomID) {
	store.UserConfigRooms[userID] = roomID
}

func (store *StateStore) GetConfigRoomId(userID mid.UserID) mid.RoomID {
	return store.UserConfigRooms[userID]
}

// Use threads or not?
func (store *StateStore) SetUseThreads(userID mid.UserID, useThreads bool) {
	store.userUseThreadsCache[userID] = useThreads
}

func (store *StateStore) GetUseThreads(userID mid.UserID) (bool, error) {
	useThreads, found := store.userUseThreadsCache[userID]
	if !found {
		roomID := store.GetConfigRoomId(userID)
		stateKey := strings.TrimPrefix(userID.String(), "@")
		var useThreadsEventContent types.UseThreadsEventContent
		if err := store.Client.StateEvent(roomID, types.StateUseThreads, stateKey, &useThreadsEventContent); err == nil {
			useThreads = useThreadsEventContent.UseThreads
			store.userUseThreadsCache[userID] = useThreads
		} else {
			return false, err
		}
	}
	return useThreads, nil
}

// Notification time handling

func (store *StateStore) SetTimezone(userID mid.UserID, timezone string) {
	store.userTimezoneCache[userID] = timezone
}

func (store *StateStore) GetTimezone(userID mid.UserID) *time.Location {
	timezone, found := store.userTimezoneCache[userID]
	if !found {
		roomID := store.GetConfigRoomId(userID)
		stateKey := strings.TrimPrefix(userID.String(), "@")
		var tzSettingEventContent types.TzSettingEventContent
		if err := store.Client.StateEvent(roomID, types.StateTzSetting, stateKey, &tzSettingEventContent); err == nil {
			timezone = tzSettingEventContent.TzString
			store.userTimezoneCache[userID] = timezone
		}
	}

	if location, err := time.LoadLocation(timezone); err == nil {
		return location
	}
	return time.UTC
}

func (store *StateStore) RemoveNotify(userID mid.UserID) {
	delete(store.userNotifyTimeCache, userID)
}

func (store *StateStore) SetNotify(userID mid.UserID, minutesAfterMidnight int) {
	store.userNotifyTimeCache[userID] = minutesAfterMidnight
}

func (store *StateStore) GetNotify(userID mid.UserID) (int, error) {
	minutesAfterMidnight, found := store.userNotifyTimeCache[userID]
	if !found {
		roomID := store.GetConfigRoomId(userID)
		stateKey := strings.TrimPrefix(userID.String(), "@")
		var notifyEventContent types.NotifyEventContent
		if err := store.Client.StateEvent(roomID, types.StateNotify, stateKey, &notifyEventContent); err == nil {
			if notifyEventContent.MinutesAfterMidnight != nil {
				minutesAfterMidnight = *notifyEventContent.MinutesAfterMidnight
				store.userNotifyTimeCache[userID] = minutesAfterMidnight
			}
		} else {
			// No notify time
			return 0, err
		}
	}
	return minutesAfterMidnight, nil
}

func (store *StateStore) SetSendRoomId(userID mid.UserID, sendRoomID mid.RoomID) {
	store.userSendRoomCache[userID] = sendRoomID
}

func (store *StateStore) GetSendRoomId(userID mid.UserID) (mid.RoomID, error) {
	sendRoomID, found := store.userSendRoomCache[userID]
	if !found {
		roomID := store.GetConfigRoomId(userID)
		stateKey := strings.TrimPrefix(userID.String(), "@")
		var sendRoomEventContent types.SendRoomEventContent
		if err := store.Client.StateEvent(roomID, types.StateSendRoom, stateKey, &sendRoomEventContent); err == nil {
			sendRoomID = sendRoomEventContent.SendRoomID
			store.userSendRoomCache[userID] = sendRoomID
		} else {
			// No send room
			return mid.RoomID(""), err
		}
	}
	return sendRoomID, nil
}

func (store *StateStore) GetCurrentWeekdayInUserTimezone(userID mid.UserID) time.Weekday {
	timezone, found := store.userTimezoneCache[userID]
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

	for userID := range store.UserConfigRooms {
		location := store.GetTimezone(userID)
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)

		// Don't add a notification time if it's on the weekend
		if midnight.Weekday() == time.Saturday || midnight.Weekday() == time.Sunday {
			log.Debugf("It is the weekend in %s, not including the notification time in the dictionary.", location)
			continue
		}

		minutesAfterMidnight, err := store.GetNotify(userID)
		if err != nil || minutesAfterMidnight == 0 {
			continue
		}
		notifyTime := midnight.Add(time.Duration(minutesAfterMidnight) * time.Minute)

		h, m, _ := notifyTime.UTC().Clock()
		minutesAfterUtcMidnight := h*60 + m

		if _, exists := notifyTimes[minutesAfterUtcMidnight]; !exists {
			notifyTimes[minutesAfterUtcMidnight] = make(map[mid.UserID]mid.RoomID)
		}
		roomID := store.UserConfigRooms[userID]
		notifyTimes[minutesAfterUtcMidnight][userID] = roomID
	}

	return notifyTimes
}
