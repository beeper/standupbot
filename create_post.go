package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"
)

const CHECKMARK = "✅"
const RED_X = "❌"

// Previous Post
var StatePreviousPost = mevent.Type{Type: "com.nevarro.standupbot.previous_post", Class: mevent.StateEventType}

type PreviousPostEventContent struct {
	EditEventID mid.EventID
	FlowID      uuid.UUID
	Day         time.Weekday
	TodayItems  []StandupItem
}

func sendMessageWithCheckmarkReaction(roomID mid.RoomID, message mevent.MessageEventContent) (*mautrix.RespSendEvent, error) {
	resp, err := SendMessage(roomID, message)
	if err != nil {
		return nil, err
	}
	SendReaction(roomID, resp.EventID, CHECKMARK)
	return resp, nil
}

func sendThreadRootMessage(roomID mid.RoomID, header string) (*mautrix.RespSendEvent, error) {
	return SendMessage(roomID, mevent.MessageEventContent{
		MsgType:       mevent.MsgText,
		Body:          fmt.Sprintf("**%s** (thread)", header),
		Format:        mevent.FormatHTML,
		FormattedBody: fmt.Sprintf("<b>%s</b> <i>(thread)</i>", header),
	})
}

func GoToStateAndNotify(roomID mid.RoomID, userID mid.UserID, state StandupFlowState) {
	var question string
	switch state {
	case Friday:
		question = "What did you do Friday?"
		break
	case Weekend:
		question = "What did you do over the weekend?"
		break
	case Yesterday:
		question = "What did you do yesterday?"
		break
	case Today:
		question = "What are you planning to do today?"
		break
	case Blockers:
		question = "Do you have any blockers?"
		break
	case Notes:
		question = "Do you have any other notes?"
		break
	}

	var resp *mautrix.RespSendEvent
	var err error
	if state == Threads || state == ThreadsFriday {
		resp, err = SendMessage(roomID, mevent.MessageEventContent{
			MsgType:       mevent.MsgText,
			Body:          "**Fill out the standup post by replying in each thread.** *Enter one item per message.*",
			Format:        mevent.FormatHTML,
			FormattedBody: "<b>Fill out the standup post by replying in each thread.</b> <i>Enter one item per message.</i>",
		})
	} else {
		resp, err = sendMessageWithCheckmarkReaction(roomID, mevent.MessageEventContent{
			MsgType:       mevent.MsgText,
			Body:          fmt.Sprintf("%s *Enter one item per message. React with ✅ when done.*", question),
			Format:        mevent.FormatHTML,
			FormattedBody: fmt.Sprintf("%s <i>Enter one item per message. React with ✅ when done.</i>", question),
		})
	}
	if err != nil {
		log.Errorf("Failed to send notice asking '%s'!", question)
		return
	}

	if _, found := currentStandupFlows[userID]; !found {
		currentStandupFlows[userID] = BlankStandupFlow()
	}
	currentStandupFlows[userID].State = state
	currentStandupFlows[userID].ReactableEvents = append(currentStandupFlows[userID].ReactableEvents, resp.EventID)

	if state == Threads || state == ThreadsFriday {
		if state == ThreadsFriday {
			resp, err := sendThreadRootMessage(roomID, "Friday")
			if err != nil {
				log.Error("Unable to send thread root for Friday")
				return
			}
			currentStandupFlows[userID].FridayThreadEvents = []mid.EventID{resp.EventID}

			resp, err = sendThreadRootMessage(roomID, "Weekend")
			if err != nil {
				log.Error("Unable to send thread root for Weekend")
				return
			}
			currentStandupFlows[userID].WeekendThreadEvents = []mid.EventID{resp.EventID}
		} else {
			resp, err = sendThreadRootMessage(roomID, "Yesterday")
			if err != nil {
				log.Error("Unable to send thread root for Yesterday")
				return
			}
			currentStandupFlows[userID].YesterdayThreadEvents = []mid.EventID{resp.EventID}
		}

		resp, err = sendThreadRootMessage(roomID, "Today")
		if err != nil {
			log.Error("Unable to send thread root for Today")
			return
		}
		currentStandupFlows[userID].TodayThreadEvents = []mid.EventID{resp.EventID}

		resp, err = sendThreadRootMessage(roomID, "Blockers")
		if err != nil {
			log.Error("Unable to send thread root for Blockers")
			return
		}
		currentStandupFlows[userID].BlockersThreadEvents = []mid.EventID{resp.EventID}

		resp, err = sendThreadRootMessage(roomID, "Notes")
		if err != nil {
			log.Error("Unable to send thread root for Notes")
			return
		}
		currentStandupFlows[userID].NotesThreadEvents = []mid.EventID{resp.EventID}

		// Show the preview
		ShowMessagePreview(roomID, userID, currentStandupFlows[userID], false)
	}
}

