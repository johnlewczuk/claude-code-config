#!/usr/bin/env python3
"""Light a per-key RGB keyboard from Claude Code statusline stats via OpenRGB.

Data path:
  Claude Code statusLine hook  -->  $XDG_RUNTIME_DIR/claude-rgb/<session_id>.json
                                    (payload + tmux_pane + claude_pid)
  this daemon (polls files,    -->  OpenRGB SDK server (TCP 6742)  -->  keyboard
   follows the focused tmux    -->  $XDG_RUNTIME_DIR/claude-rgb/slots/<session_id>
   pane, assigns slots)             (slot key label, read back by the statusline;
                                    bgslots/<session_id> for the Z row)
                               -->  $XDG_RUNTIME_DIR/claude-rgb/targets.json
                                    (key label -> session + pane, read by claude-goto)
                               -->  ~/.local/state/claude-rgb/{slots,bgslots}.json
                                    (persistent slot maps)

Layout (Razer Cynosa Chroma, but any keyboard with OpenRGB key names works):
  F1-F12        context window used %     green / amber / red
                (of the session in the focused tmux pane; dark when that
                pane holds no Claude session)
  1..0 - =      one key per Claude session (its "slot", stable for the
                session's lifetime, Ctrl+Alt+<key> jumps to its tmux pane):
                  orange breathing  working (mid-turn, or waiting for its
                                    background agents)
                  blue breathing    blocked on you (permission, question, ...)
                  blue (steady)     finished since you last looked at it
                  grey              idle, seen
                  off               no session in this slot
                the focused session's key is at full brightness, others dim;
                a focused idle session is white instead of grey
  numpad 1-9,0  5-hour rate limit used %  cyan  / amber / red
  Ins..PgDn     7-day rate limit used %   violet/ amber / red
  Logo          model (Fable violet, Opus amber, Sonnet teal, Haiku green)
  Space         permission mode of the focused session, read off its footer
                (accept edits purple, plan teal, auto amber, bypass red;
                manual mode dark)
  Escape        red when any limit >= 90 %
  everything else: dim base, pulsing red while an alarm is active

Ripples mode (claude-rgb-mode, Ctrl+Alt+R; kept in ~/.local/state/claude-rgb/mode)
replaces that layout with a Claude-themed look: nearly dark keys, a breathing
logo, and a coral ring from every key you press (read through X11 XInput2 raw
events) that fades out within 2 s. Sessions are still tracked, so the jump
shortcuts keep working.

The pure part (Session, Stats, SlotMap, render, render_ripples, snapshot) is
unit tested and has no OpenRGB dependency; only main() talks to the server,
tmux and X11.
"""
from __future__ import annotations

import colorsys
import enum
import json
import math
import os
import re
import select
import shutil
import socket
import struct
import subprocess
import sys
import threading
import time
from collections import deque
from dataclasses import dataclass, field
from datetime import datetime
from typing import Callable, Collection, Iterator, Mapping, Optional

Colour = tuple[int, int, int]

BASE: Colour = (0, 0, 0)
UNKNOWN: Colour = (18, 18, 18)
WHITE: Colour = (90, 90, 90)
IDLE_LOGO: Colour = (20, 20, 20)
GREEN: Colour = (0, 140, 30)
CYAN: Colour = (0, 110, 140)
VIOLET: Colour = (90, 0, 140)
AMBER: Colour = (200, 110, 0)
RED: Colour = (220, 0, 0)
ORANGE: Colour = (255, 70, 0)
BLUE: Colour = (0, 90, 255)
SLOT_GREY: Colour = (130, 130, 150)
FOCUS_IDLE: Colour = (255, 255, 255)  # white: the focused session when idle

MODEL_COLOURS: dict[str, Colour] = {
    "fable": (150, 0, 200),
    "opus": (220, 120, 0),
    "sonnet": (0, 150, 150),
    "haiku": (0, 170, 60),
}

F_ROW = [f"Key: F{i}" for i in range(1, 13)]
SLOT_LABELS = list("1234567890-=")
SLOT_KEYS = [f"Key: {k}" for k in SLOT_LABELS]
NUMPAD = [f"Key: Number Pad {k}" for k in "1234567890"]
NAV = [f"Key: {k}" for k in ("Insert", "Home", "Page Up", "Delete", "End", "Page Down")]
BG_LABELS = list("ZXCVBNM,./")        # Z row: sessions with no tmux pane (background)
BG_KEYS = [f"Key: {k}" for k in BG_LABELS]
ALARM_AT = 90
BUSY_MAX_AGE = 15 * 60   # a transcript silent this long is not mid-turn
SLOT_GRACE = 60          # a slot outlives its session's last statusline write by this
# tmux's #{pane_current_command} while Claude Code runs in the pane. Tool
# subprocesses never take the tty foreground, so this holds mid-turn too.
CLAUDE_COMMANDS = {"claude", "node"}
# Teammates are launched by the path of the versioned binary, so tmux reports
# the version (e.g. "2.1.319") as their command.
VERSIONED_BINARY_RE = re.compile(r"\d+\.\d+\.\d+")
UNFOCUSED_DIM = 0.65

# A Claude Code dialog (permission, AskUserQuestion, plan approval, folder
# trust) shows its selected option as a cursor on a numbered line, or ends
# with an Enter/Esc footer. The ordinary input prompt is "❯ " + free text.
DIALOG_RE = re.compile(r"^\s*❯\s*\d+\.\s|Enter to (confirm|select) · .*Esc to cancel", re.M)

SPACE = "Key: Space"
# The footer under the prompt names the permission mode, e.g.
# "  ⏵⏵ bypass permissions on (shift+tab to cycle) · 1 shell". It is the
# only real-time source: the statusline payload lacks the mode, and the
# transcript records it only when a prompt is submitted.
MODE_RE = re.compile(r"^\s*(?:⏵⏵|⏸)\s+(manual mode|accept edits|plan mode|auto mode|bypass permissions) on\b", re.M)
MODE_FOOTER_LINES = 4    # the footer sits this close to the bottom of the screen


class Mode(enum.Enum):
    """Claude Code permission mode (values are its permissionMode names)."""
    DEFAULT = "default"
    ACCEPT_EDITS = "acceptEdits"
    PLAN = "plan"
    AUTO = "auto"
    BYPASS = "bypassPermissions"


MODE_FOOTERS = {"manual mode": Mode.DEFAULT, "accept edits": Mode.ACCEPT_EDITS,
                "plan mode": Mode.PLAN, "auto mode": Mode.AUTO,
                "bypass permissions": Mode.BYPASS}

# Claude Code's own footer colours (autoAccept, planMode, warning, error);
# manual mode leaves the spacebar dark.
MODE_COLOURS: dict[Mode, Colour] = {
    Mode.ACCEPT_EDITS: (110, 50, 255),
    Mode.PLAN: (0, 140, 110),
    Mode.AUTO: AMBER,
    Mode.BYPASS: RED,
}


class State(enum.Enum):
    WORKING = "working"    # mid-turn
    BLOCKED = "blocked"    # mid-turn and a dialog is on screen
    UNREAD = "unread"      # turn ended since the pane was last focused
    IDLE = "idle"          # turn ended, seen


