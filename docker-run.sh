#!/bin/sh

if [[ -z "$GID" ]]; then
	GID="$UID"
fi

# Define functions.
function fixperms {
	chown -R $UID:$GID /data /opt/standupbot
}

if [[ ! -f /data/config.json ]]; then
	cp /opt/standupbot/config.sample.json /data/config.json
	echo "Didn't find a config file."
	echo "Copied default config file to /data/config.json"
	echo "Modify that config file to your liking."
	exit
fi

cd /data
fixperms
exec su-exec $UID:$GID /usr/bin/standupbot