func CreatePost(roomID mid.RoomID, userID mid.UserID) {
	stateKey := strings.TrimPrefix(userID.String(), "@")
	var previousPostEventContent PreviousPostEventContent
	err := client.StateEvent(roomID, StatePreviousPost, stateKey, &previousPostEventContent)
	if err != nil {
		log.Debug("Couldn't find previous post info.")
	} else {
		log.Debug("Found previous post info ", previousPostEventContent)
	}

	useThreads, _ := stateStore.GetUseThreads(userID)
	var nextState StandupFlowState

	if useThreads {
		nextState = Threads
		if stateStore.GetCurrentWeekdayInUserTimezone(userID) == time.Monday {
			nextState = ThreadsFriday
		}
	} else {
		nextState = Yesterday
		if stateStore.GetCurrentWeekdayInUserTimezone(userID) == time.Monday {
			nextState = Friday
		}
	}

	GoToStateAndNotify(roomID, userID, nextState)
}

func formatList(items []StandupItem) (string, string) {
	plain := make([]string, 0)
	html := make([]string, 0)
	for _, item := range items {
		plain = append(plain, fmt.Sprintf("- %s", item.Body))
		if item.FormattedBody == "" {
			item.FormattedBody = item.Body
		}
		html = append(html, fmt.Sprintf("<li>%s</li>", item.FormattedBody))
	}

	return strings.Join(plain, "\n"), strings.Join(html, "")
}

func FormatPost(userID mid.UserID, standupFlow *StandupFlow, preview bool, sendConfirmation bool, isEditOfExisting bool) mevent.MessageEventContent {
	postText := fmt.Sprintf(`%s's standup post:\n\n`, userID)
	postHtml := fmt.Sprintf(`<a href="https://matrix.to/#/%s">%s</a>'s standup post:<br><br>`, userID, userID)

	if len(standupFlow.Yesterday) > 0 {
		plain, html := formatList(standupFlow.Yesterday)
		postText += "**Yesterday**\n" + plain
		postHtml += "<b>Yesterday</b><br><ul>" + html + "</ul>"
	}
	if len(standupFlow.Friday) > 0 {
		plain, html := formatList(standupFlow.Friday)
		postText += "\n**Friday**\n" + plain
		postHtml += "<b>Friday</b><br><ul>" + html + "</ul>"
	}
	if len(standupFlow.Weekend) > 0 {
		plain, html := formatList(standupFlow.Weekend)
		postText += "\n**Weekend**\n" + plain
		postHtml += "<b>Weekend</b><br><ul>" + html + "</ul>"
	}
	if len(standupFlow.Today) > 0 {
		plain, html := formatList(standupFlow.Today)
		postText += "\n**Today**\n" + plain
		postHtml += "<b>Today</b><br><ul>" + html + "</ul>"
	}
	if len(standupFlow.Blockers) > 0 {
		plain, html := formatList(standupFlow.Blockers)
		postText += "\n**Blockers**\n" + plain
		postHtml += "<b>Blockers</b><br><ul>" + html + "</ul>"
	}
	if len(standupFlow.Notes) > 0 {
		plain, html := formatList(standupFlow.Notes)
		postText += "\n**Notes**\n" + plain
		postHtml += "<b>Notes</b><br><ul>" + html + "</ul>"
	}

	if preview {
		postText = fmt.Sprintf("Standup post preview:\n----------------------------------------\n" + postText)
		postHtml = fmt.Sprintf("<i>Standup post preview:</i><hr>" + postHtml)
	}
	if sendConfirmation {
		if isEditOfExisting {
			postText = fmt.Sprintf("%s\n----------------------------------------\nSend Edit (%s) or Cancel (%s)?", postText, CHECKMARK, RED_X)
			postHtml = fmt.Sprintf("%s<hr><b>Send Edit (%s) or Cancel (%s)?</b>", postHtml, CHECKMARK, RED_X)
		} else {
			postText = fmt.Sprintf("%s\n----------------------------------------\nSend (%s) or Cancel (%s)?", postText, CHECKMARK, RED_X)
			postHtml = fmt.Sprintf("%s<hr><b>Send (%s) or Cancel (%s)?</b>", postHtml, CHECKMARK, RED_X)
		}
	}

	return mevent.MessageEventContent{
		MsgType:       mevent.MsgText,
		Body:          postText,
		Format:        mevent.FormatHTML,
		FormattedBody: postHtml,
	}
}

