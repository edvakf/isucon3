#!/bin/bash

curl -s --data "start benchmark" 'https://teamfreesozai.slack.com/services/hooks/slackbot?token=oxjd47qGfo59VhemVz43FQZF&channel=%23general' -o/dev/null
carton exec -- perl bench.pl  -d /home/isucon/image_source/ --workload=1 --host=isu3app2 http://isu3app2/ 2>&1 | tee >(while read line; do curl -s --data '`'"$line"'`' 'https://teamfreesozai.slack.com/services/hooks/slackbot?token=oxjd47qGfo59VhemVz43FQZF&channel=%23general' -o/dev/null; done)
