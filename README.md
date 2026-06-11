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

## Library

Three packages, no dependencies outside the standard library:

- **`interval`** — half-open span algebra: `Normalize`, `Union`,
  `Intersect`, `Subtract`, `Complement`, `Shrink`. Everything else is built
  on this.
- **`schedule`** — the kernel. `Find(Request)` intersects required
  attendees' free time with a working-hours mask, applies travel padding by
  *shrinking* free intervals (so a padded meeting fits iff the slot survives),
  walks candidates at `Step` granularity, and ranks by optional attendance.
  Pure and deterministic: busy sets in, slots out — trivially testable, and
  ready to sit behind a server or UI unchanged.
- **`ics`** — a small, honest iCalendar reader: VEVENT, IANA `TZID`,
  all-day events, `DURATION`, `EXDATE`, `TRANSP`/`STATUS`, and
  DAILY/WEEKLY recurrence with `INTERVAL`/`COUNT`/`UNTIL`/`BYDAY`. Monthly
  and yearly rules fall back to their first occurrence. It reads feeds; it
  does not write them.

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

1. ~~Library: interval algebra, scheduling kernel, iCal ingestion~~ ✦ here
2. ~~CLI: `anvil find` over iCal files/URLs~~ ✦ here
3. CalDAV and Google Calendar API adapters (read *and* write — create the
   invite, not just find the slot)
4. `anvil serve` — self-hostable scheduling links: "pick a time that works
   for these four people," with conferencing links on the invite and
   travel-time-aware in-person options
5. UI: at-a-glance agenda across all calendars, join links and directions
   one tap away

## Non-goals

Exchange. Outlook. Being a calendar. anvil reads calendars and writes
invitations; it does not want to *be* your calendar.
