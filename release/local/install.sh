#!/usr/bin/env bash

# set -e -o pipefail

if [ -d /usr/local/go ]; then
  export PATH="$PATH:/usr/local/go/bin"
fi

DIR=$(dirname "$0")
PROJECT=$DIR/../..

pushd $PROJECT
TAGS="with_quic,with_wireguard,with_utls,with_clash_api,with_gvisor" make build
popd

RUNNING_BOX=$(systemctl is-active sing-box && echo "1")
if [ -n "$RUNNING_BOX" ]; then
  sudo systemctl stop sing-box
fi
sudo cp ./sing-box /usr/local/bin/
sudo mkdir -p /usr/local/etc/sing-box
sudo mkdir -p /var/lib/sing-box
# sudo cp $PROJECT/release/config/config.json /usr/local/etc/sing-box/config.json
sudo cp $DIR/sing-box.service /etc/systemd/system
sudo systemctl daemon-reload
if [ -n "$RUNNING_BOX" ]; then
  sudo systemctl start sing-box
fi