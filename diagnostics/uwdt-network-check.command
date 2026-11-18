#!/bin/bash
# Снимок сети macOS. Не меняет настройки и не требует sudo.
set -u
umask 077
out=$(/usr/bin/mktemp "$HOME/Desktop/UWDT-network-$(/bin/date +%Y%m%d-%H%M%S).XXXXXX") || exit 1
printf 'Сохраняю отчёт: %s\n' "$out"
run() {
  printf '\n> %s\n' "$*"
  /usr/bin/perl -e 'alarm shift; exec @ARGV or die "$!\n"' 6 "$@"
  printf 'exit=%s\n' "$?"
}
{
  /bin/date
  /usr/bin/sw_vers
  run /usr/sbin/scutil --nwi
  run /sbin/route -n get default
  run /sbin/route -n get 1.1.1.1
  run /sbin/route -n get 8.8.8.8
  run /usr/sbin/scutil --dns
  gateway=$(/sbin/route -n get default 2>/dev/null | /usr/bin/awk '/gateway:/ {print $2; exit}')
  if [[ "$gateway" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    run /sbin/ping -n -c 2 -W 1000 "$gateway"
    run /usr/sbin/arp -n "$gateway"
  fi
  run /usr/bin/nc -nvz -G 3 1.1.1.1 443
  run /usr/bin/nc -nvz -G 3 8.8.8.8 443
  run /usr/bin/dscacheutil -q host -a name example.com
  run /usr/bin/curl -q -sS --noproxy '*' --connect-timeout 3 --max-time 5 -o /dev/null -w 'http=%{http_code} remote_ip=%{remote_ip}\n' https://example.com/
  /bin/date
} >>"$out" 2>&1
/bin/mv "$out" "$out.txt" || exit 1
printf 'Готово: %s.txt\n' "$out"
