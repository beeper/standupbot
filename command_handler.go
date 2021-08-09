package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
	Friday
	Weekend
	Today
	Blockers
	Notes
	Confirm
	Sent
)

type StandupItem struct {
	EventID       mid.EventID
	Body          string
	FormattedBody string
}

type StandupFlow struct {
	FlowID          uuid.UUID
	State           StandupFlowState
	ReactableEvents []mid.EventID
	PreviewEventId  mid.EventID
	Yesterday       []StandupItem
	Friday          []StandupItem
	Weekend         []StandupItem
	Today           []StandupItem
	Blockers        []StandupItem
	Notes           []StandupItem
}

var currentStandupFlows map[mid.UserID]*StandupFlow = make(map[mid.UserID]*StandupFlow)

func BlankStandupFlow() *StandupFlow {
	uuid, _ := uuid.NewUUID()
	return &StandupFlow{
		FlowID:          uuid,
		State:           FlowNotStarted,
		ReactableEvents: make([]mid.EventID, 0),
		Yesterday:       make([]StandupItem, 0),
		Friday:          make([]StandupItem, 0),
		Weekend:         make([]StandupItem, 0),
		Today:           make([]StandupItem, 0),
		Blockers:        make([]StandupItem, 0),
		Notes:           make([]StandupItem, 0),
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

			encrypted.RelatesTo = content.RelatesTo // The m.relates_to field should be unencrypted, so copy it.
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
* edit [Friday|Weekend|Yesterday|Today|Blockers|Notes] -- edit the given section of the standup post
* cancel -- cancel the current standup post
* help -- show this help
* vanquish -- tell the bot to leave the room
* tz [timezone] -- show or set the timezone to use for configuring notifications
* notify [time] -- show or set the time at which the standup notification will be sent
* room [room alias or ID] -- show or set the room where your standup notification will be sent

Version %s. Source code: https://sr.ht/~sumner/standupbot/`
	noticeHtml := `<b>COMMANDS:</b>
<ul>
<li><b>new</b> &mdash; prepare a new standup post</li>
<li><b>show</b> &mdash; show the current standup post</li>
<li><b>edit [Friday|Weekend|Yesterday|Today|Blockers|Notes]</b> &mdash; edit the given section of the standup post</li>
<li><b>cancel</b> &mdash; cancel the current standup post</li>
<li><b>help</b> &mdash; show this help</li>
<li><b>vanquish</b> &mdash; tell the bot to leave the room</li>
<li><b>tz [timezone]</b> &mdash; show or set the timezone to use for configuring notifications</li>
<li><b>notify [time]</b> &mdash; show or set the time at which the standup notification will be sent</li>
<li><b>room [room alias or ID]</b> &mdash; show or set the room where your standup notification will be sent</li>
</ul>

Version %s. <a href="https://sr.ht/~sumner/standupbot/">Source code</a>.`

	SendMessage(roomId, mevent.MessageEventContent{
		MsgType:       mevent.MsgNotice,
		Body:          fmt.Sprintf(noticeText, VERSION),
		Format:        mevent.FormatHTML,
		FormattedBody: fmt.Sprintf(noticeHtml, VERSION),
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
	noticeText := ""
	if err != nil {
		noticeText = fmt.Sprintf("Could not join room %s: %s", roomIdToJoin, err)
	} else {
		sendRoomID := respJoinRoom.(*mautrix.RespJoinRoom).RoomID
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

func EditPreview(roomID mid.RoomID, userID mid.UserID, flow *StandupFlow) []mid.EventID {
	newPost := FormatPost(userID, flow, true, true, false)
	resp, _ := SendMessage(roomID, mevent.MessageEventContent{
		MsgType:       mevent.MsgText,
		Body:          " * " + newPost.Body,
		Format:        mevent.FormatHTML,
		FormattedBody: " * " + newPost.FormattedBody,
		RelatesTo: &mevent.RelatesTo{
			Type:    mevent.RelReplace,
			EventID: flow.PreviewEventId,
		},
		NewContent: &newPost,
	})
	return append(flow.ReactableEvents, resp.EventID)
}

func HandleMessage(_ mautrix.EventSource, event *mevent.Event) {
	userId := mid.UserID(configuration.Username)
	localpart, _, _ := userId.ParseAndDecode()

	if event.Sender == userId {
		return
	}

	messageEventContent := event.Content.AsMessage()

	log.Debug("Received message with content: ", messageEventContent.Body)
	body := messageEventContent.Body
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "!")
	body = strings.TrimPrefix(body, "@")

	log.Debug("userid: ", localpart)
	log.Debug(!strings.HasPrefix(body, localpart), !strings.HasPrefix(body, "su"))
	if !strings.HasPrefix(body, localpart) && !strings.HasPrefix(body, "su") {
		if val, found := currentStandupFlows[event.Sender]; found {
			// If this is an edit of one of the messages that is in
			// the set of reactable messages, then
			relatesTo := messageEventContent.RelatesTo
			if relatesTo != nil && relatesTo.Type == mevent.RelReplace {
				for i, item := range val.Yesterday {
					if item.EventID == relatesTo.EventID {
						val.Yesterday[i].EventID = item.EventID
						val.Yesterday[i].Body = messageEventContent.NewContent.Body
						val.Yesterday[i].FormattedBody = messageEventContent.NewContent.FormattedBody
						break
					}
				}
				for i, item := range val.Friday {
					if item.EventID == relatesTo.EventID {
						val.Friday[i].EventID = item.EventID
						val.Friday[i].Body = messageEventContent.NewContent.Body
						val.Friday[i].FormattedBody = messageEventContent.NewContent.FormattedBody
						break
					}
				}
				for i, item := range val.Weekend {
					if item.EventID == relatesTo.EventID {
						val.Weekend[i].EventID = item.EventID
						val.Weekend[i].Body = messageEventContent.NewContent.Body
						val.Weekend[i].FormattedBody = messageEventContent.NewContent.FormattedBody
						break
					}
				}
				for i, item := range val.Today {
					if item.EventID == relatesTo.EventID {
						val.Today[i].EventID = item.EventID
						val.Today[i].Body = messageEventContent.NewContent.Body
						val.Today[i].FormattedBody = messageEventContent.NewContent.FormattedBody
						break
					}
				}
				for i, item := range val.Blockers {
					if item.EventID == relatesTo.EventID {
						val.Blockers[i].EventID = item.EventID
						val.Blockers[i].Body = messageEventContent.NewContent.Body
						val.Blockers[i].FormattedBody = messageEventContent.NewContent.FormattedBody
						break
					}
				}
				for i, item := range val.Notes {
					if item.EventID == relatesTo.EventID {
						val.Notes[i].EventID = item.EventID
						val.Notes[i].Body = messageEventContent.NewContent.Body
						val.Notes[i].FormattedBody = messageEventContent.NewContent.FormattedBody
						break
					}
				}

				if val.State == Confirm {
					val.ReactableEvents = EditPreview(event.RoomID, event.Sender, val)
				} else if val.State == Sent {
					client.RedactEvent(event.RoomID, val.PreviewEventId)
					ShowMessagePreview(event, val, true)
				}

				return
			}

			standupItem := StandupItem{
				EventID:       event.ID,
				Body:          messageEventContent.Body,
				FormattedBody: messageEventContent.FormattedBody,
			}

			switch val.State {
			case Yesterday:
				val.Yesterday = append(val.Yesterday, standupItem)
				break
			case Friday:
				val.Friday = append(val.Friday, standupItem)
				break
			case Weekend:
				val.Weekend = append(val.Weekend, standupItem)
				break
			case Today:
				val.Today = append(val.Today, standupItem)
				break
			case Blockers:
				val.Blockers = append(val.Blockers, standupItem)
				break
			case Notes:
				val.Notes = append(val.Notes, standupItem)
				break
			default:
				return
			}
			SendReaction(event.RoomID, event.ID, CHECKMARK)
			val.ReactableEvents = append(val.ReactableEvents, event.ID)
		}

		// This is not a bot command. Return.
		return
	}

	stateStore.SetConfigRoom(event.Sender, event.RoomID)

	body = strings.TrimPrefix(body, localpart)
	body = strings.TrimPrefix(body, "su")
	body = strings.TrimPrefix(body, ":")
	body = strings.TrimSpace(body)

	commandPartsRaw := strings.Split(strings.TrimSpace(body), " ")
	commandParts := make([]string, 0, len(commandPartsRaw))
	for _, part := range commandPartsRaw {
		if len(part) > 0 {
			commandParts = append(commandParts, part)
		}
	}

	if len(commandParts) == 0 {
		commandParts = append(commandParts, "help")
	} else if len(commandParts) == 1 && commandParts[0] == "" {
		commandParts[0] = "help"
	}

	switch strings.ToLower(commandParts[0]) {
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
			SendMessage(event.RoomID, FormatPost(event.Sender, currentFlow, true, false, false))
		} else {
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgText, Body: "No standup post to show."})
		}
		break
	case "edit":
		if len(commandParts) > 2 {
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "Incorrect number of parameters."})
		}
		if currentFlow, found := currentStandupFlows[event.Sender]; !found || currentFlow.State == FlowNotStarted {
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "No standup post to edit."})
		}

		switch strings.ToLower(commandParts[1]) {
		case "friday":
			if stateStore.GetCurrentWeekdayInUserTimezone(event.Sender) != time.Monday {
				SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "It's not Monday, so you can't go back to edit Friday."})
				return
			}
			GoToStateAndNotify(event.RoomID, event.Sender, Friday)
			break
		case "weekend":
			if stateStore.GetCurrentWeekdayInUserTimezone(event.Sender) != time.Monday {
				SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "It's not Monday, so you can't go back to edit the weekend."})
				return
			}
			GoToStateAndNotify(event.RoomID, event.Sender, Weekend)
			break
		case "yesterday":
			GoToStateAndNotify(event.RoomID, event.Sender, Yesterday)
			break
		case "today":
			GoToStateAndNotify(event.RoomID, event.Sender, Today)
			break
		case "blockers":
			GoToStateAndNotify(event.RoomID, event.Sender, Blockers)
			break
		case "notes":
			GoToStateAndNotify(event.RoomID, event.Sender, Notes)
			break
		default:
			SendMessage(event.RoomID, mevent.MessageEventContent{
				MsgType: mevent.MsgNotice,
				Body:    fmt.Sprintf("Invalid item to edit! Must be one of Friday, Weekend, Yesterday, Today, Blockers, or Notes"),
			})
		}
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
	default:
		SendHelp(event.RoomID)
		break
	}
}