func ShowMessagePreview(roomID mid.RoomID, userID mid.UserID, currentFlow *StandupFlow, isEditOfExisting bool) {
	resp, err := SendMessage(roomID, FormatPost(userID, currentFlow, true, true, isEditOfExisting))
	if err == nil {
		SendReaction(roomID, resp.EventID, CHECKMARK)
		SendReaction(roomID, resp.EventID, RED_X)
	}
	currentFlow.PreviewEventId = resp.EventID
	currentStandupFlows[userID].ReactableEvents = append(currentStandupFlows[userID].ReactableEvents, resp.EventID)
}

func SendMessageToSendRoom(event *mevent.Event, currentFlow *StandupFlow, editEventID *mid.EventID) {
	sendRoomID, err := stateStore.GetSendRoomId(event.Sender)
	if err != nil {
		SendMessage(event.RoomID, mevent.MessageEventContent{
			MsgType:       mevent.MsgText,
			Body:          "No send room set! Set one using `!standupbot room [room ID or alias]`",
			Format:        mevent.FormatHTML,
			FormattedBody: "No send room set! Set one using <code>!standupbot room [room ID or alias]</code>",
		})
		return
	}

	found := false
	for _, userID := range stateStore.GetRoomMembers(sendRoomID) {
		if event.Sender == userID {
			found = true
		}
	}
	if !found {
		SendMessage(event.RoomID, mevent.MessageEventContent{
			MsgType:       mevent.MsgText,
			Body:          "You are not a member of the configured send room! Refusing to send a message to the room. Set a new one using `!standupbot room [room ID or alias]`.",
			Format:        mevent.FormatHTML,
			FormattedBody: "<b>You are not a member of the configured send room!</b> Refusing to send a message to the room. Set a new one using <code>!standupbot room [room ID or alias]</code>.",
		})
		return
	}

	newPost := FormatPost(event.Sender, currentFlow, false, false, false)
	var futureEditId mid.EventID
	var sent *mautrix.RespSendEvent
	editStr := ""
	if editEventID != nil {
		_, err = SendMessage(sendRoomID, mevent.MessageEventContent{
			MsgType:       mevent.MsgText,
			Body:          " * " + newPost.Body,
			Format:        mevent.FormatHTML,
			FormattedBody: " * " + newPost.FormattedBody,
			RelatesTo: &mevent.RelatesTo{
				Type:    mevent.RelReplace,
				EventID: *editEventID,
			},
			NewContent: &newPost,
		})
		editStr = " edit"
		futureEditId = *editEventID
	} else {
		sent, err = SendMessage(sendRoomID, newPost)
		if err != nil {
			futureEditId = sent.EventID
		}
	}

	if err != nil {
		SendMessage(event.RoomID, mevent.MessageEventContent{
			MsgType: mevent.MsgText,
			Body:    "Failed to send standup post" + editStr + " to " + sendRoomID.String(),
		})
	} else {
		SendMessage(event.RoomID, mevent.MessageEventContent{
			MsgType: mevent.MsgText,
			Body:    "Sent standup post" + editStr + " to " + sendRoomID.String(),
		})
		currentFlow.State = Sent
		stateKey := strings.TrimPrefix(event.Sender.String(), "@")
		_, err = client.SendStateEvent(event.RoomID, StatePreviousPost, stateKey, PreviousPostEventContent{
			EditEventID: futureEditId,
			FlowID:      currentFlow.FlowID,
			Day:         stateStore.GetCurrentWeekdayInUserTimezone(event.Sender),
			TodayItems:  currentFlow.Today,
		})
	}
}

