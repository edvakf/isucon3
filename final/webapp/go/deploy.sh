#!/bin/bash -e -x

export SSH_KEYFILE="${SSH_KEYFILE:-$HOME/.ssh/isucon2014-09-23.pem}"
export SSH_USER=isucon
export RSYNC_RSH="ssh -i $SSH_KEYFILE"

curl --data "deploy by $USER" 'https://teamfreesozai.slack.com/services/hooks/slackbot?token=oxjd47qGfo59VhemVz43FQZF&channel=%23general'

SERVERS="54.64.230.18 54.64.166.133 54.64.226.134"

for SERVER in ${SERVERS}; do
  rsync -avz ./ ${SSH_USER}@${SERVER}:/home/isucon/isucon3/final/webapp/go/
done
