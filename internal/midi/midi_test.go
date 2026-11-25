package midi

import (
	"testing"
)

// buildTestFile returns a small format-1 file: 2 tracks, 480 ticks per
// quarter note. Track 0 is the conductor (tempo 120 BPM, 4/4); track 1
// is a piano playing C4 then E4, the second note-on reusing running
// status.
func buildTestFile() []byte {
	var b []byte
	b = append(b, []byte("MThd")...)
	b = append(b, 0, 0, 0, 6) // header length
	b = append(b, 0, 1)       // format 1
	b = append(b, 0, 2)       // 2 tracks
	b = append(b, 0x01, 0xE0) // division 480

	// Track 0: name, tempo 500000 us/qn, time signature 4/4, end.
	t0 := []byte{
		0x00, 0xFF, 0x03, 0x09, 'C', 'o', 'n', 'd', 'u', 'c', 't', 'o', 'r',
		0x00, 0xFF, 0x51, 0x03, 0x07, 0xA1, 0x20, // tempo 500000
		0x00, 0xFF, 0x58, 0x04, 0x04, 0x02, 0x18, 0x08, // 4/4
		0x00, 0xFF, 0x2F, 0x00, // end of track
	}
	b = append(b, []byte("MTrk")...)
	b = append(b, byte(len(t0)>>24), byte(len(t0)>>16), byte(len(t0)>>8), byte(len(t0)))
	b = append(b, t0...)

	// Track 1: name, program 0, C4 on (vel 100), E4 on via running status,
	// C4 off after 480 ticks, E4 off after another 480 ticks, end.
	t1 := []byte{
		0x00, 0xFF, 0x03, 0x05, 'P', 'i', 'a', 'n', 'o',
		0x00, 0xC0, 0x00, // program change 0
		0x00, 0x90, 0x3C, 0x64, // note on C4 vel 100
		0x00, 0x40, 0x64, // running status: note on E4 vel 100
		0x83, 0x60, 0x80, 0x3C, 0x00, // delta 480, note off C4
		0x83, 0x60, 0x80, 0x40, 0x00, // delta 480, note off E4
		0x00, 0xFF, 0x2F, 0x00,
	}
	b = append(b, []byte("MTrk")...)
	b = append(b, byte(len(t1)>>24), byte(len(t1)>>16), byte(len(t1)>>8), byte(len(t1)))
	b = append(b, t1...)
	return b
}

func TestParseHeader(t *testing.T) {
	f, err := Parse(buildTestFile())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Format != 1 {
		t.Errorf("Format = %d, want 1", f.Format)
	}
	if len(f.Tracks) != 2 {
		t.Fatalf("len(Tracks) = %d, want 2", len(f.Tracks))
	}
	if f.Division.TicksPerQuarter != 480 {
		t.Errorf("TicksPerQuarter = %d, want 480", f.Division.TicksPerQuarter)
	}
	if f.Tracks[0].Name != "Conductor" {
		t.Errorf("track 0 name = %q, want Conductor", f.Tracks[0].Name)
	}
	if f.Tracks[1].Name != "Piano" {
		t.Errorf("track 1 name = %q, want Piano", f.Tracks[1].Name)
	}
}

func TestParseNotesAndRunningStatus(t *testing.T) {
	f, err := Parse(buildTestFile())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tr := f.Tracks[1]
	if tr.NoteOns != 2 {
		t.Errorf("NoteOns = %d, want 2", tr.NoteOns)
	}
	if tr.NoteOffs != 2 {
		t.Errorf("NoteOffs = %d, want 2", tr.NoteOffs)
	}
	if tr.LowestNote != 60 || tr.HighestNote != 64 {
		t.Errorf("note range = %d..%d, want 60..64", tr.LowestNote, tr.HighestNote)
	}
	// The E4 note-on used running status: no explicit 0x90 byte.
	var ons []Event
	for _, ev := range tr.Events {
		if ev.IsNoteOn() {
			ons = append(ons, ev)
		}
	}
	if len(ons) != 2 || ons[1].Data1 != 64 {
		t.Fatalf("running-status note on not parsed: %+v", ons)
	}
	if ons[1].AbsTicks != 0 {
		t.Errorf("E4 AbsTicks = %d, want 0", ons[1].AbsTicks)
	}
	if ons[1].Status != 0x90 {
		t.Errorf("E4 status = 0x%02X, want 0x90", ons[1].Status)
	}
	// Velocity-0 note on counts as a note off.
	vel0 := []byte{0x00, 0x90, 0x3C, 0x00}
	ev := Event{Kind: KindChannel, Status: 0x90, Data1: 0x3C, Data2: 0x00}
	_ = vel0
	if !ev.IsNoteOff() || ev.IsNoteOn() {
		t.Error("velocity-0 note on should be a note off")
	}
}