@dataclass(frozen=True)
class Session:
    id: str
    pane: Optional[str]
    transcript: Optional[str]
    file_mtime: float
    activity: float          # when its turn state last changed (see turn_from_transcript)
    fresh: bool              # statusline wrote recently
    running: bool            # its pane runs claude and no newer session shares the pane (True if no pane)
    busy: bool
    blocked: bool
    live: bool               # (fresh or blocked) and running
    agent: bool              # a background subagent (never gets a key)
    payload: dict = field(compare=False, repr=False)

    def state(self, viewed_at: float) -> State:
        if self.blocked:
            return State.BLOCKED
        if self.busy:
            return State.WORKING
        if self.pane and self.activity > viewed_at:
            return State.UNREAD
        return State.IDLE


SlotView = tuple[State, bool]      # (state, is the focused session)


@dataclass(frozen=True)
class Stats:
    ctx: Optional[float]
    h5: Optional[float]
    d7: Optional[float]
    model: Optional[str]
    has_session: bool = True      # False: focused pane holds no Claude session
    slots: tuple[Optional[SlotView], ...] = ()   # aligned with SLOT_KEYS
    bg: tuple[Optional[SlotView], ...] = ()      # aligned with BG_KEYS
    mode: Optional[Mode] = None   # permission mode of the focused session


# --- rendering ----------------------------------------------------------------

def lit_count(pct: Optional[float], n: int) -> Optional[int]:
    """How many of n keys to light for pct%. Any non-zero usage lights >= 1."""
    if pct is None:
        return None
    return max(0, min(n, math.ceil(pct / 100 * n)))


def ramp(pct: Optional[float], ok: Colour, warn: int, crit: int) -> Colour:
    if pct is None:
        return UNKNOWN
    if pct >= crit:
        return RED
    if pct >= warn:
        return AMBER
    return ok


def bar(frame: list[Colour], names: list[str], keys: list[str],
        pct: Optional[float], ok: Colour, warn: int, crit: int) -> None:
    n = lit_count(pct, len(keys))
    colour = ramp(pct, ok, warn, crit)
    for i, key in enumerate(keys):
        if key not in names:
            continue
        if n is None:
            frame[names.index(key)] = UNKNOWN
        elif i < n:
            frame[names.index(key)] = colour


def model_colour(model: Optional[str]) -> Colour:
    if not model:
        return WHITE
    m = model.lower()
    for key, colour in MODEL_COLOURS.items():
        if key in m:
            return colour
    return WHITE


def breathe(t: float, period: float) -> float:
    """0..1 cosine breathe with the given period in seconds."""
    return (1 - math.cos(2 * math.pi * t / period)) / 2


def pulse(t: float) -> Colour:
    """Slow red breathe for alarm state; t is seconds."""
    return (int(30 + breathe(t, 2.0) * 80), 0, 0)   # never dips to BASE


def scaled(colour: Colour, k: float) -> Colour:
    return tuple(int(c * k) for c in colour)


def paint(frame: list[Colour], names: list[str], keys: Collection[str], colour: Colour) -> None:
    for key in keys:
        if key in names:
            frame[names.index(key)] = colour


def slot_colour(view: Optional[SlotView], t: float) -> Colour:
    if view is None:
        return BASE
    state, focused = view
    if state is State.WORKING:
        c = scaled(ORANGE, 0.5 + 0.5 * breathe(t, 2.0))
    elif state is State.BLOCKED:
        c = scaled(BLUE, 0.5 + 0.5 * breathe(t, 1.5))
    elif state is State.UNREAD:
        c = BLUE
    elif focused:
        return FOCUS_IDLE
    else:
        c = SLOT_GREY
    return c if focused else scaled(c, UNFOCUSED_DIM)


def render(names: list[str], s: Optional[Stats], t: float) -> list[Colour]:
    """Pure: LED names + stats + time -> one colour per LED."""
    if s is None:
        frame = [BASE] * len(names)
        if "Logo" in names:
            frame[names.index("Logo")] = IDLE_LOGO
        return frame

    alarm = any(v is not None and v >= ALARM_AT for v in (s.ctx, s.h5, s.d7))
    base = pulse(t) if alarm else BASE
    frame = [base] * len(names)

    if s.has_session:
        bar(frame, names, F_ROW, s.ctx, GREEN, 60, 80)
    bar(frame, names, NUMPAD, s.h5, CYAN, 70, 90)
    bar(frame, names, NAV, s.d7, VIOLET, 50, 80)
    for key, view in zip(SLOT_KEYS, s.slots):
        paint(frame, names, [key], slot_colour(view, t))
    for key, view in zip(BG_KEYS, s.bg):
        paint(frame, names, [key], slot_colour(view, t))
    if "Logo" in names:
        frame[names.index("Logo")] = model_colour(s.model) if s.has_session else IDLE_LOGO
    if s.has_session and s.mode in MODE_COLOURS:
        paint(frame, names, [SPACE], MODE_COLOURS[s.mode])
    if alarm and "Key: Escape" in names:
        frame[names.index("Key: Escape")] = RED
    return frame


# --- ripples mode ---------------------------------------------------------------
# A look with no session info: the board rests nearly dark coral, the logo
# breathes, every key press sends a ring in that key's own colour (a rainbow
# laid diagonally across the board) that fades out, and a key glows cream while
# held, fading after release. Switched by hand (claude-rgb-mode).

RIPPLE_CORAL: Colour = (255, 85, 35)      # Claude coral as the LEDs show it
RIPPLE_CREAM: Colour = (255, 200, 140)
RIPPLE_SPEED = 8.0        # key widths per second
RIPPLE_WIDTH = 0.8        # half-thickness of a ring, in key widths
RIPPLE_LIFE = 4.0         # seconds until a ring has faded to nothing
RIPPLE_FLASH = 2.5        # how fast a released key's glow fades, per second
RAINBOW_HUES = 0.8        # share of the colour wheel the keys span: red..violet, not back to red
REST_LEVEL = 0.1          # how bright the board looks at rest, 0..1
LOGO_BREATHE = 4.0        # seconds per logo breath
# LEDs look far brighter than their drive level at the low end, so levels are
# worked out as they should look and converted with this before sending.
LED_GAMMA = 2.2


def led_level(look: float) -> float:
    """How hard to drive an LED (0..1) for it to look this bright (0..1)."""
    return max(0.0, min(1.0, look)) ** LED_GAMMA


def shade(colour: Colour, look: float) -> Colour:
    return tuple(round(c * led_level(look)) for c in colour)


RIPPLE_REST: Colour = shade(RIPPLE_CORAL, REST_LEVEL)

