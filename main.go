// Command midi is a zero-dependency inspector for Standard MIDI Files.
//
// Usage:
//
//	midi info <file.mid> [--json]        file overview: format, tempo, duration
//	midi tracks <file.mid> [--json]      per-track summary
//	midi events <file.mid> [--track N] [--limit N] [--json]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Subhanmalik138/go-midi-inspector/internal/midi"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "info":
		err = runInfo(args)
	case "tracks":
		err = runTracks(args)
	case "events":
		err = runEvents(args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "midi: unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "midi: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`midi — inspect Standard MIDI Files (.mid)

Usage:
  midi info <file.mid> [--json]
      File overview: format, division, track count, tempo map, duration.
  midi tracks <file.mid> [--json]
      One line per track: name, events, notes, note range, channels.
  midi events <file.mid> [--track N] [--limit N] [--json]
      List events in tick order with wall-clock times.

Flags:
  --json      machine-readable output
  --track N   events: only this track (default: all)
  --limit N   events: stop after N events (default: 200)
`)
}

// flags parses --json, --track N and --limit N out of args, returning the
// remaining positional arguments.
type flags struct {
	json  bool
	track int // -1 = all tracks
	limit int
}

func parseFlags(args []string) (flags, []string, error) {
	f := flags{track: -1, limit: 200}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			f.json = true
		case a == "--track" || a == "--limit":
			if i+1 >= len(args) {
				return f, nil, fmt.Errorf("%s needs a value", a)
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return f, nil, fmt.Errorf("%s needs a non-negative integer", a)
			}
			if a == "--track" {
				f.track = n
			} else {
				f.limit = n
			}
			i++
		case strings.HasPrefix(a, "--"):
			return f, nil, fmt.Errorf("unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	return f, rest, nil
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---- info ----

type infoOut struct {
	File            string             `json:"file"`
	Format          uint16             `json:"format"`
	Division        string             `json:"division"`
	Tracks          int                `json:"tracks"`
	DurationSeconds float64            `json:"duration_seconds"`
	TempoChanges    []midi.TempoChange `json:"tempo_changes"`
}

func runInfo(args []string) error {
	fl, rest, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: midi info <file.mid> [--json]")
	}
	data, err := os.ReadFile(rest[0])
	if err != nil {
		return err
	}
	mf, err := midi.Parse(data)
	if err != nil {
		return err
	}
	div := fmt.Sprintf("%d ticks/quarter", mf.Division.TicksPerQuarter)
	if mf.Division.SMPTE {
		div = fmt.Sprintf("SMPTE %d fps, %d ticks/frame",
			mf.Division.FramesPerSecond, mf.Division.TicksPerFrame)
	}
	if fl.json {
		return emitJSON(infoOut{
			File: rest[0], Format: mf.Format, Division: div,
			Tracks: len(mf.Tracks), DurationSeconds: mf.DurationSeconds,
			TempoChanges: mf.TempoMap,
		})
	}
	fmt.Printf("File:     %s\n", rest[0])
	fmt.Printf("Format:   SMF %d\n", mf.Format)
	fmt.Printf("Division: %s\n", div)
	fmt.Printf("Tracks:   %d\n", len(mf.Tracks))
	fmt.Printf("Duration: %.3fs\n", mf.DurationSeconds)
	fmt.Println("Tempo map:")
	for _, tc := range mf.TempoMap {
		fmt.Printf("  tick %6d  %7.2f BPM  (%d us/qn, %.3fs)\n",
			tc.AbsTicks, tc.BPM, tc.Microseconds, tc.AbsSeconds)
	}
	return nil
}

// ---- tracks ----

type trackOut struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Events      int     `json:"events"`
	NoteOns     int     `json:"note_ons"`
	NoteOffs    int     `json:"note_offs"`
	LowestNote  string  `json:"lowest_note,omitempty"`
	HighestNote string  `json:"highest_note,omitempty"`
	Channels    []int   `json:"channels"`
	DurationSec float64 `json:"duration_seconds"`
}

func runTracks(args []string) error {
	fl, rest, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: midi tracks <file.mid> [--json]")
	}
	data, err := os.ReadFile(rest[0])
	if err != nil {
		return err
	}
	mf, err := midi.Parse(data)
	if err != nil {
		return err
	}
	var out []trackOut
	for _, tr := range mf.Tracks {
		var chans []int
		for c := range tr.Channels {
			chans = append(chans, c)
		}
		sort.Ints(chans)
		dur := 0.0
		if len(tr.Events) > 0 {
			dur = tr.Events[len(tr.Events)-1].AbsSeconds
		}
		t := trackOut{
			Index: tr.Index, Name: tr.Name, Events: len(tr.Events),
			NoteOns: tr.NoteOns, NoteOffs: tr.NoteOffs,
			Channels: chans, DurationSec: dur,
		}
		if tr.LowestNote >= 0 {
			t.LowestNote = midi.NoteName(byte(tr.LowestNote))
			t.HighestNote = midi.NoteName(byte(tr.HighestNote))
		}
		out = append(out, t)
	}
	if fl.json {
		return emitJSON(out)
	}
	fmt.Printf("%-5s %-20s %7s %7s %7s %-12s %s\n",
		"#", "NAME", "EVENTS", "NOTE ON", "NOTE OFF", "RANGE", "CHANNELS")
	for _, t := range out {
		rng := "-"
		if t.LowestNote != "" {
			rng = t.LowestNote + ".." + t.HighestNote
		}
		fmt.Printf("%-5d %-20s %7d %7d %7d %-12s %v\n",
			t.Index, t.Name, t.Events, t.NoteOns, t.NoteOffs, rng, t.Channels)
	}
	return nil
}

