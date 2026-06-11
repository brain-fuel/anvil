# anvil

Meeting times, hammered into shape. anvil finds slots that work across
*every* calendar each person lives by — personal, work, startup, volunteer —
for any mix of required and optional attendees, with drive-time padding for
in-person meetings.

It is built in the order the problem deserves: **a library first** (interval
algebra + a scheduling kernel), **a CLI second** (usable today against any
provider's secret iCal URL — no OAuth, no Exchange), and a UI later on top of
the same kernel.

## Why

Existing schedulers assume one person ≈ one calendar, maybe two. The real
shape of the problem is *people × calendars*: you have four calendars,
your co-founder has three, and "find a time for Mike and Melissa and Stan"
means none of the ten may conflict. anvil's model is exactly that:

```go
mike := schedule.Merge("mike", workBusy, personalBusy, volunteerBusy)
```

A `Person` is a name plus the union of all their busy time. The finder never
needs to know how many calendars fed it.

## Use it now

```sh
go install goforge.dev/anvil/cmd/anvil@latest

anvil find -d 45m -from 2026-06-15 -to 2026-06-19 -tz America/New_York \
    -who mike=work.ics,personal.ics \
    -who melissa=https://calendar.google.com/calendar/ical/…/basic.ics \
    -opt stan=stan.ics -opt david=david.ics \
    -travel 30m
```

```
Wed Jun 17  13:30–14:15 EDT  4/4
Thu Jun 18  10:30–11:15 EDT  4/4
Thu Jun 18  11:00–11:45 EDT  3/4  missing: stan
```

Every `-who` is required; every `-opt` is scored — slots with more optional
attendees rank first, earliest wins ties. `-travel 30m` demands a clear half
hour on both sides of the meeting (drive time), without widening the meeting
itself. Calendars are iCal files or URLs; every major provider (Google,
Fastmail, iCloud, Proton) exports a private iCal address.

Flags: `-d` duration, `-from`/`-to` date window, `-hours 09:00-17:00`,
`-days mon,tue,…` (default weekdays), `-step` start granularity, `-n` max
results.

## Scheduling links: `anvil serve`

Self-hostable Calendly-for-many-people. One JSON config wires calendars
(iCal URLs, CalDAV, Google) to people, people to links, and each link to the
calendar the booked invite lands in:

```jsonc
{
  "timezone": "America/New_York",
  "calendars": [
    {"name": "mike-work",     "ics_url": "https://…/basic.ics"},
    {"name": "mike-personal", "caldav": {"base_url": "https://caldav.fastmail.com",
        "username": "mike@fastmail.com", "password": "app-password",
        "calendar_url": "https://…/calendars/…/personal/"}},
    {"name": "melissa", "google": {"client_id": "…", "client_secret": "…",
        "refresh_token": "…", "calendar_id": "primary"}}
  ],
  "people": [
    {"name": "mike",    "email": "mike@example.com",    "calendars": ["mike-work", "mike-personal"]},
    {"name": "melissa", "email": "melissa@example.com", "calendars": ["melissa"]}
  ],
  "links": [
    {"slug": "intro", "title": "Intro with Mike & Melissa", "duration_m": 45,
     "required": ["mike", "melissa"], "optional": ["stan"],
     "google_meet": true, "book_into": "melissa"},
    {"slug": "onsite", "title": "Coffee with Mike", "duration_m": 60,
     "required": ["mike"], "in_person": true, "travel_m": 30,
     "address": "300 Webster St, Oakland", "book_into": "mike-personal"}
  ]
}
```

`anvil serve -config anvil.json`, hand out `https://your.host/l/intro`, and
J-random person picks from times that work across *every* calendar of every
required attendee. Booking re-verifies against live calendars (409 if the
slot just vanished), then creates the invite — with a Google Meet link
minted on the spot (`google_meet`), a static room (`video_url`), or an
in-person address with drive-time padding (`in_person` + `travel_m`).
Attendee invitations go out through the target calendar (Google
`sendUpdates`, CalDAV server scheduling).

## Agenda: desktop, mobile, terminal

`anvil serve` also hosts an installable web app (PWA — add it to a phone
home screen or install from a desktop browser) showing everything coming
across all configured calendars, each entry with its **Join** link (Meet,
Zoom, Teams, Webex, Jitsi, Whereby auto-detected) or **Directions** button.
Protect it with `"auth": {"username": …, "password": …}`; scheduling links
stay public. Same thing in a terminal:

```sh
anvil agenda -cal mike=work.ics -cal mike=personal.ics -days 3
```

Setup helpers: `anvil gcal-login` (one-time OAuth, prints the refresh
token), `anvil caldav-calendars` (lists collection URLs for the config).

## Library

Six packages, no dependencies outside the standard library:

- **`interval`** — half-open span algebra: `Normalize`, `Union`,
  `Intersect`, `Subtract`, `Complement`, `Shrink`. Everything else is built
  on this.
- **`schedule`** — the kernel. `Find(Request)` intersects required
  attendees' free time with a working-hours mask, applies travel padding by
  *shrinking* free intervals (so a padded meeting fits iff the slot survives),
  walks candidates at `Step` granularity, and ranks by optional attendance.
  Pure and deterministic: busy sets in, slots out — trivially testable, and
  ready to sit behind a server or UI unchanged.
- **`ics`** — a small, honest iCalendar reader *and writer*: VEVENT, IANA
  `TZID`, all-day events, `DURATION`, `EXDATE`, `TRANSP`/`STATUS`,
  attendees/organizer, and DAILY/WEEKLY recurrence with
  `INTERVAL`/`COUNT`/`UNTIL`/`BYDAY`. Monthly and yearly rules fall back to
  their first occurrence. `Encode` emits RFC 5545 (folded, escaped) for
  invites and CalDAV PUTs.
- **`caldav`** — minimal CalDAV client: discovery
  (current-user-principal → calendar-home-set → collections),
  calendar-query REPORT for busy time, and event creation
  (`If-None-Match: *`, so it never overwrites). Fastmail, iCloud,
  Nextcloud, Radicale.
- **`gcal`** — minimal Google Calendar API client: refresh-token OAuth,
  free/busy, event listing, and event insertion with Google Meet
  conference creation. Includes the loopback login flow.
- **`agenda`** — merges occurrences across calendars into one sorted list,
  extracting the join URL or a directions link per item.
- **`serve`** — the scheduling-link server and agenda app described above,
  built entirely on the packages above.

```go
cal, _ := ics.ParseIn(feed, tz)
busy := cal.Busy(window)               // interval.Set
mike := schedule.Merge("mike", busy, otherBusy)
slots, _ := schedule.Find(schedule.Request{
    Duration: 45 * time.Minute,
    Window:   window,
    Travel:   30 * time.Minute,
    Required: []schedule.Person{mike, melissa},
    Optional: []schedule.Person{stan, david},
})
```

## Roadmap

1. ~~Library: interval algebra, scheduling kernel, iCal ingestion~~ — v0.1.0
2. ~~CLI: `anvil find` over iCal files/URLs~~ — v0.1.0
3. ~~CalDAV and Google Calendar API adapters (read *and* write — create the
   invite, not just find the slot)~~ — v0.2.0
4. ~~`anvil serve` — self-hostable scheduling links with conferencing links
   on the invite and travel-time-aware in-person options~~ — v0.2.0
5. ~~UI: at-a-glance agenda across all calendars, join links and directions
   one tap away (installable PWA + terminal)~~ — v0.2.0

Next up: ATTENDEE free/busy via iTIP, recurring link round-robin, and
native wrappers if the PWA ever feels insufficient.

## Non-goals

Exchange. Outlook. Being a calendar. anvil reads calendars and writes
invitations; it does not want to *be* your calendar.