# Linux evdev key codes (linux/input-event-codes.h) -> Cynosa LED names, as
# runs of consecutive codes; None skips a code with no key on this board. X11
# keycodes are these plus 8. Physical keys, so the layout setting is irrelevant.
EVDEV_RUNS: list[tuple[int, list[Optional[str]]]] = [
    (1, ["Escape", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "-", "=",
         "Backspace", "Tab", "Q", "W", "E", "R", "T", "Y", "U", "I", "O", "P",
         "[", "]", "Enter", "Left Control", "A", "S", "D", "F", "G", "H", "J",
         "K", "L", ";", "'", "`", "Left Shift", "\\ (ANSI)", "Z", "X", "C", "V",
         "B", "N", "M", ",", ".", "/", "Right Shift", "Number Pad *", "Left Alt",
         "Space", "Caps Lock", "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8",
         "F9", "F10", "Num Lock", "Scroll Lock", "Number Pad 7", "Number Pad 8",
         "Number Pad 9", "Number Pad -", "Number Pad 4", "Number Pad 5",
         "Number Pad 6", "Number Pad +", "Number Pad 1", "Number Pad 2",
         "Number Pad 3", "Number Pad 0", "Number Pad ."]),
    (87, ["F11", "F12"]),
    (96, ["Number Pad Enter", "Right Control", "Number Pad /", "Print Screen",
          "Right Alt", None, "Home", "Up Arrow", "Page Up", "Left Arrow",
          "Right Arrow", "End", "Down Arrow", "Page Down", "Insert", "Delete"]),
    (119, ["Pause/Break"]),
    (125, ["Left Windows", None, "Menu"]),
]
EVDEV_KEYS: dict[int, str] = {code + i: f"Key: {name}"
                              for code, run in EVDEV_RUNS
                              for i, name in enumerate(run) if name}
X_KEYCODE_OFFSET = 8

# The Cynosa's keys as they sit on a US (ANSI) full-size board, row by row from
# the left edge: a name is a key one key wide, (name, width) a wider key, a bare
# number an empty gap; widths are in key widths. The function row has half a
# key of space below it. The tall Number Pad + and Enter span two rows.
LAYOUT_ROWS: list[tuple[float, list[str | float | tuple[str, float]]]] = [
    (0.0, ["Escape", 1.0, "F1", "F2", "F3", "F4", 0.5, "F5", "F6", "F7", "F8", 0.5,
           "F9", "F10", "F11", "F12", 0.25, "Print Screen", "Scroll Lock", "Pause/Break"]),
    (1.5, ["`", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "-", "=", ("Backspace", 2.0),
           0.25, "Insert", "Home", "Page Up",
           0.25, "Num Lock", "Number Pad /", "Number Pad *", "Number Pad -"]),
    (2.5, [("Tab", 1.5), "Q", "W", "E", "R", "T", "Y", "U", "I", "O", "P", "[", "]",
           ("\\ (ANSI)", 1.5), 0.25, "Delete", "End", "Page Down",
           0.25, "Number Pad 7", "Number Pad 8", "Number Pad 9", "Number Pad +"]),
    (3.5, [("Caps Lock", 1.75), "A", "S", "D", "F", "G", "H", "J", "K", "L", ";", "'",
           ("Enter", 2.25), 3.5, "Number Pad 4", "Number Pad 5", "Number Pad 6"]),
    (4.5, [("Left Shift", 2.25), "Z", "X", "C", "V", "B", "N", "M", ",", ".", "/",
           ("Right Shift", 2.75), 1.25, "Up Arrow",
           1.25, "Number Pad 1", "Number Pad 2", "Number Pad 3", "Number Pad Enter"]),
    (5.5, [("Left Control", 1.25), ("Left Windows", 1.25), ("Left Alt", 1.25), ("Space", 6.25),
           ("Right Alt", 1.25), ("Right Fn", 1.25), ("Menu", 1.25), ("Right Control", 1.25),
           0.25, "Left Arrow", "Down Arrow", "Right Arrow",
           0.25, ("Number Pad 0", 2.0), "Number Pad ."]),
]
TALL_KEYS = {"Number Pad +", "Number Pad Enter"}


def layout_positions(rows: list[tuple[float, list[str | float | tuple[str, float]]]]
                     ) -> dict[str, tuple[float, float]]:
    """Pure: LAYOUT_ROWS -> LED name -> (x, y) of the key's middle, in key widths."""
    pos = {}
    for y, row in rows:
        x = 0.0
        for item in row:
            if isinstance(item, float):
                x += item
                continue
            name, width = (item, 1.0) if isinstance(item, str) else item
            pos[f"Key: {name}"] = (x + width / 2, y + (0.5 if name in TALL_KEYS else 0.0))
            x += width
    return pos


KEY_POSITIONS = layout_positions(LAYOUT_ROWS)

Press = tuple[str, float]   # LED name, monotonic time of the press


def raw_keycode(data: bytes) -> int:
    """X keycode of an XInput2 raw key event, from python-xlib's undecoded
    payload (deviceid u16, time u32, then detail u32)."""
    return struct.unpack_from("=I", data, 6)[0]


def led_for_x_keycode(keycode: int) -> Optional[str]:
    return EVDEV_KEYS.get(keycode - X_KEYCODE_OFFSET)


def mix(a: Colour, b: Colour, k: float) -> Colour:
    k = max(0.0, min(1.0, k))
    return tuple(x + (y - x) * k for x, y in zip(a, b))


def key_colours(pos: Mapping[str, tuple[float, float]]) -> dict[str, Colour]:
    """Pure: key positions -> each key's own ripple colour.

    A rainbow laid diagonally, red at the top left to violet at the bottom
    right, so neighbouring keys differ a little and the board reads as one sweep.
    Each channel is converted with led_level, like brightness: a little green
    drive already looks like a lot, which would turn red into orange."""
    along = {name: x + y for name, (x, y) in pos.items()}
    low = min(along.values(), default=0.0)
    span = (max(along.values(), default=0.0) - low) or 1.0
    return {name: tuple(round(255 * led_level(x))
                        for x in colorsys.hsv_to_rgb(RAINBOW_HUES * (a - low) / span, 1.0, 1.0))
            for name, a in along.items()}


def render_ripples(names: list[str], pos: Mapping[str, tuple[float, float]],
                   presses: Collection[Press], held: Collection[str],
                   releases: Collection[Press], t: float) -> list[Colour]:
    """Pure: LED names + key positions (x, y in key widths) + recent key activity + time
    -> one colour per LED.

    Each press lights a ring of radius age * RIPPLE_SPEED around its key, in
    that key's colour, fading to nothing by RIPPLE_LIFE. Where rings overlap
    their colours blend by how bright each is there. A held key glows cream at
    full brightness; after release the glow fades out the same way."""
    colours = key_colours(pos)
    rings = [(pos[name], t - at, colours[name]) for name, at in presses
             if name in pos and 0 <= t - at < RIPPLE_LIFE]
    glow: dict[str, float] = {}
    for name, at in releases:
        age = t - at
        if 0 <= age < RIPPLE_LIFE:
            glow[name] = max(glow.get(name, 0.0),
                             math.exp(-age * RIPPLE_FLASH) * (1 - age / RIPPLE_LIFE))
    glow.update(dict.fromkeys(held, 1.0))
    frame = []
    for name in names:
        if name == "Logo":
            frame.append(shade(RIPPLE_CORAL, 0.35 + 0.65 * breathe(t, LOGO_BREATHE)))
            continue
        here = pos.get(name)
        if here is None:
            frame.append(RIPPLE_REST)
            continue
        ring = 0.0
        tint = [0.0, 0.0, 0.0]
        for (x, y), age, colour in rings:
            d = math.hypot(here[0] - x, here[1] - y)
            w = math.exp(-((d - age * RIPPLE_SPEED) / RIPPLE_WIDTH) ** 2) * (1 - age / RIPPLE_LIFE)
            ring += w
            tint = [x + w * y for x, y in zip(tint, colour)]
        # The resting coral blends in only while the rings here are fainter than
        # the rest level: a faint tail tints the key a little, a ring owns it.
        rest = max(0.0, REST_LEVEL - ring)
        tint = [x + rest * y for x, y in zip(tint, RIPPLE_CORAL)]
        base = tuple(x / (rest + ring) for x in tint)
        flash = glow.get(name, 0.0)
        look = max(REST_LEVEL, min(1.0, ring + flash))
        frame.append(shade(mix(base, RIPPLE_CREAM, flash), look))
    return frame


