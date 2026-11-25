// Package midi parses Standard MIDI Files (SMF, .mid) with zero dependencies.
//
// It handles format 0, 1 and 2 files: the MThd header, MTrk chunks,
// variable-length quantities, running status, channel voice messages,
// system-exclusive messages and meta events. A tempo map built from the
// Set Tempo meta events turns tick positions into wall-clock seconds.
package midi

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Kind classifies an event.
type Kind int

const (
	// KindChannel is a MIDI channel voice message (note on/off, control
	// change, program change, ...).
	KindChannel Kind = iota
	// KindMeta is a meta event (0xFF), e.g. tempo, time signature, track name.
	KindMeta
	// KindSysEx is a system-exclusive message (0xF0 / 0xF7).
	KindSysEx
)

// Channel message status nibbles.
const (
	StatusNoteOff         = 0x80
	StatusNoteOn          = 0x90
	StatusAftertouch      = 0xA0
	StatusControlChange   = 0xB0
	StatusProgramChange   = 0xC0
	StatusChannelPressure = 0xD0
	StatusPitchBend       = 0xE0
)

// Meta event types.
const (
	MetaSequenceNumber = 0x00
	MetaText           = 0x01
	MetaCopyright      = 0x02
	MetaTrackName      = 0x03
	MetaInstrument     = 0x04
	MetaLyric          = 0x05
	MetaMarker         = 0x06
	MetaCuePoint       = 0x07
	MetaEndOfTrack     = 0x2F
	MetaTempo          = 0x51
	MetaTimeSignature  = 0x58
	MetaKeySignature   = 0x59
)

// Division describes how ticks map to musical time.
type Division struct {
	// TicksPerQuarter is set when the file uses ticks-per-quarter-note
	// timing (the common case).
	TicksPerQuarter int
	// SMPTE is set when the file uses SMPTE timing instead.
	SMPTE bool
	// FramesPerSecond and TicksPerFrame apply only when SMPTE is set.
	FramesPerSecond int
	TicksPerFrame   int
}

// Event is a single parsed MIDI event.
type Event struct {
	// Delta is the ticks since the previous event in the same track.
	Delta uint32
	// AbsTicks is the absolute tick position within the track.
	AbsTicks uint64
	// AbsSeconds is the wall-clock position, resolved against the file's
	// tempo map. It is 0 when the file has no tempo information beyond
	// the default 120 BPM (which is itself a valid tempo).
	AbsSeconds float64

	Kind Kind

	// Channel fields (KindChannel only).
	Status  byte // full status byte, high nibble is the message type
	Channel int  // 0-15
	Data1   byte // note number / controller number / program number
	Data2   byte // velocity / value (0 for one-byte messages)

	// Meta fields (KindMeta only).
	MetaType byte
	MetaData []byte

	// SysEx payload (KindSysEx only, without the 0xF0/0xF7 status byte).
	SysEx []byte
}

// IsNoteOn reports whether the event starts a note. A Note On with
// velocity 0 is a Note Off by convention.
func (e Event) IsNoteOn() bool {
	return e.Kind == KindChannel && e.Status&0xF0 == StatusNoteOn && e.Data2 > 0
}

// IsNoteOff reports whether the event ends a note.
func (e Event) IsNoteOff() bool {
	return e.Kind == KindChannel &&
		(e.Status&0xF0 == StatusNoteOff ||
			(e.Status&0xF0 == StatusNoteOn && e.Data2 == 0))
}

// NoteName renders a MIDI note number as e.g. "C4" (middle C = 60).
func NoteName(n byte) string {
	names := [...]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	return fmt.Sprintf("%s%d", names[int(n)%12], int(n)/12-1)
}

// Track is one MTrk chunk with its parsed events.
type Track struct {
	Index  int
	Name   string
	Events []Event

	// Stats, filled by Parse.
	NoteOns     int
	NoteOffs    int
	LowestNote  int // -1 when the track has no notes
	HighestNote int
	Channels    map[int]bool // channels used by this track
}

// File is a parsed Standard MIDI File.
type File struct {
	Format   uint16
	Division Division
	Tracks   []*Track

	// TempoMap maps absolute tick -> microseconds per quarter note,
	// collected from every track (conventionally track 0). The first
	// entry is always tick 0 at the default 120 BPM (500000 us/qn)
	// unless the file sets a tempo there.
	TempoMap []TempoChange

	// DurationSeconds is the length of the longest track, in seconds.
	DurationSeconds float64
}

// TempoChange is one entry of the tempo map.
type TempoChange struct {
	AbsTicks     uint64
	Microseconds uint32 // per quarter note
	BPM          float64
	AbsSeconds   float64
}

