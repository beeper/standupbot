package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kyoh86/xdg"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mcrypto "maunium.net/go/mautrix/crypto"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"

	"git.sr.ht/~sumner/standupbot/store"
	"git.sr.ht/~sumner/standupbot/types"
)

var client *mautrix.Client
var configuration Configuration
var olmMachine *mcrypto.OlmMachine
var stateStore *store.StateStore

var VERSION = "0.4.0"

func main() {
	// Arg parsing
	configPath := flag.String("config", "./config.json", "config file location")
	logLevelStr := flag.String("loglevel", "debug", "the log level")
	logFilename := flag.String("logfile", "", "the log file to use (defaults to '' meaning no log file)")
	flag.Parse()

	// Configure logging
	if *logFilename != "" {
		logFile, err := os.OpenFile(*logFilename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err == nil {
			mw := io.MultiWriter(os.Stdout, logFile)
			log.SetOutput(mw)
		} else {
			log.Errorf("Failed to open logging file; using default stderr: %s", err)
		}
	}
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.DebugLevel)
	logLevel, err := log.ParseLevel(*logLevelStr)
	if err == nil {
		log.SetLevel(logLevel)
	} else {
		log.Errorf("Invalid loglevel '%s'. Using default 'debug'.", logLevel)
	}

	log.Info("standupbot starting...")

	// Load configuration
	log.Infof("reading config from %s...", *configPath)
	configJson, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Could not read config from %s: %s", *configPath, err)
	}

	err = json.Unmarshal(configJson, &configuration)
	username := mid.UserID(configuration.Username)

	// Open the config database
	db, err := sql.Open("sqlite3", xdg.DataHome()+"/standupbot/standupbot.db")
	if err != nil {
		log.Fatal("Could not open standupbot database.")
	}

	currentStandupFlowsJson, err := os.ReadFile(xdg.DataHome() + "/standupbot/current-flows.json")
	if err != nil {
		log.Warn("Couldn't open the current-flows JSON.")
	} else {
		err = json.Unmarshal(currentStandupFlowsJson, &currentStandupFlows)
		if err != nil {
			log.Warnf("Failed to unmarshal the current flows JSON: %+v", err)
		} else {
			log.Info("Loaded current flows from disk.")
		}
	}

	// Make sure to exit cleanly
	c := make(chan os.Signal, 1)
	signal.Notify(c,
		os.Interrupt,
		os.Kill,
		syscall.SIGABRT,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
	)
	go func() {
		for range c { // when the process is killed
			log.Info("Cleaning up")
			db.Close()
			bytes, err := json.Marshal(currentStandupFlows)
			if err != nil {
				log.Error("Failed to serialize current standup flows!")
			} else {
				currentStandupFlowsFile, err := os.OpenFile(xdg.DataHome()+"/standupbot/current-flows.json", os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					log.Error("Failed to open current standup flows JSON file!")
				} else {
					_, err = currentStandupFlowsFile.Write(bytes)
					if err != nil {
						log.Error("Failed to write current standup flows JSON to file!")
					} else {
						log.Info("Saved current flows to disk.")
					}
				}
				currentStandupFlowsFile.Close()
			}
			os.Exit(0)
		}
	}()

	stateStore = store.NewStateStore(db)
	if err := stateStore.CreateTables(); err != nil {
		log.Fatal("Failed to create the tables for standupbot.", err)
	}

	// login to homeserver
	log.Info("Using username/password auth")
	password, err := configuration.GetPassword()
	if err != nil {
		log.Fatalf("Could not read password from %s", configuration.PasswordFile)
	}
	deviceID := FindDeviceID(db, username.String())
	if len(deviceID) > 0 {
		log.Info("Found existing device ID in database:", deviceID)
	}
	client, err = mautrix.NewClient(configuration.Homeserver, "", "")
	if err != nil {
		panic(err)
	}
	_, err = DoRetry("login", func() (interface{}, error) {
		return client.Login(&mautrix.ReqLogin{
			Type: mautrix.AuthTypePassword,
			Identifier: mautrix.UserIdentifier{
				Type: mautrix.IdentifierTypeUser,
				User: username.String(),
			},
			Password:                 password,
			InitialDeviceDisplayName: "standupbot",
			DeviceID:                 deviceID,
			StoreCredentials:         true,
		})
	})
	if err != nil {
		log.Fatalf("Couldn't login to the homeserver.")
	}
	log.Infof("Logged in as %s/%s", client.UserID, client.DeviceID)

	// set the client store on the client.
	client.Store = stateStore
	stateStore.Client = client

	// Load state from all of the rooms that we are joined to in case the
	// database died.
	log.Info("Loading state from joined rooms...")
	joinedRooms, err := client.JoinedRooms()
	if err == nil {
		for _, roomID := range joinedRooms.JoinedRooms {
			members, err := client.Members(roomID)
			potentialUsers := make([]mid.UserID, 0)
			if err != nil {
				continue
			}
			for _, membershipEvent := range members.Chunk {
				potentialUsers = append(potentialUsers, membershipEvent.Sender)
			}

			for _, userID := range potentialUsers {
				stateKey := strings.TrimPrefix(userID.String(), "@")

				var tzSettingEventContent types.TzSettingEventContent
				if err := client.StateEvent(roomID, types.StateTzSetting, stateKey, &tzSettingEventContent); err == nil {
					if location, err := time.LoadLocation(tzSettingEventContent.TzString); err == nil {
						log.Infof("Loaded timezone (%s) for %s from state", location, userID)
						stateStore.SetConfigRoom(userID, roomID)
						stateStore.SetTimezone(userID, location.String())
					}
				}

				var notifyEventContent types.NotifyEventContent
				if err := client.StateEvent(roomID, types.StateNotify, stateKey, &notifyEventContent); err == nil {
					log.Infof("Loaded notification minutes after midnight (%d) for %s from state", notifyEventContent.MinutesAfterMidnight, userID)
					stateStore.SetConfigRoom(userID, roomID)
					stateStore.SetNotify(userID, notifyEventContent.MinutesAfterMidnight)
				}

				var sendRoomEventContent types.SendRoomEventContent
				if err := client.StateEvent(roomID, types.StateSendRoom, stateKey, &sendRoomEventContent); err == nil {
					log.Infof("Loaded send room (%s) for %s from state", sendRoomEventContent.SendRoomID, userID)
					stateStore.SetConfigRoom(userID, roomID)
					stateStore.SetSendRoomId(userID, sendRoomEventContent.SendRoomID)
				}

				var useThreadsEventContent types.UseThreadsEventContent
				if err := client.StateEvent(roomID, types.StateUseThreads, stateKey, &useThreadsEventContent); err == nil {
					log.Infof("Loaded thread usage setting (%t) for %s from state", useThreadsEventContent.UseThreads, userID)
					stateStore.SetConfigRoom(userID, roomID)
					stateStore.SetUseThreads(userID, useThreadsEventContent.UseThreads)
				}
			}
		}
	}
	log.Info("Finished loading state from joined rooms")

	// Setup the crypto store
	sqlCryptoStore := mcrypto.NewSQLCryptoStore(
		db,
		"sqlite3",
		username.String(),
		client.DeviceID,
		[]byte("standupbot_cryptostore_key"),
		CryptoLogger{},
	)
	err = sqlCryptoStore.CreateTables()
	if err != nil {
		log.Fatal("Could not create tables for the SQL crypto store.")
	}

	olmMachine = mcrypto.NewOlmMachine(client, &CryptoLogger{}, sqlCryptoStore, stateStore)
	err = olmMachine.Load()
	if err != nil {
		log.Errorf("Could not initialize encryption support. Encrypted rooms will not work.")
	}

	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	// Hook up the OlmMachine into the Matrix client so it receives e2ee
	// keys and other such things.
	syncer.OnSync(func(resp *mautrix.RespSync, since string) bool {
		olmMachine.ProcessSyncResponse(resp, since)
		return true
	})

	syncer.OnEventType(mevent.StateMember, func(_ mautrix.EventSource, event *mevent.Event) {
		olmMachine.HandleMemberEvent(event)
		stateStore.SetMembership(event)

		if event.GetStateKey() == username.String() && event.Content.AsMember().Membership == mevent.MembershipInvite {
			log.Info("Joining ", event.RoomID)
			_, err := DoRetry("join room", func() (interface{}, error) {
				return client.JoinRoomByID(event.RoomID)
			})
			if err != nil {
				log.Errorf("Could not join channel %s. Error %+v", event.RoomID.String(), err)
			} else {
				log.Infof("Joined %s sucessfully", event.RoomID.String())
			}
		} else if event.GetStateKey() == username.String() && event.Content.AsMember().Membership.IsLeaveOrBan() {
			log.Infof("Left or banned from %s", event.RoomID)
		}
	})

	syncer.OnEventType(mevent.StateEncryption, func(_ mautrix.EventSource, event *mevent.Event) {
		stateStore.SetEncryptionEvent(event)
	})

	syncer.OnEventType(mevent.EventReaction, func(source mautrix.EventSource, event *mevent.Event) { go HandleReaction(source, event) })

	syncer.OnEventType(mevent.EventMessage, func(source mautrix.EventSource, event *mevent.Event) { go HandleMessage(source, event) })

	syncer.OnEventType(mevent.EventRedaction, func(source mautrix.EventSource, event *mevent.Event) { go HandleRedaction(source, event) })

	syncer.OnEventType(mevent.EventEncrypted, func(source mautrix.EventSource, event *mevent.Event) {
		decryptedEvent, err := olmMachine.DecryptMegolmEvent(event)
		if err != nil {
			log.Errorf("Failed to decrypt message from %s in %s: %+v", event.Sender, event.RoomID, err)
		} else {
			log.Debugf("Received encrypted event from %s in %s", event.Sender, event.RoomID)
			if decryptedEvent.Type == mevent.EventMessage {
				go HandleMessage(source, decryptedEvent)
			} else if decryptedEvent.Type == mevent.EventReaction {
				go HandleReaction(source, decryptedEvent)
			} else if decryptedEvent.Type == mevent.EventRedaction {
				go HandleRedaction(source, decryptedEvent)
			}
		}
	})

	// Notification loop
	go func() {
		log.Debugf("Starting notification loop")
		for {
			h, m, _ := time.Now().UTC().Clock()
			currentMinutesAfterMidnight := h*60 + m
			usersForCurrentMinute := stateStore.GetNotifyUsersForMinutesAfterUtcForToday()[currentMinutesAfterMidnight]

			for userID, roomID := range usersForCurrentMinute {
				log.Infof("Notifying %s", userID)
				if currentFlow, found := currentStandupFlows[userID]; !found || currentFlow.State == FlowNotStarted || currentFlow.State == Sent {
					SendMessage(roomID, mevent.MessageEventContent{
						MsgType: mevent.MsgText,
						Body:    "Time to write your standup post!",
					})
					currentStandupFlows[userID] = BlankStandupFlow()
					go CreatePost(roomID, userID)
				} else {
					SendMessage(roomID, mevent.MessageEventContent{
						MsgType:       mevent.MsgText,
						Body:          "Looks like you are already writing a standup post! If you want to start over, type `!standupbot new`",
						Format:        mevent.FormatHTML,
						FormattedBody: "Looks like you are already writing a standup post! If you want to start over, type <code>!standupbot new</code>",
					})
				}
			}

			// Sleep until the next minute comes around
			time.Sleep(time.Duration(60-time.Now().Second()) * time.Second)
		}
	}()

	for {
		log.Debugf("Running sync...")
		err = client.Sync()
		if err != nil {
			log.Errorf("Sync failed. %+v", err)
		}
	}
}

func FindDeviceID(db *sql.DB, accountID string) (deviceID mid.DeviceID) {
	err := db.QueryRow("SELECT device_id FROM crypto_account WHERE account_id=$1", accountID).Scan(&deviceID)
	if err != nil && err != sql.ErrNoRows {
		log.Warnf("Failed to scan device ID: %v", err)
	}
	return
}