# --- transcript ----------------------------------------------------------------

LOCAL_CAVEAT = "<local-command-caveat>"
LOCAL_OUTPUT = ("<local-command-stdout>", "<local-command-stderr>")


def user_text(e: dict) -> str:
    content = (e.get("message") or {}).get("content")
    return content if isinstance(content, str) else ""


def stamp(e: dict) -> Optional[float]:
    try:
        return datetime.fromisoformat(e["timestamp"]).timestamp()
    except (KeyError, TypeError, ValueError):
        return None


def turn_rows(tail: bytes) -> Iterator[dict]:
    """The rows of a transcript tail that may say something about turn
    state, newest first. Metadata rows (ai-title, mode, api-request,
    artifact ledgers, ...) are dropped, and so are user rows that are no
    prompt: a compaction summary (compaction happens both between turns and
    mid-turn) and a local slash command's output."""
    for line in reversed(tail.splitlines()):
        try:
            e = json.loads(line)
        except ValueError:
            continue
        kind = e.get("type")
        if (kind in ("assistant", "continued-in")
                or kind == "system" and e.get("subtype") == "turn_duration"
                or kind == "user" and not e.get("isCompactSummary")
                and not user_text(e).startswith(LOCAL_OUTPUT)):
            yield e


def turn_from_transcript(path: Optional[str]) -> tuple[bool, Optional[float]]:
    """(mid-turn?, when) from the last transcript row that says anything
    about turn state:
      user prompt / tool_result  -> Claude owes a response      -> busy
      assistant (tool_use etc.)  -> tool running or streaming   -> busy
      system/turn_duration       -> turn finished               -> idle,
                                    unless pendingBackgroundAgentCount says
                                    background agents still run -> busy
      '[Request interrupted ...' -> user hit Esc                -> idle
      continued-in               -> session handed off elsewhere -> idle
    A local slash command (/model, /config, ...) is a caveat row followed
    by a user row with the command; it runs without Claude and is skipped.
    `when` is that row's own timestamp, None if it has none: the file's
    mtime is no use, as Claude Code appends rows to sessions idle for hours."""
    if not path:
        return False, None
    try:
        with open(path, "rb") as fh:
            fh.seek(max(0, os.fstat(fh.fileno()).st_size - 512 * 1024))
            tail = fh.read()
    except OSError:
        return False, None
    rows = turn_rows(tail)
    for e in rows:
        kind = e.get("type")
        if kind == "system":
            return bool(e.get("pendingBackgroundAgentCount")), stamp(e)
        if kind == "continued-in":
            return False, stamp(e)
        if kind == "assistant":
            return True, stamp(e)
        text = user_text(e)
        if text.startswith(LOCAL_CAVEAT):
            continue
        older = next(rows, None)
        if older is not None and user_text(older).startswith(LOCAL_CAVEAT):
            continue
        return not text.startswith("[Request interrupted"), stamp(e)
    return False, None


def subagent_activity(transcript: Optional[str]) -> float:
    """Newest mtime of a session's subagent transcripts, which Claude Code
    keeps in <transcript without .jsonl>/subagents/; 0 if there are none."""
    if not transcript:
        return 0.0
    try:
        with os.scandir(os.path.join(transcript.removesuffix(".jsonl"), "subagents")) as it:
            return max((e.stat().st_mtime for e in it if e.name.endswith(".jsonl")), default=0.0)
    except OSError:
        return 0.0


def has_dialog(screen: str) -> bool:
    return DIALOG_RE.search(screen) is not None


def mode_from_screen(screen: str) -> Optional[Mode]:
    """The permission mode named in the footer at the bottom of a Claude Code
    screen, or None when no footer is visible (a dialog replaces it). Only the
    last few lines count, so a footer quoted in the conversation is ignored."""
    bottom = [l for l in screen.splitlines() if l.strip()][-MODE_FOOTER_LINES:]
    found = MODE_RE.findall("\n".join(bottom))
    return MODE_FOOTERS[found[-1]] if found else None


# --- sessions -------------------------------------------------------------------

def parent_pid(pid: int) -> Optional[int]:
    try:
        with open(f"/proc/{pid}/stat") as fh:
            return int(fh.read().rsplit(")", 1)[1].split()[1])
    except (OSError, ValueError, IndexError):
        return None


def resolve_pane(pid: Optional[int], pane_pids: Mapping[int, str],
                 parent_of: Callable[[int], Optional[int]] = parent_pid) -> Optional[str]:
    """Walk up from a claude pid to the shell a tmux pane started
    (`pane_pid`), giving the pane. Forks and subagents are child processes
    without TMUX_PANE in their environment, so this is how they are placed."""
    seen = 0
    while pid and pid > 1 and seen < 32:
        if pid in pane_pids:
            return pane_pids[pid]
        pid = parent_of(pid)
        seen += 1
    return None


def is_agent(payload: dict) -> bool:
    """Background subagents run inside the parent's process with their own
    session id; they must not claim the parent's pane. A teammate's payload
    names an agent too, but the hook saw TMUX_PANE: it is a separate process
    with a pane of its own, so an ordinary session."""
    return not payload.get("tmux_pane") and bool(payload.get("agent_type") or payload.get("agent"))


@dataclass(frozen=True)
class Candidate:
    """A session that might be the one a pane is displaying."""
    id: str
    name: Optional[str]
    fresh: bool
    tagged: bool        # the hook saw TMUX_PANE: the pane's own foreground claude
    mid_turn: bool
    mtime: float