// Parse parses a complete Standard MIDI File from raw bytes.
func Parse(data []byte) (*File, error) {
	r := &reader{b: data}
	f := &File{}

	// ---- MThd header ----
	if len(data) < 14 || string(data[0:4]) != "MThd" {
		return nil, errors.New("midi: not a Standard MIDI File (missing MThd header)")
	}
	r.pos = 4
	hlen, err := r.u32()
	if err != nil {
		return nil, err
	}
	if hlen < 6 {
		return nil, fmt.Errorf("midi: header length %d too small", hlen)
	}
	format, err := r.u16()
	if err != nil {
		return nil, err
	}
	if format > 2 {
		return nil, fmt.Errorf("midi: unsupported SMF format %d", format)
	}
	ntrks, err := r.u16()
	if err != nil {
		return nil, err
	}
	div, err := r.u16()
	if err != nil {
		return nil, err
	}
	f.Format = format
	f.Division = parseDivision(div)
	// Skip any extra header bytes (hlen is normally 6).
	if hlen > 6 {
		if err := r.skip(int(hlen) - 6); err != nil {
			return nil, err
		}
	}

	// ---- MTrk chunks ----
	for i := 0; i < int(ntrks); i++ {
		tr, err := parseTrack(r, i)
		if err != nil {
			return nil, fmt.Errorf("midi: track %d: %w", i, err)
		}
		f.Tracks = append(f.Tracks, tr)
	}

	f.buildTempoMap()
	f.DurationSeconds = f.duration()
	return f, nil
}

func parseDivision(div uint16) Division {
	if div&0x8000 == 0 {
		return Division{TicksPerQuarter: int(div)}
	}
	// SMPTE: high byte is frames per second (stored negated),
	// low byte is ticks per frame.
	fps := int(int8(div >> 8))
	if fps < 0 {
		fps = -fps
	}
	return Division{
		SMPTE:           true,
		FramesPerSecond: fps,
		TicksPerFrame:   int(div & 0xFF),
	}
}

func parseTrack(r *reader, index int) (*Track, error) {
	if r.remaining() < 8 || string(r.b[r.pos:r.pos+4]) != "MTrk" {
		return nil, errors.New("missing MTrk chunk")
	}
	r.pos += 4
	length, err := r.u32()
	if err != nil {
		return nil, err
	}
	end := r.pos + int(length)
	if end > len(r.b) {
		return nil, errors.New("track chunk extends past end of file")
	}
	tr := &Track{Index: index, LowestNote: -1, Channels: map[int]bool{}}
	var abs uint64
	var running byte // running status byte, 0 when none
	for r.pos < end {
		delta, err := r.vlq()
		if err != nil {
			return nil, err
		}
		abs += uint64(delta)
		ev := Event{Delta: delta, AbsTicks: abs}
		if r.pos >= end {
			return nil, errors.New("truncated event at end of track")
		}
		b := r.b[r.pos]
		switch {
		case b == 0xFF: // meta event
			r.pos++
			ev.Kind = KindMeta
			mt, err := r.u8()
			if err != nil {
				return nil, err
			}
			ev.MetaType = mt
			mlen, err := r.vlq()
			if err != nil {
				return nil, err
			}
			ev.MetaData, err = r.bytes(int(mlen))
			if err != nil {
				return nil, err
			}
			running = 0 // meta events cancel running status
		case b == 0xF0 || b == 0xF7: // system exclusive
			r.pos++
			ev.Kind = KindSysEx
			slen, err := r.vlq()
			if err != nil {
				return nil, err
			}
			ev.SysEx, err = r.bytes(int(slen))
			if err != nil {
				return nil, err
			}
			running = 0 // sysex cancels running status
		case b&0x80 != 0: // fresh channel status byte
			r.pos++
			running = b
			if err := parseChannelEvent(r, &ev, b); err != nil {
				return nil, err
			}
		default: // running status: data byte, status implied
			if running == 0 {
				return nil, fmt.Errorf("data byte 0x%02X with no running status", b)
			}
			if err := parseChannelEvent(r, &ev, running); err != nil {
				return nil, err
			}
		}
		tr.Events = append(tr.Events, ev)
		tr.tally(ev)
	}
	if r.pos != end {
		return nil, errors.New("track chunk length mismatch")
	}
	return tr, nil
}

// parseChannelEvent reads the data bytes of a channel message given its
// status byte. The status byte itself has already been consumed.
func parseChannelEvent(r *reader, ev *Event, status byte) error {
	ev.Kind = KindChannel
	ev.Status = status
	ev.Channel = int(status & 0x0F)
	hi := status & 0xF0
	oneByte := hi == StatusProgramChange || hi == StatusChannelPressure
	d1, err := r.u8()
	if err != nil {
		return err
	}
	if d1&0x80 != 0 {
		return fmt.Errorf("invalid data byte 0x%02X", d1)
	}
	ev.Data1 = d1
	if oneByte {
		return nil
	}
	d2, err := r.u8()
	if err != nil {
		return err
	}
	if d2&0x80 != 0 {
		return fmt.Errorf("invalid data byte 0x%02X", d2)
	}
	ev.Data2 = d2
	return nil
}

func (t *Track) tally(ev Event) {
	if ev.Kind == KindMeta {
		if ev.MetaType == MetaTrackName {
			t.Name = string(ev.MetaData)
		}
		return
	}
	if ev.Kind != KindChannel {
		return
	}
	t.Channels[ev.Channel] = true
	if ev.IsNoteOn() {
		t.NoteOns++
		n := int(ev.Data1)
		if t.LowestNote == -1 || n < t.LowestNote {
			t.LowestNote = n
		}
		if n > t.HighestNote {
			t.HighestNote = n
		}
	}
	if ev.IsNoteOff() {
		t.NoteOffs++
	}
}

