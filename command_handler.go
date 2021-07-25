package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mcrypto "maunium.net/go/mautrix/crypto"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"
)

type StandupFlowState int

const (
	FlowNotStarted StandupFlowState = iota
	Yesterday
	Today
	Blockers
	Notes
	Confirm
)

type StandupFlow struct {
	State           StandupFlowState
	ReactableEvents []mid.EventID
	Yesterday       []string
	Today           []string
	Blockers        []string
	Notes           []string
}

var currentStandupFlows map[mid.UserID]*StandupFlow = make(map[mid.UserID]*StandupFlow)

func BlankStandupFlow() *StandupFlow {
	return &StandupFlow{
		State:           FlowNotStarted,
		ReactableEvents: make([]mid.EventID, 0),
		Yesterday:       make([]string, 0),
		Today:           make([]string, 0),
		Blockers:        make([]string, 0),
		Notes:           make([]string, 0),
	}
}

func SendMessage(roomId mid.RoomID, content mevent.MessageEventContent) (resp *mautrix.RespSendEvent, err error) {
	r, err := DoRetry("send message to "+roomId.String(), func() (interface{}, error) {
		if stateStore.IsEncrypted(roomId) {
			log.Debugf("Sending event to %s encrypted: %+v", roomId, content)
			encrypted, err := olmMachine.EncryptMegolmEvent(roomId, mevent.EventMessage, content)

			// These three errors mean we have to make a new Megolm session
			if err == mcrypto.SessionExpired || err == mcrypto.SessionNotShared || err == mcrypto.NoGroupSession {
				err = olmMachine.ShareGroupSession(roomId, stateStore.GetRoomMembers(roomId))
				if err != nil {
					log.Errorf("Failed to share group session to %s: %s", roomId, err)
					return nil, err
				}
				encrypted, err = olmMachine.EncryptMegolmEvent(roomId, mevent.EventMessage, content)
			}

			if err != nil {
				log.Errorf("Failed to encrypt message to %s: %s", roomId, err)
				return nil, err
			}

			return client.SendMessageEvent(roomId, mevent.EventEncrypted, encrypted)
		} else {
			log.Debugf("Sending event to %s unencrypted: %+v", roomId, content)
			return client.SendMessageEvent(roomId, mevent.EventMessage, content)
		}
	})
	if err != nil {
		// give up
		log.Errorf("Failed to send message to %s: %s", roomId, err)
		return nil, err
	}
	return r.(*mautrix.RespSendEvent), err
}

func SendReaction(roomId mid.RoomID, eventID mid.EventID, reaction string) (resp *mautrix.RespSendEvent, err error) {
	r, err := DoRetry("send reaction", func() (interface{}, error) {
		return client.SendReaction(roomId, eventID, reaction)
	})
	if err != nil {
		// give up
		log.Errorf("Failed to send reaction to %s in %s: %s", eventID, roomId, err)
		return nil, err
	}
	return r.(*mautrix.RespSendEvent), err
}

func SendHelp(roomId mid.RoomID) {
	// send message to channel confirming join (retry 3 times)
	noticeText := `COMMANDS:
* new -- prepare a new standup post
* show -- show the current standup post
* cancel -- cancel the current standup post
* help -- show this help
* vanquish -- tell the bot to leave the room
* tz [timezone] -- show or set the timezone to use for configuring notifications
* notify [time] -- show or set the time at which the standup notification will be sent
* room [room alias or ID] -- show or set the room where your standup notification will be sent`
	noticeHtml := `<b>COMMANDS:</b>
<ul>
<li><b>new</b> &mdash; prepare a new standup post</li>
<li><b>show</b> &mdash; show the current standup post</li>
<li><b>cancel</b> &mdash; cancel the current standup post</li>
<li><b>help</b> &mdash; show this help</li>
<li><b>vanquish</b> &mdash; tell the bot to leave the room</li>
<li><b>tz [timezone]</b> &mdash; show or set the timezone to use for configuring notifications</li>
<li><b>notify [time]</b> &mdash; show or set the time at which the standup notification will be sent</li>
<li><b>room [room alias or ID]</b> &mdash; show or set the room where your standup notification will be sent</li>
</ul>`

	SendMessage(roomId, mevent.MessageEventContent{
		MsgType:       mevent.MsgNotice,
		Body:          noticeText,
		Format:        mevent.FormatHTML,
		FormattedBody: noticeHtml,
	})
}