def pane_owner(cands: list[Candidate], screen: str) -> Candidate:
    """Which of several sessions sharing a pane is the one on screen.
    Claude Code can host extra sessions (forks, /resume targets) in
    background ptys and attach the pane to one of them; all of them keep
    writing their mirror file every second, so "newest file" would flap.
      1. An attached session shows its name in the separator above the
         prompt; a fork's name extends its parent's, so the longest name
         found on screen wins.
      2. A dialog on screen belongs to the displayed session, whose
         statusline is frozen: the stale mid-turn one.
      3. Otherwise the pane's own foreground claude (tagged), if fresh.
      4. Otherwise the newest fresh one, else the newest."""
    named = [c for c in cands if c.name and c.name[:40] in screen]
    if named:
        return max(named, key=lambda c: len(c.name))
    if has_dialog(screen):
        frozen = [c for c in cands if c.mid_turn and not c.fresh]
        if frozen:
            return max(frozen, key=lambda c: c.mtime)
    fresh = [c for c in cands if c.fresh]
    tagged = [c for c in fresh if c.tagged]
    if tagged:
        return max(tagged, key=lambda c: c.mtime)
    return max(fresh or cands, key=lambda c: c.mtime)


def load_sessions(state_dir: str, max_age: float, now: float,
                  screen_of: Callable[[str], str], panes: Mapping[str, str],
                  pane_pids: Mapping[int, str] = {},
                  parent_of: Callable[[int], Optional[int]] = parent_pid,
                  pane_tags: Mapping[str, str] = {}) -> list[Session]:
    """One Session per mirror file. A half-written file (statusline
    mid-write) is skipped for this tick. `panes` maps every existing tmux
    pane id to its foreground command, `pane_pids` each pane's shell pid to
    the pane id, `pane_tags` pane id -> the session claude-goto attached
    there, and screen_of(pane) returns the pane's visible text (it is only
    called for panes that need it). A session's pane is its tmux_pane tag if
    the hook had one, else a pane tagged with its id, else the pane found by
    walking up from claude_pid; background agents get none.

    A session is `fresh` when the statusline wrote recently, and `running`
    when its pane still runs claude AND it is the session that pane displays
    (pane_owner): after /exit the pane lives on as a shell and the last
    mirror file stays fresh for a while; /clear, /resume or a restart in the
    same pane leaves the previous session's file behind; and background-
    hosted sessions share the pane with the foreground one. It is `blocked`
    when running, its transcript ends mid-turn and a dialog is on screen;
    Claude Code freezes the statusline while a dialog is up, so a blocked
    session is usually NOT fresh and must be recognised this way."""
    try:
        files = [f for f in os.listdir(state_dir) if f.endswith(".json")]
    except FileNotFoundError:
        return []
    loaded = []
    for f in files:
        path = os.path.join(state_dir, f)
        try:
            with open(path) as fh:
                loaded.append((f, os.stat(path).st_mtime, json.load(fh)))
        except (OSError, ValueError):
            continue
    attached = {sid: pane for pane, sid in pane_tags.items()}

    def pane_of(p: dict) -> Optional[str]:
        if is_agent(p):
            return None
        return (p.get("tmux_pane") or attached.get(p.get("session_id") or "")
                or resolve_pane(p.get("claude_pid"), pane_pids, parent_of))

    rows = []
    for f, mtime, p in loaded:
        if not isinstance(p, dict) or not p.get("session_id"):
            continue    # not a mirror file (targets.json lives here too)
        transcript = p.get("transcript_path")
        mid_turn, activity = turn_from_transcript(transcript)
        if activity is None:
            try:
                activity = os.stat(transcript).st_mtime
            except (TypeError, OSError):
                activity = mtime
        if mid_turn:
            # Subagents (foreground or background) work while the session's
            # own transcript stays silent; their writes are its activity.
            activity = max(activity, subagent_activity(transcript))
        rows.append(dict(
            id=p["session_id"], pane=pane_of(p), transcript=transcript,
            mtime=mtime, activity=activity, fresh=now - mtime <= max_age,
            mid_turn=mid_turn, payload=p))

    # One screen capture per pane that needs one: several sessions to choose
    # between, or a mid-turn session to check for a dialog.
    screens: dict[str, str] = {}
    by_pane: dict[str, list[dict]] = {}
    for r in rows:
        if r["pane"] and panes.get(r["pane"]) in CLAUDE_COMMANDS:
            by_pane.setdefault(r["pane"], []).append(r)
    owner: dict[str, str] = {}
    for pane, rs in by_pane.items():
        if len(rs) > 1 or any(r["mid_turn"] for r in rs):
            screens[pane] = screen_of(pane)
        if len(rs) == 1:
            owner[pane] = rs[0]["id"]
        else:
            cands = [Candidate(r["id"], r["payload"].get("session_name"), r["fresh"],
                               bool(r["payload"].get("tmux_pane")), r["mid_turn"], r["mtime"]) for r in rs]
            owner[pane] = pane_owner(cands, screens[pane]).id

    out = []
    for r in rows:
        pane = r["pane"]
        running = pane is None or owner.get(pane) == r["id"]
        blocked = bool(pane) and running and r["mid_turn"] and has_dialog(screens.get(pane, ""))
        out.append(Session(
            id=r["id"], pane=pane, transcript=r["transcript"],
            file_mtime=r["mtime"], activity=r["activity"], fresh=r["fresh"], running=running,
            busy=r["mid_turn"] and (blocked or now - r["activity"] <= BUSY_MAX_AGE),
            blocked=blocked, live=(r["fresh"] or blocked) and running,
            agent=is_agent(r["payload"]), payload=r["payload"]))
    return out


def rank(sessions: list[Session], focus: Optional[str]) -> list[Session]:
    """Focused session first, then by most recent activity. (The mirror
    file's mtime is useless for this: every session rewrites it every second.)"""
    ranked = sorted(sessions, key=lambda s: s.activity, reverse=True)
    if focus is not None:
        ranked.sort(key=lambda s: s.pane != focus)   # stable: focused to front
    return ranked


@dataclass
class LimitClock:
    """Latest rate-limit reading from any session. Rate limits are account-wide,
    but a session learns them only from its own API responses, so one payload
    can carry numbers hours old, and no payload says when they were read. The
    daemon dates each session's reading by when it first appeared or last
    changed in its view (it polls at 2 Hz, close to when the statusline runs)."""
    readings: dict[str, tuple[float, float, Optional[float], Optional[float]]] = field(default_factory=dict)
    # session id -> (wall time seen, transcript activity, five_hour %, seven_day %)

    def observe(self, sessions: list[Session], wall: float) -> None:
        for s in sessions:
            rl = s.payload.get("rate_limits") or {}
            h5 = (rl.get("five_hour") or {}).get("used_percentage")
            d7 = (rl.get("seven_day") or {}).get("used_percentage")
            if h5 is None and d7 is None:
                continue
            seen = self.readings.get(s.id)
            if seen is None or seen[2:] != (h5, d7):
                self.readings[s.id] = (wall, s.activity, h5, d7)

    def latest(self) -> tuple[Optional[float], Optional[float]]:
        """(five_hour %, seven_day %) of the most recently dated reading. Ties
        (sessions first seen in the same tick) go to the most recently active."""
        if not self.readings:
            return None, None
        _, _, h5, d7 = max(self.readings.values(), key=lambda r: r[:2])
        return h5, d7


