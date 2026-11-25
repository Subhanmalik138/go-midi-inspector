# go-midi-inspector

A zero-dependency CLI that inspects Standard MIDI Files (`.mid` / SMF) — the
kind every DAW exports. It parses the file by hand: the `MThd` header, `MTrk`
chunks, variable-length quantities, running status, channel voice messages,
system-exclusive messages and meta events, then turns tick positions into
wall-clock seconds using the file's tempo map.

## Why this exists

MIDI files look opaque, but the format is a compact binary protocol worth
understanding: delta times are variable-length quantities, a status byte can
be omitted when it repeats (running status), and tempo is a separate event
stream that has to be merged across tracks before any tick means anything in
seconds. This tool is a small, readable implementation of all of that, with
no dependencies.

## Install

```sh
go build -o midi .
```

Requires Go 1.23+.

## Usage

```sh
# File overview: format, division, track count, tempo map, duration
midi info song.mid

# One line per track: name, events, notes, note range, channels used
midi tracks song.mid

# Event listing in tick order, with wall-clock times
midi events song.mid
midi events song.mid --track 1 --limit 50

# Machine-readable output for any command
midi info song.mid --json
```

Example output:

```
$ midi info demo.mid
File:     demo.mid
Format:   SMF 1
Division: 480 ticks/quarter
Tracks:   2
Duration: 1.000s
Tempo map:
  tick      0   120.00 BPM  (500000 us/qn, 0.000s)

$ midi tracks demo.mid
#     NAME                 EVENTS NOTE ON NOTE OFF RANGE        CHANNELS
0     Conductor                 4       0       0 -            []
1     Piano                     7       2       2 C4..E4       [0]

$ midi events demo.mid --track 1
t1    0.000s  tick      0  Track Name "Piano"
t1    0.000s  tick      0  Program   ch0 #0
t1    0.000s  tick      0  Note On   ch0 C4 vel 100
t1    0.500s  tick    480  Note Off  ch0 C4 vel 0
t1    0.500s  tick    480  Note On   ch0 E4 vel 100
t1    1.000s  tick    960  Note Off  ch0 E4 vel 0
t1    1.000s  tick    960  End of Track
```

## Design decisions

- **Hand-rolled parser, no MIDI library.** The format is small enough to
  parse directly, and doing so keeps the interesting parts — VLQ decoding,
  running status, the tempo map — visible in the code instead of hidden
  behind a dependency.
- **Tempo map first, seconds second.** Every event stores both its absolute
  tick and its wall-clock position. Tempo changes are collected from all
  tracks, deduplicated by tick, sorted, and only then used to resolve
  seconds, so a tempo event in track 5 still shifts the timing of track 1.
- **Running status is explicit state.** The parser tracks the last channel
  status byte per track and clears it on meta and SysEx events, exactly as
  the spec requires — a data byte with no running status is a hard error,
  not a guess.
- **SMPTE timing supported.** Files that divide time by frames per second
  instead of ticks per quarter note resolve seconds directly from the
  frame rate, with no tempo map involved.

## What it parses

- SMF formats 0, 1 and 2; ticks-per-quarter and SMPTE divisions
- Variable-length quantities (up to 4 bytes, per spec)
- Running status for channel messages
- Note on/off (including velocity-0 note on = note off), aftertouch,
  control change, program change, channel pressure, pitch bend
- Meta events: track name, text, tempo, time/key signature, end of track,
  and anything else reported by type and length
- System-exclusive messages (0xF0 / 0xF7)

## Tests

```sh
go test ./...
go vet ./...
```

Tests build small MIDI files byte-by-byte (including a running-status case
and an SMPTE division) and assert on parsed notes, tempo, duration and
error handling — no fixture files needed.
