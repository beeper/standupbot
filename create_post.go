package main

import (
	"strings"

	log "github.com/sirupsen/logrus"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"
)

var PreviousStandup = mevent.Type{"com.nevarro.standupbot.previous_standup", mevent.StateEventType}

type PreviousStandupEventContent struct {
	TzString string
}

func CreatePost(roomID mid.RoomID, userID mid.UserID) {
	stateKey := strings.TrimPrefix(userID.String(), "@")
	var previousStandupEventContent PreviousStandupEventContent
	err := client.StateEvent(roomID, PreviousStandup, stateKey, &previousStandupEventContent)
	if err != nil {
		log.Debug("Couldn't find previous standup info.")
	}

	resp, err := SendMessage(roomID, mevent.MessageEventContent{
		MsgType:       mevent.MsgText,
		Body:          "What did you do yesterday? Enter one item per-line. React with ✅ when done.",
		Format:        mevent.FormatHTML,
		FormattedBody: "What did you do yesterday? <i>Enter one item per-line. React with ✅ when done.</i>",
	})
	if err != nil {
		log.Error("Failed to send notice for asking what they did yesterday!")
		return
	}
	SendReaction(roomID, resp.EventID, "✅")
	currentStandupFlows[userID].State = Yesterday
}