def stats_from(sessions: list[Session], focus: Optional[str],
               slots: tuple[Optional[SlotView], ...] = (),
               limits: tuple[Optional[float], Optional[float]] = (None, None),
               bg: tuple[Optional[SlotView], ...] = (),
               mode: Optional[Mode] = None) -> Optional[Stats]:
    """Context %, model and permission mode describe the session in the
    focused tmux pane. A focused pane with no Claude session shows none
    (has_session=False) rather than someone else's; outside tmux (focus None)
    the most recently active session stands in. `limits` is the account-wide
    (five_hour, seven_day) reading, supplied by the caller's LimitClock, and
    `mode` the one read off the focused pane's screen."""
    live = [s for s in sessions if s.live]
    if not live:
        return None
    ranked = rank(live, focus)
    active = ranked[0]
    has_session = focus is None or active.pane == focus
    h5, d7 = limits
    if not has_session:
        return Stats(ctx=None, h5=h5, d7=d7, model=None, has_session=False, slots=slots, bg=bg)
    return Stats(
        ctx=(active.payload.get("context_window") or {}).get("used_percentage"),
        h5=h5, d7=d7,
        model=(active.payload.get("model") or {}).get("display_name"),
        slots=slots, bg=bg, mode=mode,
    )


def pick_stats(state_dir: str, max_age: float, now: Optional[float] = None,
               focus: Optional[str] = None,
               screen_of: Callable[[str], str] = lambda pane: "",
               live_panes: Mapping[str, str] = {}) -> Optional[Stats]:
    """Convenience: load + stats_from, without slots. One observation only, so
    the limits come from the most recently active session that reports them."""
    now = time.time() if now is None else now
    sessions = load_sessions(state_dir, max_age, now, screen_of, live_panes)
    clock = LimitClock()
    clock.observe(sessions, now)
    return stats_from(sessions, focus, limits=clock.latest())


# --- slots ------------------------------------------------------------------------

class SlotMap:
    """Stable session -> key assignment, persisted so a daemon restart does
    not reshuffle what you have learned. First free slot wins; a slot is
    released when its session is no longer held."""

    def __init__(self, path: Optional[str] = None, labels: list[str] = SLOT_LABELS):
        self.path = path
        self.labels = labels
        self.slots: dict[str, str] = {}      # label -> session id
        if path:
            try:
                with open(path) as fh:
                    data = json.load(fh).get("slots", {})
                self.slots = {k: v for k, v in data.items() if k in labels and isinstance(v, str)}
            except (OSError, ValueError, AttributeError):
                pass

    def update(self, held: Collection[str]) -> bool:
        """Release slots of sessions not in `held`, assign free slots to new
        ones (in the given order). Returns True if anything changed."""
        before = dict(self.slots)
        self.slots = {k: v for k, v in self.slots.items() if v in held}
        taken = set(self.slots.values())
        free = [k for k in self.labels if k not in self.slots]
        for sid in held:
            if sid in taken or not free:
                continue
            self.slots[free.pop(0)] = sid
            taken.add(sid)
        changed = self.slots != before
        if changed and self.path:
            self.save()
        return changed

    def label_of(self, session_id: str) -> Optional[str]:
        for k, v in self.slots.items():
            if v == session_id:
                return k
        return None

    def session_at(self, label: str) -> Optional[str]:
        return self.slots.get(label)

    def save(self) -> None:
        os.makedirs(os.path.dirname(self.path), exist_ok=True)
        tmp = f"{self.path}.{os.getpid()}"
        with open(tmp, "w") as fh:
            json.dump({"slots": self.slots}, fh, indent=1)
        os.replace(tmp, self.path)


def held_ids(sessions: list[Session], now: float, panes: Mapping[str, str]) -> list[str]:
    """Sessions that keep (or get) a slot: live ones, plus running ones that
    went quiet within the grace period so a statusline hiccup does not
    reshuffle keys. A session whose pane is gone or no longer runs claude is
    released at once. Sessions without a tmux pane (subagents, forks, claude
    outside tmux) cannot be jumped to and get no slot. Ordered oldest first
    so allocation is deterministic."""
    keep = [s for s in sessions
            if s.pane and (s.live or (s.running and now - s.file_mtime <= SLOT_GRACE))]
    return [s.id for s in sorted(keep, key=lambda s: s.file_mtime)]


def slot_views(sessions: list[Session], slots: SlotMap, focus: Optional[str],
               viewed: dict[str, float], default_viewed: float) -> tuple[Optional[SlotView], ...]:
    """What each slot key shows. `viewed` maps pane -> when it was last
    focused; a pane never focused since the daemon started counts as viewed
    at `default_viewed` (daemon start), so a restart does not light every
    idle session unread-blue. A session in its grace period (not live) is idle."""
    by_id = {s.id: s for s in sessions}
    out: list[Optional[SlotView]] = []
    for label in SLOT_LABELS:
        s = by_id.get(slots.session_at(label) or "")
        if s is None:
            out.append(None)
        elif not s.live:
            out.append((State.IDLE, False))
        else:
            out.append((s.state(viewed.get(s.pane or "", default_viewed)), s.pane == focus))
    return tuple(out)


def is_background(s: Session, panes: Mapping[str, str]) -> bool:
    """Not on screen: no tmux pane at all, or its pane still runs claude but
    displays another session (the one that was paused when a background
    session was put into the pane). A pane that went back to a shell (after
    /exit) does not count."""
    if s.agent:
        return False
    return s.pane is None or (not s.running and panes.get(s.pane) in CLAUDE_COMMANDS)


def held_bg_ids(sessions: list[Session], now: float, panes: Mapping[str, str]) -> list[str]:
    """Background sessions that keep or get a Z-row key: live or busy ones,
    plus those quiet within the grace period. A displaced session's statusline
    freezes while another session holds its pane, so busy is what keeps it
    working on the keyboard. Ordered oldest first so allocation is stable."""
    keep = [s for s in sessions
            if is_background(s, panes) and (s.live or s.busy or now - s.file_mtime <= SLOT_GRACE)]
    return [s.id for s in sorted(keep, key=lambda s: s.file_mtime)]


def bg_views(sessions: list[Session], bgslots: SlotMap) -> tuple[Optional[SlotView], ...]:
    """What each Z-row key shows: working while mid-turn, otherwise idle. A
    background session never has a pane it could be unread on or blocked in,
    so blue (steady or breathing) does not occur on this row."""
    by_id = {s.id: s for s in sessions}
    out: list[Optional[SlotView]] = []
    for label in BG_LABELS:
        s = by_id.get(bgslots.session_at(label) or "")
        if s is None:
            out.append(None)
        else:
            out.append((State.WORKING if s.busy else State.IDLE, False))
    return tuple(out)


def write_slot_labels(state_dir: str, slots: SlotMap, subdir: str = "slots") -> None:
    """One tiny file per session holding its key label, for the statusline
    to read back (a bash `read`, no fork). Removed when the slot is released."""
    d = os.path.join(state_dir, subdir)
    os.makedirs(d, exist_ok=True)
    want = {sid: label for label, sid in slots.slots.items()}
    for f in os.listdir(d):
        if f not in want:
            try:
                os.remove(os.path.join(d, f))
            except OSError:
                pass
    for sid, label in want.items():
        path = os.path.join(d, sid)
        try:
            with open(path) as fh:
                if fh.read().strip() == label:
                    continue
        except OSError:
            pass
        with open(path, "w") as fh:
            fh.write(label + "\n")


