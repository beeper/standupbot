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
