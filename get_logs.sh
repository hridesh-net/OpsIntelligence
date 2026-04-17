#!/usr/bin/expect -f
spawn ssh deros@100.96.25.105 "journalctl --user -u opsintelligence --since '30 minutes ago' --no-pager"
expect "password:"
send "pi\r"
expect eof