def jump_targets(sessions: list[Session], *maps: SlotMap) -> dict[str, dict]:
    """label -> {"session": id, "pane": pane id or None} for every assigned
    key, read by claude-goto. A null pane is a detached background session,
    which claude-goto opens with `claude attach` in a new tmux window."""
    pane_of = {s.id: s.pane for s in sessions}
    return {label: {"session": sid, "pane": pane_of.get(sid)}
            for m in maps for label, sid in m.slots.items()}


def write_targets(state_dir: str, targets: dict[str, dict]) -> None:
    """Atomically replace targets.json, only when it changed."""
    path = os.path.join(state_dir, "targets.json")
    try:
        with open(path) as fh:
            if json.load(fh) == targets:
                return
    except (OSError, ValueError):
        pass
    tmp = f"{path}.{os.getpid()}"
    with open(tmp, "w") as fh:
        json.dump(targets, fh)
    os.replace(tmp, path)


# --- tmux ------------------------------------------------------------------------

def focused_pane() -> Optional[str]:
    """Active pane of the most recently used tmux client, e.g. "%222", or
    None when tmux is absent or has no clients."""
    if not shutil.which("tmux"):
        return None
    try:
        r = subprocess.run(["tmux", "display-message", "-p", "#{pane_id}"],
                           capture_output=True, text=True, timeout=1)
    except (OSError, subprocess.SubprocessError):
        return None
    pane = r.stdout.strip()
    return pane if r.returncode == 0 and pane.startswith("%") else None


PANES_FORMAT = "#{pane_id}\t#{pane_pid}\t#{@claude_session}\t#{pane_current_command}"


def parse_panes(out: str) -> tuple[dict[str, str], dict[int, str], dict[str, str]]:
    """`tmux list-panes -F PANES_FORMAT` output -> (pane id -> foreground
    command, pane shell pid -> pane id, pane id -> @claude_session tag).
    claude-goto tags the windows it opens with `claude attach`, since the
    attached session's own process (and so its payload) knows no pane."""
    cmds: dict[str, str] = {}
    pids: dict[int, str] = {}
    tags: dict[str, str] = {}
    for line in out.splitlines():
        parts = line.split("\t", 3)
        if len(parts) == 4 and parts[1].isdigit():
            cmds[parts[0]] = "claude" if VERSIONED_BINARY_RE.fullmatch(parts[3]) else parts[3]
            pids[int(parts[1])] = parts[0]
            if parts[2]:
                tags[parts[0]] = parts[2]
    return cmds, pids, tags


def live_panes() -> tuple[dict[str, str], dict[int, str], dict[str, str]]:
    """parse_panes() for every pane tmux currently has."""
    try:
        r = subprocess.run(["tmux", "list-panes", "-a", "-F", PANES_FORMAT],
                           capture_output=True, text=True, timeout=1)
    except (OSError, subprocess.SubprocessError):
        return {}, {}, {}
    return parse_panes(r.stdout) if r.returncode == 0 else ({}, {}, {})


def pane_screen(pane: str) -> str:
    """The visible text of a tmux pane. It tells us two things the files
    cannot: whether a Claude Code dialog is up (the transcript cannot tell a
    pending permission prompt from a slow tool, and no hook fires when a
    dialog is answered), and which of several hosted sessions the pane is
    showing (its name in the separator)."""
    try:
        r = subprocess.run(["tmux", "capture-pane", "-p", "-t", pane],
                           capture_output=True, text=True, timeout=1)
    except (OSError, subprocess.SubprocessError):
        return ""
    return r.stdout if r.returncode == 0 else ""


# --- daemon ------------------------------------------------------------------------

RESCAN_DEVICES = 140    # OpenRGB SDK packet id; openrgb-python has no call for it


def request_rescan(host: str, port: int) -> None:
    """Ask the OpenRGB server to look for devices again.

    OpenRGB only detects devices at startup. At boot it can start before the
    desktop login gives this user access to the keyboard, find nothing, and
    never look again, so ask it again whenever the keyboard is missing."""
    with socket.create_connection((host, port), timeout=5) as s:
        s.sendall(struct.pack("<4sIII", b"ORGB", 0, RESCAN_DEVICES, 0))


RGB_MODES = ("info", "ripples")
# Asks for 45 frames/s; drawing time adds to each sleep, so about 35 reach the keyboard.
# Tested by eye up to that rate on 2026-11-19. A 30/s run on 2026-11-16 froze the Cynosa
# (see README); if it comes back, lower this.
RIPPLE_TICK = 1 / 45


def default_slots_path() -> str:
    return os.environ.get(
        "CLAUDE_RGB_SLOTS",
        os.path.join(os.environ.get("XDG_STATE_HOME", os.path.expanduser("~/.local/state")),
                     "claude-rgb", "slots.json"))


def default_mode_path() -> str:
    return os.path.join(os.path.dirname(default_slots_path()), "mode")


def read_rgb_mode(path: str) -> str:
    """The look chosen with claude-rgb-mode; anything unreadable means "info"."""
    try:
        with open(path) as f:
            mode = f.read().strip()
    except OSError:
        return "info"
    return mode if mode in RGB_MODES else "info"


def mode_command(args: list[str], path: str) -> int:
    """claude-rgb-mode [info|ripples|toggle]: set the look (default: toggle)."""
    want = args[0] if args else "toggle"
    if want == "toggle":
        want = "info" if read_rgb_mode(path) == "ripples" else "ripples"
    if want not in RGB_MODES:
        print(f"usage: claude-rgb-mode [{'|'.join(RGB_MODES)}|toggle]", file=sys.stderr)
        return 2
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = f"{path}.{os.getpid()}"
    with open(tmp, "w") as f:
        f.write(want + "\n")
    os.replace(tmp, path)
    print(want)
    return 0


