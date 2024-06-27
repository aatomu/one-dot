screen -r dot -X stuff "^C\n"
sleep 1s
screen -r dot -X kill
screen -U -A -md -S dot
screen -r dot -X stuff "cd $(dirname $0)/\n"
screen -r dot -X stuff "while :; do go run .; done\n"
