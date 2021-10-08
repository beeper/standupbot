package types

import (
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"
)

var StateTzSetting = mevent.Type{Type: "com.nevarro.standupbot.timezone", Class: mevent.StateEventType}
var StateNotify = mevent.Type{Type: "com.nevarro.standupbot.notify", Class: mevent.StateEventType}
var StateSendRoom = mevent.Type{Type: "com.nevarro.standupbot.send_room", Class: mevent.StateEventType}

type TzSettingEventContent struct {
	TzString string
}

type NotifyEventContent struct {
	MinutesAfterMidnight int
}

type SendRoomEventContent struct {
	SendRoomID mid.RoomID
}
