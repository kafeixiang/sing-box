#!/usr/bin/env bash

set -e

sudo groupadd sing-box
sudo useradd -g sing-box sing-box
sudo chmod -R g+rwx /var/lib/sing-box
sudo chown -R :sing-box /var/lib/sing-box