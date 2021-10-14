package main

import (
	"errors"
	"fmt"
	_ "strconv"
	"time"

	"github.com/sethvargo/go-retry"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mcrypto "maunium.net/go/mautrix/crypto"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"
)

func DoRetry(description string, fn func() (interface{}, error)) (interface{}, error) {
	var err error
	b, err := retry.NewFibonacci(1 * time.Second)
	if err != nil {
		panic(err)
	}
	b = retry.WithMaxRetries(5, b)
	for {
		log.Info("trying: ", description)
		var val interface{}
		val, err = fn()
		if err == nil {
			log.Info(description, " succeeded")
			return val, nil
		}
		nextDuration, stop := b.Next()
		log.Debugf("  %s failed. Retrying in %f seconds...", description, nextDuration.Seconds())
		if stop {
			log.Debugf("  %s failed. Retry limit reached. Will not retry.", description)
			err = errors.New("%s failed. Retry limit reached. Will not retry.")
			break
		}
		time.Sleep(nextDuration)
	}
	return nil, err
}

func SendMessage(roomId mid.RoomID, content *mevent.MessageEventContent) (resp *mautrix.RespSendEvent, err error) {
	return SendMessageOnBehalfOf(nil, roomId, content)
}

func SendMessageOnBehalfOf(user *mid.UserID, roomId mid.RoomID, content *mevent.MessageEventContent) (resp *mautrix.RespSendEvent, err error) {
	eventContent := &mevent.Content{Parsed: content}
	if user != nil {
		eventContent.Raw = map[string]interface{}{
			"space.nevarro.standupbot.on_behalf_of": *user,
		}
	}

	r, err := DoRetry(fmt.Sprintf("send message to %s on behalf of %s", roomId, user), func() (interface{}, error) {
		if stateStore.IsEncrypted(roomId) {
			log.Debugf("Sending encrypted event to %s", roomId)
			encrypted, err := olmMachine.EncryptMegolmEvent(roomId, mevent.EventMessage, eventContent)

			// These three errors mean we have to make a new Megolm session
			if err == mcrypto.SessionExpired || err == mcrypto.SessionNotShared || err == mcrypto.NoGroupSession {
				err = olmMachine.ShareGroupSession(roomId, stateStore.GetRoomMembers(roomId))
				if err != nil {
					log.Errorf("Failed to share group session to %s: %s", roomId, err)
					return nil, err
				}

				encrypted, err = olmMachine.EncryptMegolmEvent(roomId, mevent.EventMessage, eventContent)
			}

			if err != nil {
				log.Errorf("Failed to encrypt message to %s: %s", roomId, err)
				return nil, err
			}

			encrypted.RelatesTo = content.RelatesTo // The m.relates_to field should be unencrypted, so copy it.
			return client.SendMessageEvent(roomId, mevent.EventEncrypted, encrypted)
		} else {
			log.Debugf("Sending unencrypted event to %s", roomId)
			return client.SendMessageEvent(roomId, mevent.EventMessage, eventContent)
		}
	})
	if err != nil {
		// give up
		log.Errorf("Failed to send message to %s: %s", roomId, err)
		return nil, err
	}
	return r.(*mautrix.RespSendEvent), err
}
