package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"

	"gitlab.com/beeper/standupbot/types"
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

	Threads
	ThreadsFriday
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
	ResendEventId   *mid.EventID

	Yesterday []StandupItem
	Friday    []StandupItem
	Weekend   []StandupItem
	Today     []StandupItem
	Blockers  []StandupItem
	Notes     []StandupItem

	// Root events for threads
	YesterdayThreadEvents []mid.EventID
	FridayThreadEvents    []mid.EventID
	WeekendThreadEvents   []mid.EventID
	TodayThreadEvents     []mid.EventID
	BlockersThreadEvents  []mid.EventID
	NotesThreadEvents     []mid.EventID
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

		YesterdayThreadEvents: make([]mid.EventID, 0),
		FridayThreadEvents:    make([]mid.EventID, 0),
		WeekendThreadEvents:   make([]mid.EventID, 0),
		TodayThreadEvents:     make([]mid.EventID, 0),
		BlockersThreadEvents:  make([]mid.EventID, 0),
		NotesThreadEvents:     make([]mid.EventID, 0),
	}
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
* undo -- undo sending the current standup post to the send room
* help -- show this help
* vanquish -- tell the bot to leave the room
* tz [timezone] -- show or set the timezone to use for configuring notifications
* notify [time] -- show or set the time at which the standup notification will be sent
* room [room alias or ID] -- show or set the room where your standup notification will be sent
* threads [true|false] -- whether or not to use threads for composing standup posts

Version %s. Source code: https://gitlab.com/beeper/standupbot/`
	noticeHtml := `<b>COMMANDS:</b>
<ul>
<li><b>new</b> &mdash; prepare a new standup post</li>
<li><b>show</b> &mdash; show the current standup post</li>
<li><b>edit [Friday|Weekend|Yesterday|Today|Blockers|Notes]</b> &mdash; edit the given section of the standup post</li>
<li><b>cancel</b> &mdash; cancel the current standup post</li>
<li><b>undo</b> &mdash; undo sending the current standup post to the send room</li>
<li><b>help</b> &mdash; show this help</li>
<li><b>vanquish</b> &mdash; tell the bot to leave the room</li>
<li><b>tz [timezone]</b> &mdash; show or set the timezone to use for configuring notifications</li>
<li><b>notify [time]</b> &mdash; show or set the time at which the standup notification will be sent</li>
<li><b>room [room alias or ID]</b> &mdash; show or set the room where your standup notification will be sent</li>
<li><b>threads [true|false]</b> &mdash; whether or not to use threads for composing standup posts</li>
</ul>

Version %s. <a href="https://gitlab.com/beeper/standupbot/">Source code</a>.`

	SendMessage(roomId, &mevent.MessageEventContent{
		MsgType:       mevent.MsgNotice,
		Body:          fmt.Sprintf(noticeText, VERSION),
		Format:        mevent.FormatHTML,
		FormattedBody: fmt.Sprintf(noticeHtml, VERSION),
	})
}