// Timezone
var StateTzSetting = mevent.Type{"com.nevarro.standupbot.timezone", mevent.StateEventType}

type TzSettingEventContent struct {
	TzString string
}

func HandleTimezone(roomId mid.RoomID, sender mid.UserID, params []string) {
	stateKey := strings.TrimPrefix(sender.String(), "@")
	if len(params) == 0 {
		tzStr := "not set"

		var tzSettingEventContent TzSettingEventContent
		err := client.StateEvent(roomId, StateTzSetting, stateKey, &tzSettingEventContent)
		if err == nil {
			tzStr = tzSettingEventContent.TzString
		}

		noticeText := fmt.Sprintf("Timezone is set to %s", tzStr)
		SendMessage(roomId, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
		return
	}

	location, err := time.LoadLocation(params[0])
	if err != nil {
		errorMessageText := fmt.Sprintf("%s is not a recognized timezone. Use the name corresponding to a file in the IANA Time Zone database, such as 'America/New_York'", params[0])
		SendMessage(roomId, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: errorMessageText})
	}

	_, err = client.SendStateEvent(roomId, StateTzSetting, stateKey, TzSettingEventContent{
		TzString: location.String(),
	})
	noticeText := fmt.Sprintf("Timezone set to %s", location.String())
	if err != nil {
		noticeText = fmt.Sprintf("Failed setting timezone: %s\nCheck to make sure that standupbot is a mod/admin in the room!", err)
	} else {
		stateStore.SetTimezone(sender, location.String())
	}
	SendMessage(roomId, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
}

// Notify
var StateNotify = mevent.Type{"com.nevarro.standupbot.notify", mevent.StateEventType}

type NotifyEventContent struct {
	MinutesAfterMidnight int
}

func HandleNotify(roomId mid.RoomID, sender mid.UserID, params []string) {
	stateKey := strings.TrimPrefix(sender.String(), "@")
	if len(params) == 0 {
		var notifyEventContent NotifyEventContent
		err := client.StateEvent(roomId, StateNotify, stateKey, &notifyEventContent)
		var noticeText string
		if err != nil {
			noticeText = "Notification time is not set"
		} else {
			offset := time.Minute * time.Duration(notifyEventContent.MinutesAfterMidnight)
			offset = offset.Round(time.Minute)
			noticeText = fmt.Sprintf("Notification time is set to %02d:%02d", int(offset.Hours()), int(offset.Minutes())%60)
		}

		SendMessage(roomId, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
		return
	}

	timeRe := regexp.MustCompile(`(\d\d?):?(\d\d)`)
	groups := timeRe.FindStringSubmatch(params[0])
	noticeText := ""
	if groups == nil {
		noticeText = fmt.Sprintf("%s is not a valid time. Please specify it in 24-hour time like: 13:30.", params[0])
	} else {
		hours, hoursErr := strconv.Atoi(groups[1])
		minutes, minutesErr := strconv.Atoi(groups[2])

		if hoursErr != nil || minutesErr != nil || hours < 0 || hours > 24 || minutes < 0 || minutes > 60 {
			noticeText = fmt.Sprintf("%s is not a valid time. Please specify it in 24-hour time like: 13:30.", params[0])
		} else {
			noticeText = fmt.Sprintf("Notification time set to %02d:%02d", hours, minutes)
			minutesAfterMidnight := minutes + hours*60
			_, err := client.SendStateEvent(roomId, StateNotify, stateKey, NotifyEventContent{
				MinutesAfterMidnight: minutesAfterMidnight,
			})
			if err != nil {
				noticeText = fmt.Sprintf("Failed setting notification time: %s\nCheck to make sure that standupbot is a mod/admin in the room!", err)
			} else {
				stateStore.SetNotify(sender, minutesAfterMidnight)
			}
		}
	}
	SendMessage(roomId, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
}

// Room
var StateSendRoom = mevent.Type{"com.nevarro.standupbot.send_room", mevent.StateEventType}

type SendRoomEventContent struct {
	SendRoomID mid.RoomID
}

func HandleRoom(roomID mid.RoomID, sender mid.UserID, params []string) {
	stateKey := strings.TrimPrefix(sender.String(), "@")
	if len(params) == 0 {
		var sendRoomEventContent SendRoomEventContent
		err := client.StateEvent(roomID, StateSendRoom, stateKey, &sendRoomEventContent)
		var noticeText string
		if err != nil {
			noticeText = "Send room not set"
		} else {
			noticeText = fmt.Sprintf("Send room is set to %s", sendRoomEventContent.SendRoomID)
		}

		SendMessage(roomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
		return
	}

	roomIdToJoin := params[0]
	serverName := ""
	if len(params) > 1 {
		serverName = params[1]
	}

	log.Info("Joining ", roomIdToJoin)
	respJoinRoom, err := DoRetry("join room", func() (interface{}, error) {
		return client.JoinRoom(roomIdToJoin, serverName, nil)
	})
	sendRoomID := respJoinRoom.(*mautrix.RespJoinRoom).RoomID
	noticeText := ""
	if err != nil {
		noticeText = fmt.Sprintf("Could not join room %s: %s", roomIdToJoin, err)
	} else {
		noticeText = fmt.Sprintf("Joined %s and set that as your send room", roomIdToJoin)
		_, err := client.SendStateEvent(roomID, StateSendRoom, stateKey, SendRoomEventContent{
			SendRoomID: sendRoomID,
		})
		if err != nil {
			noticeText = fmt.Sprintf("Failed setting send room: %s\nCheck to make sure that standupbot is a mod/admin in the room!", err)
		} else {
			stateStore.SetSendRoomId(sender, sendRoomID)
		}
	}

	SendMessage(roomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
}

func HandleMessage(_ mautrix.EventSource, event *mevent.Event) {
	messageEventContent := event.Content.AsMessage()

	log.Debug("Received message with content: ", messageEventContent.Body)
	body := messageEventContent.Body
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "!")
	body = strings.TrimPrefix(body, "@")

	userId := mid.UserID(configuration.Username)
	localpart, _, _ := userId.ParseAndDecode()

	log.Debug("userid: ", localpart)
	if !strings.HasPrefix(body, localpart) {
		if val, found := currentStandupFlows[event.Sender]; found {
			switch val.State {
			case Yesterday:
				val.Yesterday = append(val.Yesterday, body)
				break
			case Today:
				val.Today = append(val.Today, body)
				break
			case Blockers:
				val.Blockers = append(val.Blockers, body)
				break
			case Notes:
				val.Notes = append(val.Notes, body)
				break
			default:
				return
			}
			SendReaction(event.RoomID, event.ID, CHECKMARK)
			val.ReactableEvents = append(val.ReactableEvents, event.ID)
		}
		return
	}

	stateStore.SetConfigRoom(event.Sender, event.RoomID)

	body = strings.TrimPrefix(body, localpart)
	body = strings.TrimPrefix(body, ":")
	body = strings.TrimSpace(body)

	commandParts := strings.Split(body, " ")
	if len(commandParts) == 1 && commandParts[0] == "" {
		commandParts[0] = "help"
	}

	switch strings.ToLower(commandParts[0]) {
	case "help":
		SendHelp(event.RoomID)
		break
	case "vanquish":
		DoRetry("leave room", func() (interface{}, error) {
			return client.LeaveRoom(event.RoomID)
		})
		break
	case "tz":
		HandleTimezone(event.RoomID, event.Sender, commandParts[1:])
		break
	case "notify":
		HandleNotify(event.RoomID, event.Sender, commandParts[1:])
		break
	case "new":
		currentStandupFlows[event.Sender] = BlankStandupFlow()
		CreatePost(event.RoomID, event.Sender)
		break
	case "show":
		if currentFlow, found := currentStandupFlows[event.Sender]; found && currentFlow.State != FlowNotStarted {
			SendMessage(event.RoomID, FormatPost(event.Sender, currentFlow, true, false))
		} else {
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgText, Body: "No standup post to show."})
		}
		break
	case "cancel":
		if val, found := currentStandupFlows[event.Sender]; !found || val.State == FlowNotStarted {
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "No standup post to cancel."})
		} else {
			currentStandupFlows[event.Sender] = BlankStandupFlow()
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "Standup post cancelled"})
		}
		break
	case "room":
		HandleRoom(event.RoomID, event.Sender, commandParts[1:])
		break
	}
}
