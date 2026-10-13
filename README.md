# SliceTrigger

Lets N1MM Logger+ function keys trigger actions on devices N1MM cannot control
via CAT: FlexRadio RX antenna / raw API commands, KMtronic WEB relays, generic
HTTP GET and generic TCP sends.

## Download

`SliceTrigger.exe` in this repository is a ready-to-run Windows x64 build. No
installer and no runtime are needed.

## Quick start

1. Copy `SliceTrigger.exe` to a path **without spaces**, e.g. `C:\Ham\SliceTrigger.exe`, and start it.
   Leave it running while you operate; the function keys talk to the running instance.
2. Enter the radio IP (or click **Discover**), optionally a SmartSDR **Station** name (needed for multiFLEX), and click **Connect**.
3. Click **Add** to create actions and check each one with **Test**. The log pane shows `OK` or the error.

## N1MM Logger+ setup

1. In SliceTrigger select an action and click **Copy N1MM macro**. The clipboard now holds a line like:

   ```
   F1 Ant2,"C:\Ham\SliceTrigger.exe fire 3"
   ```

2. In N1MM open the function key editor (Config > Change CW/SSB/Digital Function Key Definitions,
   or right-click a function key button in the Entry Window).
3. Paste the line and change `F1` to the key you want. The text before the comma is the button label.
4. Add the line to both the Run and the S&P section, and to the key file of each mode (CW, SSB, Digital) you use.
5. Save and press the key. The SliceTrigger log pane shows the action firing.

Use `fire 3,5` to run several actions from one key. If a key does nothing, run
`C:\Ham\SliceTrigger.exe fire 3` from a Command Prompt: if the log pane shows the
action, the bridge is fine and the N1MM line needs adjusting.

## Trigger interface

The running instance listens on `http://127.0.0.1:8765` (port configurable):

| Request | Effect |
|---|---|
| `GET /fire/3` | run action 3 |
| `GET /fire/3,5` | run actions 3 then 5 |
| `GET /fire/Ant2` | run the action named "Ant2" (case-insensitive) |
| `GET /` | list actions |

`SliceTrigger.exe fire 3`, `fire 3 5`, `fire 3,5` and `fire -port 9000 3` send the
request to the running instance and exit (exit code 0 = all actions succeeded).
The reply is 200 if everything succeeded, 404 for an unknown action, 500 if an action failed.

## Action types

| Type | What is sent |
|---|---|
| Flex RX antenna | `slice set <index> rxant=<ant>`; slice A..H or Active |
| Flex raw command | one or more API commands separated by `;`, `{slice}` = resolved slice index; stops at the first error |
| KMtronic relay | `http://<ip>/FF0101` on, `FF0100` off, `FFE0xx` pattern (bit 0 = relay 1), `FFE000` all off |
| HTTP GET | any URL, optional basic auth |
| TCP send | bytes to `host:port`, escapes `\r \n \t \0 \\ \xHH`, optional reply read |

Slice resolution: "Active" is the slice reporting `active=1` (the one selected
in SmartSDR), never the TX slice. With a Station set, only slices owned by that
SmartSDR client are considered. Without one, "Active" fails if more than one
client has an active slice (multiFLEX), and a letter picks the lowest-numbered
match, so set the Station when running multiFLEX.

## Config

`slicetrigger.json` next to the exe if that folder is writable, otherwise
`%APPDATA%\SliceTrigger\slicetrigger.json`. The path is shown in the log pane.
Relay/HTTP passwords are stored there in plain text.

## Build

Windows: `build.bat`. macOS/Linux cross-compile: `./build.sh`.

Both run `rsrc` to produce `rsrc_windows_amd64.syso` from `slicetrigger.manifest`
(common controls v6 — walk crashes at startup without it) and build with
`-ldflags "-H windowsgui"`.

Tests (any OS): `go test -race .` — includes a mock Flex radio on a local TCP port.
On non-Windows systems `go build` produces a headless build (no GUI) for development.

## Files

| File | Contents |
|---|---|
| `flex.go` | Flex TCP API client: handshake, command/reply, status merge, slice resolution, reconnect |
| `discovery.go`, `reuse_*.go` | UDP 4992 discovery listener with `SO_REUSEADDR` |
| `actions.go` | action model, KMtronic/HTTP/TCP senders, `Bridge.Fire` |
| `server.go` | HTTP trigger listener and `fire` client |
| `config.go` | config location, load, save |
| `gui_windows.go` | walk GUI |
| `app_other.go` | headless entry point for non-Windows |
| `core_test.go` | unit tests |

## Status

The core is covered by unit tests against a mock radio. It has not yet been
verified against a real FlexRadio, a real KMtronic board, or a live N1MM
installation.

## Protocol assumptions not stated in the original spec

These come from general knowledge of the SmartSDR TCP/IP API and have only been
tested against the mock radio, not real hardware:

- Station names in `client` status lines encode spaces as `0x7F`; they are decoded before comparing.
- A slice is dropped from the table on `in_use=0`, a client on `client <handle> disconnected`.
- Slices are matched to a station by comparing the slice's `client_handle` with the handle in `client 0x<handle> connected ... station=<name>`.
- A `ping` command is sent every 10 s as keepalive; only a *missing* reply (5 s) drops the connection, an error reply does not.
- Firmware that does not report `index_letter` falls back to A=0, B=1, ...
