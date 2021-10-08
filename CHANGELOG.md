# v0.4.0

* **Thread Mode:** Added ability to use standupbot in threaded mode. To enable,
  type `!su threads true` in your bot DM room.
* The bot now marks all events (including edits) as read after processing them.
* Fixed some bugs and idiosyncrasies with authentication to Matrix.
* Eliminated the `user_config_room` table making the bot less stateful, instead
  relying on the state events stored in the DM rooms.

# v0.3.0

* Fixed a bug with device ID that broke encryption for many users. It is
  recommended to just blow away the database and current flow states and start
  over.

# v0.2.7

* Improved detection of standupbot command messages.
  ([#18](https://todo.sr.ht/~sumner/standupbot/18))
* Added logic to ignore non-command messages if not in config room.
  ([#16](4https://todo.sr.ht/~sumner/standupbot/16))
* Fixed a bug when using the ❌ emoji to cancel a standup post.
* Fixed a couple of bugs with how the config rooms were being set.

# v0.2.6

* Reduced verbosity of event logging.
* Dependency update: mautrix: v0.9.14 -> v0.9.23.

# v0.2.5

* Add `!su undo` command for undoing sending to the standup room.
* Persisted current standup flows across bot restarts.

# v0.2.4

* Fixed detection of whether to start a new flow when the notification time for
  the user comes up.
* Fixed typo in standup flow.
* Prevent editing to "yesterday" when it's Monday.

# v0.2.3

* Fixed bug where typing `!su edit` would crash the bot.

# v0.2.2

* Added ability to edit your standup post after posting it to the send room.

# v0.2.1

* Fixed bug where sending `!su` would make the bot crash.
* Added a CI pipeline for building and testing the application on every push.
* Made the basic compilation tests pass.

# v0.2.0

* Enabled editing of the standup post via edits and redactions to the individual
  messages.
* Added `!su edit` command which takes the user back to the corresponding
  section of the standup post so they can add items to the post.
* Added version and source code link to `!su help`.
* Added some documentation to the README.
* Refuse to send a standup message to a room which the user is not a member of.
  This prevents potential spam if someone gets a hold of a room ID.

# v0.1.5

* Bug fix: start with blank standup post on notify

# v0.1.4

* Bug fix: prevent bot from responding to itself

# v0.1.3

* Prompt for Friday, Weekend on Monday
  ([#1](https://todo.sr.ht/~sumner/standupbot/1))
* Add !su short command ([#3](https://todo.sr.ht/~sumner/standupbot/3))

# v0.1.2

* Don't notify on the weekends.
* Added text to the errors when the bot fails to post state events.

# v0.1.1

* Added settings restoration from room state at startup in case the database
  blows up.

# v0.1.0

Initial release with all base functionality