// Timezone
func HandleTimezone(roomId mid.RoomID, sender mid.UserID, params []string) {
	stateKey := strings.TrimPrefix(sender.String(), "@")
	if len(params) == 0 {
		tzStr := "not set"

		var tzSettingEventContent types.TzSettingEventContent
		err := client.StateEvent(roomId, types.StateTzSetting, stateKey, &tzSettingEventContent)
		if err == nil {
			tzStr = tzSettingEventContent.TzString
		}

		noticeText := fmt.Sprintf("Timezone is set to %s", tzStr)
		SendMessage(roomId, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
		return
	}

	location, err := time.LoadLocation(params[0])
	if err != nil {
		errorMessageText := fmt.Sprintf("%s is not a recognized timezone. Use the name corresponding to a file in the IANA Time Zone database, such as 'America/New_York'", params[0])
		SendMessage(roomId, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: errorMessageText})
	}

	_, err = client.SendStateEvent(roomId, types.StateTzSetting, stateKey, types.TzSettingEventContent{
		TzString: location.String(),
	})
	noticeText := fmt.Sprintf("Timezone set to %s", location.String())
	if err != nil {
		noticeText = fmt.Sprintf("Failed setting timezone: %s\nCheck to make sure that standupbot is a mod/admin in the room!", err)
	} else {
		stateStore.SetTimezone(sender, location.String())
	}
	SendMessage(roomId, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
}

// Notify
func HandleNotify(roomId mid.RoomID, sender mid.UserID, params []string) {
	stateKey := strings.TrimPrefix(sender.String(), "@")
	if len(params) == 0 {
		var notifyEventContent types.NotifyEventContent
		err := client.StateEvent(roomId, types.StateNotify, stateKey, &notifyEventContent)
		var noticeText string
		if err != nil {
			noticeText = "Notification time is not set"
		} else {
			offset := time.Minute * time.Duration(notifyEventContent.MinutesAfterMidnight)
			offset = offset.Round(time.Minute)
			noticeText = fmt.Sprintf("Notification time is set to %02d:%02d", int(offset.Hours()), int(offset.Minutes())%60)
		}

		SendMessage(roomId, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
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
			_, err := client.SendStateEvent(roomId, types.StateNotify, stateKey, types.NotifyEventContent{
				MinutesAfterMidnight: minutesAfterMidnight,
			})
			if err != nil {
				noticeText = fmt.Sprintf("Failed setting notification time: %s\nCheck to make sure that standupbot is a mod/admin in the room!", err)
			} else {
				stateStore.SetNotify(sender, minutesAfterMidnight)
			}
		}
	}
	SendMessage(roomId, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
}

// Threads
func HandleThreads(roomID mid.RoomID, sender mid.UserID, params []string) {
	if len(params) == 0 {
		useThreads, err := stateStore.GetUseThreads(sender)
		var noticeText string
		if err != nil {
			noticeText = "Use threads setting is not set. Will default to not using threads."
		} else if useThreads {
			noticeText = "Using threads is enabled."
		} else {
			noticeText = "Using threads is not enabled."
		}

		SendMessage(roomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
		return
	}

	useThreadsStr := strings.ToLower(params[0])
	var useThreads bool
	if useThreadsStr == "true" {
		useThreads = true
	} else if useThreadsStr == "false" {
		useThreads = false
	} else {
		noticeText := fmt.Sprintf("Failed setting use threads option: %s is not valid. Use 'true' or 'false'.", useThreadsStr)
		SendMessage(roomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
		return
	}

	stateKey := strings.TrimPrefix(sender.String(), "@")
	_, err := client.SendStateEvent(roomID, types.StateUseThreads, stateKey, types.UseThreadsEventContent{
		UseThreads: useThreads,
	})
	var noticeText string
	if err != nil {
		noticeText = fmt.Sprintf("Failed setting use threads option: %+v", err)
	} else {
		noticeText = fmt.Sprintf("Set use threads option to %s", useThreadsStr)
		stateStore.SetUseThreads(sender, useThreads)
	}
	SendMessage(roomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
}

// Room
func HandleRoom(roomID mid.RoomID, event *mevent.Event, params []string) {
	stateKey := strings.TrimPrefix(event.Sender.String(), "@")
	if len(params) == 0 {
		sendRoomID, err := stateStore.GetSendRoomId(event.Sender)
		var noticeText string
		if err != nil {
			noticeText = "Send room not set"
		} else {
			noticeText = fmt.Sprintf("Send room is set to %s", sendRoomID)
		}

		SendMessage(roomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})
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
		_, err := client.SendStateEvent(roomID, types.StateSendRoom, stateKey, types.SendRoomEventContent{
			SendRoomID: sendRoomID,
		})
		if err != nil {
			noticeText = fmt.Sprintf("Failed setting send room: %s\nCheck to make sure that standupbot is a mod/admin in the room!", err)
		} else {
			stateStore.SetSendRoomId(event.Sender, sendRoomID)
		}
	}

	SendMessage(roomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: noticeText})

	if currentFlow, found := currentStandupFlows[event.Sender]; found && currentFlow.State == Confirm {
		client.RedactEvent(event.RoomID, currentFlow.PreviewEventId)
		ShowMessagePreview(event.RoomID, event.Sender, currentFlow, false)
	}
}

func EditPreview(roomID mid.RoomID, userID mid.UserID, flow *StandupFlow) []mid.EventID {
	newPost := FormatPost(userID, flow, true, true, false)
	resp, _ := SendMessage(roomID, &mevent.MessageEventContent{
		MsgType:       mevent.MsgText,
		Body:          " * " + newPost.Body,
		Format:        mevent.FormatHTML,
		FormattedBody: " * " + newPost.FormattedBody,
		RelatesTo: &mevent.RelatesTo{
			Type:    mevent.RelReplace,
			EventID: flow.PreviewEventId,
		},
		NewContent: newPost,
	})
	return append(flow.ReactableEvents, resp.EventID)
}

func getCommandParts(body string) ([]string, error) {
	userId := mid.UserID(configuration.Username)
	localpart, _, _ := userId.ParseAndDecode()

	// Valid command strings include:
	// standupbot: foo
	// !su foo
	// !standupbot foo
	// @standupbot foo
	// @standupbot: foo

	validCommandRegexes := []*regexp.Regexp{
		regexp.MustCompile(fmt.Sprintf("^%s:(.*)$", localpart)),
		regexp.MustCompile(fmt.Sprintf("^@%s:?(.*)$", localpart)),
		regexp.MustCompile("^!standupbot$"),
		regexp.MustCompile("^!standupbot:? (.*)$"),
		regexp.MustCompile("^!su$"),
		regexp.MustCompile("^!su:? (.*)$"),
	}

	body = strings.TrimSpace(body)

	isCommand := false
	commandParts := []string{}
	for _, commandRe := range validCommandRegexes {
		match := commandRe.FindStringSubmatch(body)
		if match != nil {
			isCommand = true
			if len(match) > 1 {
				commandParts = strings.Split(match[1], " ")
			} else {
				commandParts = []string{"help"}
			}
			break
		}
	}
	if !isCommand {
		return []string{}, errors.New("Not a command")
	}

	return commandParts, nil
}

func tryEditListItem(standupList []StandupItem, editEventID mid.EventID, newContent *mevent.MessageEventContent) bool {
	for i, item := range standupList {
		if item.EventID == editEventID {
			standupList[i].Body = mevent.TrimReplyFallbackText(newContent.Body)
			standupList[i].FormattedBody = mevent.TrimReplyFallbackHTML(newContent.FormattedBody)
			return true
		}
	}
	return false
}

func HandleEdit(event *mevent.Event, standupFlow *StandupFlow) {
	// This is an edit. If it's an edit to one of the messages in the
	// current standup, then edit the entry in the corresponding list.
	messageEventContent := event.Content.AsMessage()
	relatesTo := messageEventContent.RelatesTo
	standupLists := [][]StandupItem{standupFlow.Yesterday, standupFlow.Friday, standupFlow.Weekend, standupFlow.Today, standupFlow.Blockers, standupFlow.Notes}
	edited := false
	for _, standupList := range standupLists {
		if tryEditListItem(standupList, relatesTo.EventID, messageEventContent.NewContent) {
			edited = true
			break
		}
	}

	if edited {
		if standupFlow.State == Threads || standupFlow.State == ThreadsFriday {
			EditPreview(event.RoomID, event.Sender, standupFlow)
		} else if standupFlow.State == Confirm {
			standupFlow.ReactableEvents = EditPreview(event.RoomID, event.Sender, standupFlow)
		} else if standupFlow.State == Sent {
			client.RedactEvent(event.RoomID, standupFlow.PreviewEventId)
			ShowMessagePreview(event.RoomID, event.Sender, standupFlow, true)
		}
	}
}

func HandleReply(event *mevent.Event, standupFlow *StandupFlow) {
	// This is a reply. This only matters in thread mode.
	switch standupFlow.State {
	case Threads, ThreadsFriday, Confirm, Sent:
		log.Info("Reply in thread mode.")
		break
	default:
		log.Info("Reply not in thread mode.")
		return
	}

	messageEventContent := event.Content.AsMessage()
	relatesTo := messageEventContent.RelatesTo
	edited := false

	standupItem := StandupItem{
		EventID:       event.ID,
		Body:          mevent.TrimReplyFallbackText(messageEventContent.Body),
		FormattedBody: mevent.TrimReplyFallbackHTML(messageEventContent.FormattedBody),
	}
	for _, eventID := range standupFlow.YesterdayThreadEvents {
		if eventID == relatesTo.EventID {
			standupFlow.Yesterday = append(standupFlow.Yesterday, standupItem)
			standupFlow.YesterdayThreadEvents = append(standupFlow.YesterdayThreadEvents, event.ID)
			edited = true
		}
	}
	for _, eventID := range standupFlow.FridayThreadEvents {
		if eventID == relatesTo.EventID {
			standupFlow.Friday = append(standupFlow.Friday, standupItem)
			standupFlow.FridayThreadEvents = append(standupFlow.FridayThreadEvents, event.ID)
			edited = true
		}
	}
	for _, eventID := range standupFlow.WeekendThreadEvents {
		if eventID == relatesTo.EventID {
			standupFlow.Weekend = append(standupFlow.Weekend, standupItem)
			standupFlow.WeekendThreadEvents = append(standupFlow.WeekendThreadEvents, event.ID)
			edited = true
		}
	}
	for _, eventID := range standupFlow.TodayThreadEvents {
		if eventID == relatesTo.EventID {
			standupFlow.Today = append(standupFlow.Today, standupItem)
			standupFlow.TodayThreadEvents = append(standupFlow.TodayThreadEvents, event.ID)
			edited = true
		}
	}
	for _, eventID := range standupFlow.BlockersThreadEvents {
		if eventID == relatesTo.EventID {
			standupFlow.Blockers = append(standupFlow.Blockers, standupItem)
			standupFlow.BlockersThreadEvents = append(standupFlow.BlockersThreadEvents, event.ID)
			edited = true
		}
	}
	for _, eventID := range standupFlow.NotesThreadEvents {
		if eventID == relatesTo.EventID {
			standupFlow.Notes = append(standupFlow.Notes, standupItem)
			standupFlow.NotesThreadEvents = append(standupFlow.NotesThreadEvents, event.ID)
			edited = true
		}
	}

	if edited {
		// Update the message preview.
		standupFlow.ReactableEvents = EditPreview(event.RoomID, event.Sender, standupFlow)

		if standupFlow.State == Sent && standupFlow.ResendEventId == nil {
			resp, err := SendMessage(event.RoomID, &mevent.MessageEventContent{
				MsgType:       mevent.MsgText,
				Body:          fmt.Sprintf("Send Edit (%s) or Cancel (%s)?", CHECKMARK, RED_X),
				Format:        mevent.FormatHTML,
				FormattedBody: fmt.Sprintf("Send Edit (%s) or Cancel (%s)?", CHECKMARK, RED_X),
			})
			if err != nil {
				log.Error("Failed to ask user if the want to send an edit")
				return
			}
			SendReaction(event.RoomID, resp.EventID, CHECKMARK)
			SendReaction(event.RoomID, resp.EventID, RED_X)
			standupFlow.ReactableEvents = append(standupFlow.ReactableEvents, resp.EventID)
			standupFlow.ResendEventId = &resp.EventID
		}
	}
}

func HandleMessage(_ mautrix.EventSource, event *mevent.Event) {
	userId := mid.UserID(configuration.Username)
	if event.Sender == userId {
		return
	}

	messageEventContent := event.Content.AsMessage()

	commandParts, err := getCommandParts(messageEventContent.Body)

	if err != nil {
		// This message is not a command.
		if stateStore.GetConfigRoomId(event.Sender) != event.RoomID {
			// Ignore non-command messages if not in config room.
			return
		}

		if val, found := currentStandupFlows[event.Sender]; found {
			// Mark the message as read after we've handled it.
			defer client.MarkRead(event.RoomID, event.ID)

			// Handle edits and thread replies
			relatesTo := messageEventContent.RelatesTo
			if relatesTo != nil {
				if relatesTo.Type == mevent.RelReplace {
					HandleEdit(event, val)
				} else if relatesTo.Type == mevent.RelReply || relatesTo.Type == "io.element.thread" || relatesTo.Type == "m.thread" {
					HandleReply(event, val)
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

	// Mark the message as read after we've handled it.
	defer client.MarkRead(event.RoomID, event.ID)

	stateStore.SetConfigRoom(event.Sender, event.RoomID)

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
	case "threads":
		HandleThreads(event.RoomID, event.Sender, commandParts[1:])
		break
	case "new":
		currentStandupFlows[event.Sender] = BlankStandupFlow()
		CreatePost(event.RoomID, event.Sender)
		break
	case "show":
		if currentFlow, found := currentStandupFlows[event.Sender]; found && currentFlow.State != FlowNotStarted {
			SendMessage(event.RoomID, FormatPost(event.Sender, currentFlow, true, false, false))
		} else {
			SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgText, Body: "No standup post to show."})
		}
		break
	case "edit":
		if useThreads, _ := stateStore.GetUseThreads(event.Sender); useThreads {
			SendMessage(event.RoomID, &mevent.MessageEventContent{
				MsgType: mevent.MsgNotice,
				Body:    fmt.Sprintf("You cannot use !edit when using threads. Just reply to the corresponding thread."),
			})
			return
		}
		if len(commandParts) != 2 {
			SendMessage(event.RoomID, &mevent.MessageEventContent{
				MsgType: mevent.MsgNotice,
				Body:    fmt.Sprintf("Invalid item to edit! Must be one of Friday, Weekend, Yesterday, Today, Blockers, or Notes"),
			})
			return
		}
		if currentFlow, found := currentStandupFlows[event.Sender]; !found || currentFlow.State == FlowNotStarted {
			SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "No standup post to edit."})
		}

		switch strings.ToLower(commandParts[1]) {
		case "friday":
			if stateStore.GetCurrentWeekdayInUserTimezone(event.Sender) != time.Monday {
				SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "It's not Monday, so you can't go back to edit Friday."})
				return
			}
			GoToStateAndNotify(event.RoomID, event.Sender, Friday)
			break
		case "weekend":
			if stateStore.GetCurrentWeekdayInUserTimezone(event.Sender) != time.Monday {
				SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "It's not Monday, so you can't go back to edit the weekend."})
				return
			}
			GoToStateAndNotify(event.RoomID, event.Sender, Weekend)
			break
		case "yesterday":
			if stateStore.GetCurrentWeekdayInUserTimezone(event.Sender) == time.Monday {
				SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "It's Monday, so you can't go back to edit yesterday. Edit Friday or Weekend instead."})
				return
			}
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
		}
		break
	case "undo":
		if val, found := currentStandupFlows[event.Sender]; !found || val.State != Sent {
			SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "No sent standup post to undo."})
		} else {
			sendRoomID, err := stateStore.GetSendRoomId(event.Sender)
			if err != nil {
				log.Debugf("No send room configured for %s, can't undo anything", event.Sender)
				SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "No send room configured. Can't undo anything."})
			}

			stateKey := strings.TrimPrefix(event.Sender.String(), "@")
			var previousPostEventContent PreviousPostEventContent
			err = client.StateEvent(event.RoomID, StatePreviousPost, stateKey, &previousPostEventContent)
			if err != nil {
				log.Debug("Couldn't find previous post info.")
				SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "No previous standup post to undo."})
			}
			_, err = client.RedactEvent(sendRoomID, previousPostEventContent.EditEventID)
			if err != nil {
				SendMessage(event.RoomID, &mevent.MessageEventContent{Body: "Failed to redact the standup post!"})
			} else {
				SendMessage(event.RoomID, &mevent.MessageEventContent{
					Body: fmt.Sprintf("Redacted standup post with ID: %s in %s", previousPostEventContent.EditEventID, event.RoomID),
				})
				currentStandupFlows[event.Sender].State = Confirm
				client.SendStateEvent(event.RoomID, StatePreviousPost, stateKey, struct{}{})
			}
		}
		break
	case "cancel":
		if val, found := currentStandupFlows[event.Sender]; !found || val.State == FlowNotStarted {
			SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "No standup post to cancel."})
		} else {
			currentStandupFlows[event.Sender] = BlankStandupFlow()
			SendMessage(event.RoomID, &mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "Standup post cancelled"})
		}
		break
	case "room":
		HandleRoom(event.RoomID, event, commandParts[1:])
		break
	default:
		SendHelp(event.RoomID)
		break
	}
}