func TestTempoMapAndDuration(t *testing.T) {
	f, err := Parse(buildTestFile())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.TempoMap) != 1 {
		t.Fatalf("len(TempoMap) = %d, want 1 (file tempo at tick 0 replaces the default)", len(f.TempoMap))
	}
	if f.TempoMap[0].BPM != 120 {
		t.Errorf("tempo BPM = %v, want 120", f.TempoMap[0].BPM)
	}
	// 960 ticks at 480 tpq and 500000 us/qn = 1.0 second.
	if f.DurationSeconds != 1.0 {
		t.Errorf("DurationSeconds = %v, want 1.0", f.DurationSeconds)
	}
	last := f.Tracks[1].Events[len(f.Tracks[1].Events)-1]
	if last.AbsSeconds != 1.0 {
		t.Errorf("end-of-track AbsSeconds = %v, want 1.0", last.AbsSeconds)
	}
}

func TestNoteName(t *testing.T) {
	cases := map[byte]string{60: "C4", 61: "C#4", 64: "E4", 69: "A4", 0: "C-1", 127: "G9"}
	for n, want := range cases {
		if got := NoteName(n); got != want {
			t.Errorf("NoteName(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestVLQ(t *testing.T) {
	cases := []struct {
		bytes []byte
		want  uint32
	}{
		{[]byte{0x00}, 0},
		{[]byte{0x7F}, 127},
		{[]byte{0x81, 0x00}, 128},
		{[]byte{0x83, 0x60}, 480},
		{[]byte{0xFF, 0x7F}, 16383},
	}
	for _, c := range cases {
		r := &reader{b: c.bytes}
		got, err := r.vlq()
		if err != nil {
			t.Fatalf("vlq(%v): %v", c.bytes, err)
		}
		if got != c.want {
			t.Errorf("vlq(%v) = %d, want %d", c.bytes, got, c.want)
		}
	}
	// 5-byte VLQ is rejected.
	r := &reader{b: []byte{0x81, 0x80, 0x80, 0x80, 0x00}}
	if _, err := r.vlq(); err == nil {
		t.Error("expected error for 5-byte VLQ")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"wrong magic", []byte("RIFFxxxxxxxxxxxx")},
		{"truncated header", []byte("MThd\x00\x00\x00\x06\x00")},
		{"bad format", func() []byte {
			b := buildTestFile()
			b[9] = 0x03 // format 3
			return b
		}()},
		{"truncated track", buildTestFile()[:30]},
	}
	for _, c := range cases {
		if _, err := Parse(c.data); err == nil {
			t.Errorf("Parse(%s): expected error, got nil", c.name)
		}
	}
}

func TestSMPTEDivision(t *testing.T) {
	b := buildTestFile()
	// Division 0xE728 = SMPTE 25 fps, 40 ticks per frame.
	b[12] = 0xE7
	b[13] = 0x28
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !f.Division.SMPTE || f.Division.FramesPerSecond != 25 || f.Division.TicksPerFrame != 40 {
		t.Errorf("SMPTE division = %+v", f.Division)
	}
	// 1000 ticks at 25 fps * 40 tpf = 1.0 second.
	if got := f.ticksToSeconds(1000); got != 1.0 {
		t.Errorf("ticksToSeconds(1000) = %v, want 1.0", got)
	}
}
