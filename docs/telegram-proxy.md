# Telegram Proxy

Use this when the main server cannot reach `api.telegram.org` directly.

The implementation expects `TELEGRAM_PROXY_URL` in `.env`, for example:

```text
TELEGRAM_PROXY_URL=http://proxy-user:proxy-pass@proxy.example.com:3128
```

## Squid Example

Install Squid and Apache htpasswd tools on the VPS:

```sh
sudo apt-get update
sudo apt-get install -y squid apache2-utils
```

Create a proxy user:

```sh
sudo htpasswd -c /etc/squid/passwd proxy-user
```

Copy the example config:

```sh
sudo cp deploy/proxy/squid-telegram-only.conf /etc/squid/conf.d/telegram-only.conf
sudo squid -k parse
sudo systemctl restart squid
```

Restrict the proxy port in the VPS firewall to the main server public IP.

## Smoke Test

From the main server:

```sh
set -a
. /opt/reverse-logger/.env
set +a

telegram_curl_config="$(mktemp)"
chmod 600 "$telegram_curl_config"
trap 'rm -f "$telegram_curl_config"' EXIT

cat > "$telegram_curl_config" <<EOF
proxy = "$TELEGRAM_PROXY_URL"
url = "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe"
silent
show-error
EOF

curl --config "$telegram_curl_config"
```

Expected result is a Telegram JSON response with `"ok":true`.

Then verify that the bot can write to the configured chat:

```sh
first_chat_id="${TELEGRAM_CHAT_IDS%%,*}"
message="reverse_logger Telegram smoke test $(date -u +%FT%TZ)"

cat > "$telegram_curl_config" <<EOF
proxy = "$TELEGRAM_PROXY_URL"
url = "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage"
request = "POST"
data = "chat_id=${first_chat_id}"
data-urlencode = "text=${message}"
silent
show-error
EOF

curl --config "$telegram_curl_config"
```

Expected result is a Telegram JSON response with `"ok":true` and a visible
message in the target chat.
The temporary curl config keeps the bot token and proxy credentials out of the
process argument list on shared hosts.

## Security Notes

- Do not run an open proxy.
- Require authentication.
- Allow only `api.telegram.org:443`.
- Keep the Telegram bot token on the main server, not on the VPS.
- Rotate proxy credentials if the VPS is compromised.
