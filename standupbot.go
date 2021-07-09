package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"os"

	"github.com/kyoh86/xdg"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mid "maunium.net/go/mautrix/id"

	"git.sr.ht/~sumner/standupbot/store"
)

func main() {
	// Arg parsing
	configPath := flag.String("config", xdg.ConfigHome()+"/standupbot/config.json", "config file location")
	logLevelStr := flag.String("loglevel", "debug", "the log level")
	flag.Parse()

	// Configure logging
	os.MkdirAll(xdg.DataHome()+"/standupbot", 0700)
	logFile, err := os.OpenFile(xdg.DataHome()+"/standupbot/standupbot.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		mw := io.MultiWriter(os.Stdout, logFile)
		log.SetOutput(mw)
	} else {
		log.Errorf("failed to open logging file; using default stderr: %s", err)
	}
	log.SetLevel(log.DebugLevel)
	logLevel, err := log.ParseLevel(*logLevelStr)
	if err == nil {
		log.SetLevel(logLevel)
	} else {
		log.Errorf("invalid loglevel %s. Using default 'debug'.", logLevel)
	}

	log.Info("standupbot starting...")

	// Load configuration
	log.Infof("reading config from %s...", *configPath)
	configJson, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Could not read config from %s: %s", configPath, err)
	}

	var configuration Configuration
	err = json.Unmarshal(configJson, &configuration)
	username := mid.UserID(configuration.Username)

	// Open the config database
	db, err := sql.Open("sqlite3", xdg.DataHome()+"/standupbot/standupbot.db")
	if err != nil {
		log.Fatal("Could not open standupbot database.")
	}
	stateStore := store.NewStateStore(db)
	if err := stateStore.CreateTables(); err != nil {
		log.Fatal("Failed to create the tables for standupbot")
	}

	// login to homeserver
	var client *mautrix.Client
	if access_token, err := stateStore.GetAccessToken(); err == nil && access_token != "" {
		log.Infof("Got access token: %s", access_token)
		client, err = mautrix.NewClient(configuration.Homeserver, username, access_token)
		if err != nil {
			log.Fatalf("Couldn't login to the homeserver.")
		}
	} else {
		log.Info("Using username/password auth")
		// Use password authentication if we didn't have an access
		// token yet.
		password, err := configuration.GetPassword()
		if err != nil {
			log.Fatalf("Could not read password from %s", configuration.PasswordFile)
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
				StoreCredentials:         true,
			})
		})
		if err != nil {
			log.Fatalf("Couldn't login to the homeserver.")
		}

		if err := stateStore.SetAccessToken(client.AccessToken); err != nil {
			log.Fatalf("Couldn't set access token %+v", err)
		}
	}

	db.Close()
}
