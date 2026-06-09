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

curl -x "$TELEGRAM_PROXY_URL" \
  "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe"
```

Expected result is a Telegram JSON response with `"ok":true`.

## Security Notes

- Do not run an open proxy.
- Require authentication.
- Allow only `api.telegram.org:443`.
- Keep the Telegram bot token on the main server, not on the VPS.
- Rotate proxy credentials if the VPS is compromised.
