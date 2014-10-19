#!/bin/bash -e -x

export SSH_KEYFILE="${SSH_KEYFILE:-$HOME/.ssh/isucon2014-09-23.pem}"
export SSH_SERVER=isucon@54.64.166.133
export RSYNC_RSH="ssh -i $SSH_KEYFILE"

curl --data "deploy by $USER" 'https://teamfreesozai.slack.com/services/hooks/slackbot?token=oxjd47qGfo59VhemVz43FQZF&channel=%23general'
rsync -avz ./ $SSH_SERVER:/home/isucon/isucon3/final/webapp/go/
