package ics

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// NewUID returns a random globally unique event identifier.
func NewUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b) + "@anvil"
}

// Encode writes events as an iCalendar stream suitable for a CalDAV PUT or
// an email invitation. method is the iTIP METHOD ("REQUEST" for invites, ""
// to omit). Events need UID and Start/End set; Stamp defaults to Start.
func Encode(w io.Writer, method string, events []Event) error {
	e := &encoder{w: w}
	e.prop("BEGIN", "VCALENDAR")
	e.prop("VERSION", "2.0")
	e.prop("PRODID", "-//goforge.dev/anvil//EN")
	e.prop("CALSCALE", "GREGORIAN")
	if method != "" {
		e.prop("METHOD", method)
	}
	for _, ev := range events {
		if ev.UID == "" {
			return fmt.Errorf("ics: event %q has no UID", ev.Summary)
		}
		if ev.Start.IsZero() || ev.End.IsZero() {
			return fmt.Errorf("ics: event %q needs Start and End", ev.Summary)
		}
		stamp := ev.Stamp
		if stamp.IsZero() {
			stamp = ev.Start
		}
		e.prop("BEGIN", "VEVENT")
		e.prop("UID", ev.UID)
		e.prop("DTSTAMP", stamp.UTC().Format("20060102T150405Z"))
		if ev.AllDay {
			e.prop("DTSTART;VALUE=DATE", ev.Start.Format("20060102"))
			e.prop("DTEND;VALUE=DATE", ev.End.Format("20060102"))
		} else {
			e.prop("DTSTART", ev.Start.UTC().Format("20060102T150405Z"))
			e.prop("DTEND", ev.End.UTC().Format("20060102T150405Z"))
		}
		if ev.Summary != "" {
			e.prop("SUMMARY", escape(ev.Summary))
		}
		if ev.Location != "" {
			e.prop("LOCATION", escape(ev.Location))
		}
		if ev.Description != "" {
			e.prop("DESCRIPTION", escape(ev.Description))
		}
		if ev.URL != "" {
			e.prop("URL", ev.URL)
		}
		if ev.Organizer.Email != "" {
			e.prop("ORGANIZER"+cnParam(ev.Organizer), "mailto:"+ev.Organizer.Email)
		}
		for _, a := range ev.Attendees {
			e.prop("ATTENDEE"+cnParam(a)+";ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE",
				"mailto:"+a.Email)
		}
		if ev.Transparent {
			e.prop("TRANSP", "TRANSPARENT")
		}
		e.prop("END", "VEVENT")
	}
	e.prop("END", "VCALENDAR")
	return e.err
}

func cnParam(a Attendee) string {
	if a.Name == "" {
		return ""
	}
	return `;CN="` + strings.ReplaceAll(a.Name, `"`, "") + `"`
}

type encoder struct {
	w   io.Writer
	err error
}

// prop writes one content line, folded at 75 octets per RFC 5545 §3.1.
func (e *encoder) prop(name, value string) {
	if e.err != nil {
		return
	}
	line := name + ":" + value
	var b strings.Builder
	for len(line) > 75 {
		cut := 75
		for cut > 1 && !isUTF8Start(line[cut]) {
			cut-- // never split a UTF-8 sequence
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n ")
		line = line[cut:]
	}
	b.WriteString(line)
	b.WriteString("\r\n")
	_, e.err = io.WriteString(e.w, b.String())
}

func isUTF8Start(c byte) bool { return c < 0x80 || c >= 0xC0 }

func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`, "\r", "")
	return r.Replace(s)
}