class KeyPresses:
    """Key presses and releases from the X11 desktop, while enabled. Keeps only
    which key and when, in memory, for the life of a ripple, plus which keys
    are down; never what was typed. Disabled, it holds no X connection, so
    nothing is listened to."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._presses: deque[Press] = deque(maxlen=64)
        self._releases: deque[Press] = deque(maxlen=64)
        self._held: set[str] = set()
        self._want = threading.Event()
        self._thread: Optional[threading.Thread] = None

    def enable(self, on: bool) -> None:
        if not on:
            self._want.clear()
            self._forget()
            return
        self._want.set()
        if self._thread is None:
            self._thread = threading.Thread(target=self._run, name="keys", daemon=True)
            self._thread.start()

    def record(self, led: str, down: bool, at: float) -> None:
        with self._lock:
            if not down:
                self._held.discard(led)
                self._releases.append((led, at))
            elif led not in self._held:       # not a repeat of a key already down
                self._held.add(led)
                self._presses.append((led, at))

    def recent(self, now: float) -> list[Press]:
        with self._lock:
            return [p for p in self._presses if now - p[1] < RIPPLE_LIFE]

    def releases(self, now: float) -> list[Press]:
        with self._lock:
            return [p for p in self._releases if now - p[1] < RIPPLE_LIFE]

    def held(self) -> set[str]:
        with self._lock:
            return set(self._held)

    def _forget(self) -> None:
        with self._lock:
            self._presses.clear()
            self._releases.clear()
            self._held.clear()

    def _run(self) -> None:
        from Xlib import display
        from Xlib.ext import xinput

        failed = None
        while True:
            self._want.wait()
            try:
                d = display.Display()
                try:
                    d.screen().root.xinput_select_events(
                        [(xinput.AllMasterDevices, xinput.RawKeyPressMask | xinput.RawKeyReleaseMask)])
                    d.flush()
                    if failed:
                        print("claude-rgb: listening to key presses again", flush=True)
                    failed = None
                    while self._want.is_set():
                        select.select([d], [], [], 0.5)
                        while d.pending_events():
                            e = d.next_event()
                            if (e.type == d.extension_event.GenericEvent
                                    and e.evtype in (xinput.RawKeyPress, xinput.RawKeyRelease)):
                                led = led_for_x_keycode(raw_keycode(bytes(e.data)))
                                if led:
                                    self.record(led, e.evtype == xinput.RawKeyPress, time.monotonic())
                finally:
                    d.close()
                    with self._lock:
                        self._held.clear()     # releases while not listening would be missed
            except Exception as e:  # no desktop yet, X restarted, ...: retry
                if str(e) != failed:
                    print(f"claude-rgb: key presses unavailable ({e.__class__.__name__}: {e}); retrying", flush=True)
                failed = str(e)
                time.sleep(5)


def server_has_news(sock: socket.socket) -> bool:
    """True if the OpenRGB server has sent something unasked, without waiting.

    Once connected the daemon only sends colours, which get no reply, so
    anything to read is a "device list changed" notice (the keyboard was
    unplugged and came back as a new device) or the server closing the
    connection. Either way the device the daemon holds is no longer live."""
    readable, _, _ = select.select([sock], [], [], 0)
    return bool(readable)


def main() -> int:
    if sys.argv[1:2] == ["mode"]:
        return mode_command(sys.argv[2:], default_mode_path())

    from openrgb import OpenRGBClient
    from openrgb.utils import RGBColor

    host = os.environ.get("OPENRGB_HOST", "127.0.0.1")
    port = int(os.environ.get("OPENRGB_PORT", "6742"))
    match = os.environ.get("CLAUDE_RGB_DEVICE", "Cynosa").lower()
    state_dir = os.environ.get(
        "CLAUDE_RGB_STATE_DIR",
        os.path.join(os.environ.get("XDG_RUNTIME_DIR", "/tmp"), "claude-rgb"))
    slots_path = default_slots_path()
    mode_path = default_mode_path()
    max_age = float(os.environ.get("CLAUDE_RGB_MAX_AGE", "30"))
    tick = float(os.environ.get("CLAUDE_RGB_TICK", "0.125"))
    stats_every = 0.5    # tmux lookups (focus, dialog screens) happen at this rate
    os.makedirs(state_dir, exist_ok=True)
    slots = SlotMap(slots_path)
    bgslots = SlotMap(os.path.join(os.path.dirname(slots_path), "bgslots.json"), BG_LABELS)
    print(f"claude-rgb: state={state_dir} slots={slots_path} server={host}:{port} device~{match!r}", flush=True)

    client = dev = None
    names: list[str] = []
    rgb_mode = read_rgb_mode(mode_path)
    keys = KeyPresses()
    keys.enable(rgb_mode == "ripples")
    print(f"claude-rgb: mode {rgb_mode}", flush=True)
    last: Optional[list[Colour]] = None
    stats, stats_at = None, -1.0
    viewed: dict[str, float] = {}       # pane -> wall time last focused
    modes: dict[str, Mode] = {}         # pane -> last permission mode its footer showed
    limits = LimitClock()
    started = time.time()
    while True:
        try:
            if dev is None:
                client = OpenRGBClient(host, port, "claude-rgb")
                cands = [d for d in client.devices if match in d.name.lower()]
                if not cands:
                    request_rescan(host, port)    # the next retry sees the new list
                    raise RuntimeError(f"no device matching {match!r}: {[d.name for d in client.devices]}")
                dev = cands[0]
                dev.set_mode("Direct")
                names = [l.name for l in dev.leds]
                last = None
                print(f"claude-rgb: driving {dev.name} ({len(names)} leds)", flush=True)

            now = time.monotonic()
            if now - stats_at >= stats_every:
                if server_has_news(client.comms.sock):
                    raise RuntimeError("OpenRGB device list changed")
                want = read_rgb_mode(mode_path)
                if want != rgb_mode:
                    rgb_mode = want
                    keys.enable(rgb_mode == "ripples")
                    print(f"claude-rgb: mode {rgb_mode}", flush=True)
                wall = time.time()
                focus = focused_pane()
                if focus:
                    viewed[focus] = wall
                panes, pane_pids, pane_tags = live_panes()
                # A dialog hides the footer, so keep the pane's last mode until it is back.
                modes = {p: m for p, m in modes.items() if panes.get(p) in CLAUDE_COMMANDS}
                if focus and panes.get(focus) in CLAUDE_COMMANDS:
                    mode = mode_from_screen(pane_screen(focus))
                    if mode:
                        modes[focus] = mode
                sessions = load_sessions(state_dir, max_age, wall, pane_screen, panes, pane_pids,
                                         pane_tags=pane_tags)
                if slots.update(held_ids(sessions, wall, panes)):
                    print(f"claude-rgb: slots {slots.slots}", flush=True)
                write_slot_labels(state_dir, slots)
                if bgslots.update(held_bg_ids(sessions, wall, panes)):
                    print(f"claude-rgb: background {bgslots.slots}", flush=True)
                write_slot_labels(state_dir, bgslots, "bgslots")
                write_targets(state_dir, jump_targets(sessions, slots, bgslots))
                limits.observe(sessions, wall)
                stats = stats_from(sessions, focus, slot_views(sessions, slots, focus, viewed, started),
                                   limits=limits.latest(), bg=bg_views(sessions, bgslots),
                                   mode=modes.get(focus or ""))
                stats_at = now
            if rgb_mode == "ripples":
                frame = render_ripples(names, KEY_POSITIONS, keys.recent(now), keys.held(),
                                       keys.releases(now), now)
            else:
                frame = render(names, stats, now)
            if frame != last:
                dev.set_colors([RGBColor(*c) for c in frame], fast=True)
                last = frame
        except KeyboardInterrupt:
            return 0
        except Exception as e:  # server down, USB unplugged, ...: retry
            print(f"claude-rgb: {e.__class__.__name__}: {e}; reconnecting in 5s", flush=True)
            try:
                if client:
                    client.disconnect()
            except Exception:
                pass
            client = dev = None
            time.sleep(5)
            continue
        time.sleep(RIPPLE_TICK if rgb_mode == "ripples" else tick)


if __name__ == "__main__":
    sys.exit(main())
