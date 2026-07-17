# Self-hosting anvil

Anvil's entire server state is one JSON config file. A $5 VPS runs it
comfortably.

## Try before configuring

`anvil serve` with no `anvil.json` present starts **demo mode**: synthetic
calendars, a bookable `/l/intro` link, and the agenda at `/`. Bookings land
in an in-memory calendar and immediately block the slot. Nothing touches
real providers. Copy `deploy/anvil.example.json` to `anvil.json` when ready
to go live.

## Option A: binary + systemd + Caddy

```sh
# 1. Install the binary (or grab a release asset from GitHub)
go install goforge.dev/anvil/cmd/anvil@latest
sudo cp $(go env GOPATH)/bin/anvil /usr/local/bin/

# 2. Config — start from the example and fill in your calendars
sudo mkdir -p /etc/anvil
sudo cp deploy/anvil.example.json /etc/anvil/anvil.json
sudo chmod 600 /etc/anvil/anvil.json   # it holds tokens and passwords
sudoedit /etc/anvil/anvil.json

# 3. Service
sudo cp deploy/anvil.service /etc/systemd/system/
sudo systemctl enable --now anvil
curl -s localhost:8080/healthz

# 4. TLS — Caddy in two lines
sudo apt install caddy
printf 'sched.example.com {\n\treverse_proxy 127.0.0.1:8080\n}\n' | sudo tee /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

## Option B: Docker Compose (anvil + Caddy with auto-TLS)

```sh
cd deploy
cp anvil.example.json anvil.json && $EDITOR anvil.json
DOMAIN=sched.example.com EMAIL=you@example.com docker compose up -d
```

## Wiring calendars

| Source | How |
|--------|-----|
| Any provider's private iCal URL | `"ics_url": "https://…/basic.ics"` — read-only, zero auth setup |
| CalDAV (Fastmail, iCloud, Nextcloud, Radicale) | `anvil caldav-calendars -url … -user …` lists collection URLs; app passwords work |
| Google Calendar | Create a Desktop-app OAuth client in Google Cloud Console, then `anvil gcal-login -client-id … -client-secret …` prints the refresh token |

`book_into` on each link must point at a CalDAV or Google calendar (the
invite gets *written* there). iCal URLs are read-only sources.

## Operations

- **Backup**: `/etc/anvil/anvil.json`. One file.
- **Health**: `GET /healthz` — no auth, fit for uptime monitors.
- **Logs**: one line per request on stderr → `journalctl -u anvil`.
- **Shutdown**: SIGTERM drains in-flight bookings (10s).
- **Upgrade**: replace the binary, `systemctl restart anvil`. Config format
  is versioned with the docs; breaking changes get a major version.
- **Rate limiting**: booking POSTs are limited per-IP (5 burst, 6/min)
  out of the box.

## License

anvil is MIT-licensed and free: every command and an unlimited number of
scheduling links, with nothing to activate.