// buildTempoMap collects Set Tempo events and resolves every event's
// wall-clock position. Ticks-per-quarter files use the tempo map;
// SMPTE files use frames per second directly.
func (f *File) buildTempoMap() {
	f.TempoMap = []TempoChange{{AbsTicks: 0, Microseconds: 500000, BPM: 120}}
	seen := map[uint64]bool{0: true}
	for _, tr := range f.Tracks {
		for _, ev := range tr.Events {
			if ev.Kind == KindMeta && ev.MetaType == MetaTempo && len(ev.MetaData) == 3 {
				us := uint32(ev.MetaData[0])<<16 | uint32(ev.MetaData[1])<<8 | uint32(ev.MetaData[2])
				if us == 0 {
					continue
				}
				if seen[ev.AbsTicks] {
					// A tempo at tick 0 replaces the assumed default;
					// a second tempo at any other tick is ignored.
					if ev.AbsTicks == 0 {
						f.TempoMap[0] = TempoChange{
							AbsTicks:     0,
							Microseconds: us,
							BPM:          60_000_000 / float64(us),
						}
					}
					continue
				}
				seen[ev.AbsTicks] = true
				f.TempoMap = append(f.TempoMap, TempoChange{
					AbsTicks:     ev.AbsTicks,
					Microseconds: us,
					BPM:          60_000_000 / float64(us),
				})
			}
		}
	}
	// Sort by tick (insertion order is usually already sorted, but
	// tempo events can live in any track).
	for i := 1; i < len(f.TempoMap); i++ {
		for j := i; j > 0 && f.TempoMap[j].AbsTicks < f.TempoMap[j-1].AbsTicks; j-- {
			f.TempoMap[j], f.TempoMap[j-1] = f.TempoMap[j-1], f.TempoMap[j]
		}
	}
	// Resolve seconds for each tempo change and each event.
	for i := range f.TempoMap {
		f.TempoMap[i].AbsSeconds = f.ticksToSeconds(f.TempoMap[i].AbsTicks)
	}
	for _, tr := range f.Tracks {
		for i := range tr.Events {
			tr.Events[i].AbsSeconds = f.ticksToSeconds(tr.Events[i].AbsTicks)
		}
	}
}

// ticksToSeconds converts an absolute tick position to seconds using
// the tempo map (ticks-per-quarter) or SMPTE timing.
func (f *File) ticksToSeconds(ticks uint64) float64 {
	d := f.Division
	if d.SMPTE {
		if d.FramesPerSecond == 0 || d.TicksPerFrame == 0 {
			return 0
		}
		return float64(ticks) / float64(d.FramesPerSecond*d.TicksPerFrame)
	}
	if d.TicksPerQuarter == 0 {
		return 0
	}
	seconds := 0.0
	prevTick := uint64(0)
	prevUs := uint32(500000)
	for _, tc := range f.TempoMap {
		if tc.AbsTicks > ticks {
			break
		}
		seconds += float64(tc.AbsTicks-prevTick) * float64(prevUs) / float64(d.TicksPerQuarter) / 1e6
		prevTick = tc.AbsTicks
		prevUs = tc.Microseconds
	}
	seconds += float64(ticks-prevTick) * float64(prevUs) / float64(d.TicksPerQuarter) / 1e6
	return seconds
}

func (f *File) duration() float64 {
	max := 0.0
	for _, tr := range f.Tracks {
		if len(tr.Events) == 0 {
			continue
		}
		last := tr.Events[len(tr.Events)-1].AbsSeconds
		if last > max {
			max = last
		}
	}
	return max
}

// reader is a small bounds-checked cursor over the file bytes.
type reader struct {
	b   []byte
	pos int
}

func (r *reader) remaining() int { return len(r.b) - r.pos }

func (r *reader) u8() (byte, error) {
	if r.remaining() < 1 {
		return 0, errors.New("unexpected end of file")
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

func (r *reader) u16() (uint16, error) {
	if r.remaining() < 2 {
		return 0, errors.New("unexpected end of file")
	}
	v := binary.BigEndian.Uint16(r.b[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, errors.New("unexpected end of file")
	}
	v := binary.BigEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, errors.New("unexpected end of file")
	}
	v := r.b[r.pos : r.pos+n]
	r.pos += n
	return v, nil
}

func (r *reader) skip(n int) error {
	if n < 0 || r.remaining() < n {
		return errors.New("unexpected end of file")
	}
	r.pos += n
	return nil
}

// vlq reads a variable-length quantity: up to 4 bytes, 7 bits each,
// most-significant group first, high bit set on all but the last.
func (r *reader) vlq() (uint32, error) {
	var v uint32
	for i := 0; i < 4; i++ {
		b, err := r.u8()
		if err != nil {
			return 0, err
		}
		v = (v << 7) | uint32(b&0x7F)
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, errors.New("variable-length quantity longer than 4 bytes")
}
