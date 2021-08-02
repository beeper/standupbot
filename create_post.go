package main

import (
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"
)

const CHECKMARK = "✅"
const RED_X = "❌"

var PreviousStandup = mevent.Type{"com.nevarro.standupbot.previous_standup", mevent.StateEventType}

type PreviousStandupEventContent struct {
	TzString string
}

func sendMessageWithCheckmarkReaction(roomID mid.RoomID, message mevent.MessageEventContent) (*mautrix.RespSendEvent, error) {
	resp, err := SendMessage(roomID, message)
	if err != nil {
		return nil, err
	}
	SendReaction(roomID, resp.EventID, CHECKMARK)
	return resp, nil
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

	resp, err := sendMessageWithCheckmarkReaction(roomID, mevent.MessageEventContent{
		MsgType:       mevent.MsgText,
		Body:          fmt.Sprintf("%s Enter one item per-line. React with ✅ when done.", question),
		Format:        mevent.FormatHTML,
		FormattedBody: fmt.Sprintf("%s <i>Enter one item per-line. React with ✅ when done.</i>", question),
	})
	if err != nil {
		log.Errorf("Failed to send notice asking '%s'!", question)
		return
	}
	if _, found := currentStandupFlows[userID]; !found {
		currentStandupFlows[userID] = BlankStandupFlow()
	}
	currentStandupFlows[userID].State = state
	currentStandupFlows[userID].ReactableEvents = append(currentStandupFlows[userID].ReactableEvents, resp.EventID)
}

func CreatePost(roomID mid.RoomID, userID mid.UserID) {
	stateKey := strings.TrimPrefix(userID.String(), "@")
	var previousStandupEventContent PreviousStandupEventContent
	err := client.StateEvent(roomID, PreviousStandup, stateKey, &previousStandupEventContent)
	if err != nil {
		log.Debug("Couldn't find previous standup info.")
	}

	nextState := Yesterday
	if stateStore.GetCurrentWeekdayInUserTimezone(userID) == time.Monday {
		nextState = Friday
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

func FormatPost(userID mid.UserID, standupFlow *StandupFlow, preview bool, sendConfirmation bool) mevent.MessageEventContent {
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
		postText = fmt.Sprintf("Standup post preview:\n\n" + postText)
		postHtml = fmt.Sprintf("<i>Standup post preview:</i><br><br>" + postHtml)
	}
	if sendConfirmation {
		postText = fmt.Sprintf("%s\n\n---\nSend (%s) or Cancel (%s)?", postText, CHECKMARK, RED_X)
		postHtml = fmt.Sprintf("%s<hr><b>Send (%s) or Cancel (%s)?</b>", postHtml, CHECKMARK, RED_X)
	}

	return mevent.MessageEventContent{
		MsgType:       mevent.MsgText,
		Body:          postText,
		Format:        mevent.FormatHTML,
		FormattedBody: postHtml,
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

	if reactionEventContent.RelatesTo.Key == CHECKMARK {
		currentFlow.ReactableEvents = make([]mid.EventID, 0)

		if currentFlow.PreviewEventId.String() != "" && currentFlow.State != Confirm {
			// this means we have already gone through the flow, and we went back to edit.
			client.RedactEvent(event.RoomID, currentFlow.PreviewEventId)
			currentFlow.State = Notes
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
			resp, err := SendMessage(event.RoomID, FormatPost(event.Sender, currentFlow, true, true))
			if err == nil {
				SendReaction(event.RoomID, resp.EventID, CHECKMARK)
				SendReaction(event.RoomID, resp.EventID, RED_X)
			}
			currentFlow.State = Confirm
			currentFlow.PreviewEventId = resp.EventID
			currentStandupFlows[event.Sender].ReactableEvents =
				append(currentStandupFlows[event.Sender].ReactableEvents, resp.EventID)
			return
		case Confirm:
			sendRoomID := stateStore.GetSendRoomId(event.Sender)
			if sendRoomID.String() == "" {
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

			_, err := SendMessage(sendRoomID, FormatPost(event.Sender, currentFlow, false, false))
			if err != nil {
				SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgText, Body: "Failed to send standup post to " + sendRoomID.String()})
			} else {
				SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgText, Body: "Sent standup post to " + sendRoomID.String()})
				currentFlow.State = FlowNotStarted
			}
			return
		}
	} else if reactionEventContent.RelatesTo.Key == RED_X {
		if currentFlow.State == Confirm {
			currentFlow = BlankStandupFlow()
			SendMessage(event.RoomID, mevent.MessageEventContent{MsgType: mevent.MsgText, Body: "Standup post cancelled"})
		}
	}
}