func HandleRedaction(_ mautrix.EventSource, event *mevent.Event) {
	// Handle redactions
	if val, found := currentStandupFlows[event.Sender]; found {
		for i, item := range val.Yesterday {
			if item.EventID == event.Redacts {
				val.Yesterday = append(val.Yesterday[:i], val.Yesterday[i+1:]...)
				break
			}
		}
		for i, item := range val.Friday {
			if item.EventID == event.Redacts {
				val.Friday = append(val.Friday[:i], val.Friday[i+1:]...)
				break
			}
		}
		for i, item := range val.Weekend {
			if item.EventID == event.Redacts {
				val.Weekend = append(val.Weekend[:i], val.Weekend[i+1:]...)
				break
			}
		}
		for i, item := range val.Today {
			if item.EventID == event.Redacts {
				val.Today = append(val.Today[:i], val.Today[i+1:]...)
				break
			}
		}
		for i, item := range val.Blockers {
			if item.EventID == event.Redacts {
				val.Blockers = append(val.Blockers[:i], val.Blockers[i+1:]...)
				break
			}
		}
		for i, item := range val.Notes {
			if item.EventID == event.Redacts {
				val.Notes = append(val.Notes[:i], val.Notes[i+1:]...)
				break
			}
		}

		if val.PreviewEventId.String() != "" {
			val.ReactableEvents = EditPreview(event.RoomID, event.Sender, val)
		}
	}
}