func HandleReaction(_ mautrix.EventSource, event *mevent.Event) {
	reactionEventContent := event.Content.AsReaction()
	currentFlow, found := currentStandupFlows[event.Sender]
	if !found || currentFlow.State == FlowNotStarted {
		return
	}
	found = false
	for _, eventId := range currentFlow.ReactableEvents {
		if reactionEventContent.RelatesTo.EventID == eventId {
			found = true
			break
		}
	}

	if !found {
		return
	}

	// Mark the reaction as read after we've handled it.
	defer client.MarkRead(event.RoomID, event.ID)

	if reactionEventContent.RelatesTo.Key == CHECKMARK {
		currentFlow.ReactableEvents = make([]mid.EventID, 0)

		stateKey := strings.TrimPrefix(event.Sender.String(), "@")
		var previousPostEventContent PreviousPostEventContent
		stateEventErr := client.StateEvent(event.RoomID, StatePreviousPost, stateKey, &previousPostEventContent)

		if stateEventErr == nil && currentFlow.FlowID == previousPostEventContent.FlowID {
			if currentFlow.State != Sent {
				// this means that we have already gone through the flow, sent the message, then went back to edit.
				client.RedactEvent(event.RoomID, currentFlow.PreviewEventId)
				ShowMessagePreview(event.RoomID, event.Sender, currentFlow, false)
				currentFlow.State = Sent
				return
			}
		} else if currentFlow.PreviewEventId.String() != "" {
			if currentFlow.State != Confirm && currentFlow.State != Sent && currentFlow.State != Threads && currentFlow.State != ThreadsFriday {
				// this means we have already gone through the flow, and we went back to edit.
				client.RedactEvent(event.RoomID, currentFlow.PreviewEventId)
				currentFlow.State = Notes
			}
		}

		switch currentFlow.State {
		case Yesterday:
			GoToStateAndNotify(event.RoomID, event.Sender, Today)
			break
		case Friday:
			GoToStateAndNotify(event.RoomID, event.Sender, Weekend)
			break
		case Weekend:
			GoToStateAndNotify(event.RoomID, event.Sender, Today)
			break
		case Today:
			GoToStateAndNotify(event.RoomID, event.Sender, Blockers)
			break
		case Blockers:
			GoToStateAndNotify(event.RoomID, event.Sender, Notes)
			break
		case Notes:
			ShowMessagePreview(event.RoomID, event.Sender, currentFlow, false)
			currentFlow.State = Confirm
			return
		case Threads, ThreadsFriday, Confirm:
			SendMessageToSendRoom(event, currentFlow, nil)
			return
		case Sent:
			if stateEventErr != nil {
				SendMessage(event.RoomID, mevent.MessageEventContent{
					MsgType: mevent.MsgText,
					Body:    "No previous post info found!",
				})
				currentFlow = BlankStandupFlow()
				return
			}
			SendMessageToSendRoom(event, currentFlow, &previousPostEventContent.EditEventID)
			return
		}
	} else if reactionEventContent.RelatesTo.Key == RED_X {
		if currentFlow.State == Confirm || currentFlow.State == Sent {
			currentStandupFlows[event.Sender] = BlankStandupFlow()
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgNotice, Body: "Standup post cancelled"})
		}
	}
}