func HandleRedaction(_ mautrix.EventSource, event *mevent.Event) {
	// Mark the redaction as read after we've handled it.
	defer client.MarkRead(event.RoomID, event.ID)

	// Handle redactions
	if val, found := currentStandupFlows[event.Sender]; found {
		removedItem := false
		for i, item := range val.Yesterday {
			if item.EventID == event.Redacts {
				removedItem = true
				val.Yesterday = append(val.Yesterday[:i], val.Yesterday[i+1:]...)
				break
			}
		}
		if !removedItem {
			for i, item := range val.Friday {
				if item.EventID == event.Redacts {
					removedItem = true
					val.Friday = append(val.Friday[:i], val.Friday[i+1:]...)
					break
				}
			}
		}
		if !removedItem {
			for i, item := range val.Weekend {
				if item.EventID == event.Redacts {
					removedItem = true
					val.Weekend = append(val.Weekend[:i], val.Weekend[i+1:]...)
					break
				}
			}
		}
		if !removedItem {
			for i, item := range val.Today {
				if item.EventID == event.Redacts {
					removedItem = true
					val.Today = append(val.Today[:i], val.Today[i+1:]...)
					break
				}
			}
		}
		if !removedItem {
			for i, item := range val.Blockers {
				if item.EventID == event.Redacts {
					removedItem = true
					val.Blockers = append(val.Blockers[:i], val.Blockers[i+1:]...)
					break
				}
			}
		}
		if !removedItem {
			for i, item := range val.Notes {
				if item.EventID == event.Redacts {
					removedItem = true
					val.Notes = append(val.Notes[:i], val.Notes[i+1:]...)
					break
				}
			}
		}

		if removedItem {
			if val.PreviewEventId.String() != "" {
				val.ReactableEvents = EditPreview(event.RoomID, event.Sender, val)
			}
		}
	}
}