// ---- events ----

type eventOut struct {
	Track    int     `json:"track"`
	AbsTicks uint64  `json:"abs_ticks"`
	AbsSec   float64 `json:"abs_seconds"`
	Summary  string  `json:"summary"`
}

func describeEvent(ev midi.Event) string {
	switch ev.Kind {
	case midi.KindMeta:
		switch ev.MetaType {
		case midi.MetaTrackName:
			return fmt.Sprintf("Track Name %q", string(ev.MetaData))
		case midi.MetaTempo:
			if len(ev.MetaData) == 3 {
				us := uint32(ev.MetaData[0])<<16 | uint32(ev.MetaData[1])<<8 | uint32(ev.MetaData[2])
				return fmt.Sprintf("Tempo %.2f BPM (%d us/qn)", 60_000_000/float64(us), us)
			}
			return "Tempo (malformed)"
		case midi.MetaTimeSignature:
			if len(ev.MetaData) >= 4 {
				return fmt.Sprintf("Time Signature %d/%d", ev.MetaData[0], 1<<ev.MetaData[1])
			}
			return "Time Signature (malformed)"
		case midi.MetaKeySignature:
			if len(ev.MetaData) >= 2 {
				return fmt.Sprintf("Key Signature sf=%d mi=%d", int8(ev.MetaData[0]), ev.MetaData[1])
			}
			return "Key Signature (malformed)"
		case midi.MetaEndOfTrack:
			return "End of Track"
		case midi.MetaText, midi.MetaCopyright, midi.MetaLyric, midi.MetaMarker, midi.MetaCuePoint:
			return fmt.Sprintf("Meta 0x%02X %q", ev.MetaType, string(ev.MetaData))
		default:
			return fmt.Sprintf("Meta 0x%02X (%d bytes)", ev.MetaType, len(ev.MetaData))
		}
	case midi.KindSysEx:
		return fmt.Sprintf("SysEx (%d bytes)", len(ev.SysEx))
	default:
		switch ev.Status & 0xF0 {
		case midi.StatusNoteOn:
			if ev.Data2 == 0 {
				return fmt.Sprintf("Note Off  ch%d %s vel 0", ev.Channel, midi.NoteName(ev.Data1))
			}
			return fmt.Sprintf("Note On   ch%d %s vel %d", ev.Channel, midi.NoteName(ev.Data1), ev.Data2)
		case midi.StatusNoteOff:
			return fmt.Sprintf("Note Off  ch%d %s vel %d", ev.Channel, midi.NoteName(ev.Data1), ev.Data2)
		case midi.StatusControlChange:
			return fmt.Sprintf("Control   ch%d CC%d = %d", ev.Channel, ev.Data1, ev.Data2)
		case midi.StatusProgramChange:
			return fmt.Sprintf("Program   ch%d #%d", ev.Channel, ev.Data1)
		case midi.StatusAftertouch:
			return fmt.Sprintf("Aftertouch ch%d %s %d", ev.Channel, midi.NoteName(ev.Data1), ev.Data2)
		case midi.StatusChannelPressure:
			return fmt.Sprintf("Chan Press ch%d %d", ev.Channel, ev.Data1)
		case midi.StatusPitchBend:
			return fmt.Sprintf("Pitch Bend ch%d %d", ev.Channel, int(ev.Data1)|int(ev.Data2)<<7)
		default:
			return fmt.Sprintf("Channel 0x%02X ch%d %d %d", ev.Status, ev.Channel, ev.Data1, ev.Data2)
		}
	}
}

func runEvents(args []string) error {
	fl, rest, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: midi events <file.mid> [--track N] [--limit N] [--json]")
	}
	data, err := os.ReadFile(rest[0])
	if err != nil {
		return err
	}
	mf, err := midi.Parse(data)
	if err != nil {
		return err
	}
	if fl.track >= len(mf.Tracks) {
		return fmt.Errorf("track %d does not exist (file has %d tracks)", fl.track, len(mf.Tracks))
	}
	var out []eventOut
	shown := 0
	for _, tr := range mf.Tracks {
		if fl.track >= 0 && tr.Index != fl.track {
			continue
		}
		for _, ev := range tr.Events {
			if shown >= fl.limit {
				break
			}
			out = append(out, eventOut{
				Track: tr.Index, AbsTicks: ev.AbsTicks,
				AbsSec: ev.AbsSeconds, Summary: describeEvent(ev),
			})
			shown++
		}
	}
	if fl.json {
		return emitJSON(out)
	}
	for _, e := range out {
		fmt.Printf("t%-3d %8.3fs  tick %6d  %s\n", e.Track, e.AbsSec, e.AbsTicks, e.Summary)
	}
	if shown >= fl.limit {
		fmt.Fprintf(os.Stderr, "(limited to %d events; use --limit to see more)\n", fl.limit)
	}
	return nil
}
