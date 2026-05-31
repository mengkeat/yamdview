# Postmortem: browser launch fallback failed under WSL

Date: 2026-05-31
Status: Resolved
Affected command: `bin/yamdview PLAN.md`
Affected area: `internal/browser`

## Summary

`yamdview` is supposed to start a local HTTP server and open the rendered Markdown page in the user's default browser. The original browser-opening implementation selected an opener purely from `runtime.GOOS`:

- Linux: `xdg-open <url>`
- macOS: `open <url>`
- Windows: `rundll32 url.dll,FileProtocolHandler <url>`

That was too coarse for the actual runtime environment. The command was being run inside WSL2, where Go correctly reports `runtime.GOOS == "linux"`, but the usable graphical browser is the Windows default browser. Because WSL looks like Linux to Go while the real desktop browser lives on the Windows side, the Linux opener path was selected.

A first fix added Linux fallbacks (`xdg-open`, `gio open`, `sensible-browser`) but still did not solve the user's `bin/yamdview PLAN.md` case. There were two separate issues:

1. The already-built `bin/yamdview` binary was not rebuilt after the first source change, so running `bin/yamdview PLAN.md` still executed the old `xdg-open`-only logic.
2. Even after rebuilding, generic Linux openers are still the wrong primary choice under WSL. They may be present, but they do not necessarily have a working graphical browser behind them.

The final fix special-cases WSL and prefers Windows-side browser launchers before Linux desktop openers.

## User-visible symptom

The user ran:

```sh
bin/yamdview PLAN.md
```

The app started the server, but browser launch failed with an `xdg-open`/Linux-opener problem instead of opening the page in the correct browser.

## Environment findings

The runtime environment was inspected with:

```sh
uname -a
go env GOOS
command -v xdg-open || true
command -v gio || true
command -v sensible-browser || true
command -v wslview || true
command -v explorer.exe || true
command -v powershell.exe || true
command -v cmd.exe || true
```

Relevant findings:

```text
Linux Hawkins 6.6.114.1-microsoft-standard-WSL2 ... x86_64 GNU/Linux
GOOS=linux
```

Available commands included:

```text
/usr/bin/gio
/usr/bin/sensible-browser
/mnt/c/WINDOWS/explorer.exe
/mnt/c/WINDOWS/System32/WindowsPowerShell/v1.0/powershell.exe
/mnt/c/WINDOWS/system32/cmd.exe
```

Additional check:

```sh
/proc/sys/kernel/osrelease
```

contained:

```text
6.6.114.1-microsoft-standard-WSL2
```

This confirmed the important mismatch:

- Go sees Linux.
- The actual usable desktop browser is on Windows.
- WSL exposes Windows `.exe` launchers on `PATH`.

## Why it fell back to `xdg-open`

There were two layers to this.

### 1. Original implementation selected by `runtime.GOOS` only

The original code in `internal/browser/browser.go` did this:

```go
switch runtime.GOOS {
case "linux":
    return exec.Command("xdg-open", url), nil
case "darwin":
    return exec.Command("open", url), nil
case "windows":
    return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
}
```

Inside WSL2, `runtime.GOOS` is `linux`, so `xdg-open` was selected. That is technically correct from Go's point of view, but wrong for the user's intended desktop-browser behavior.

### 2. First fallback patch did not handle WSL and was not rebuilt into `bin/yamdview`

The first attempted fix changed source code to try Linux openers in this order:

1. `xdg-open`
2. `gio open`
3. `sensible-browser`

However, after that source change only tests were run. The ignored built artifact `bin/yamdview` was not rebuilt. Because the user invoked the binary directly, they were still running the old binary, not the patched source.

That explains why the user still saw the `xdg-open` behavior after the initial source-only fix.

Even if it had been rebuilt, the first fallback was still incomplete for WSL because it treated WSL as ordinary Linux. The correct primary route in WSL is to ask Windows to open the URL in the Windows default browser.

## Why the first Linux fallback design was insufficient

The first fallback design used `exec.LookPath` to choose the first installed opener. That only proves the executable exists. It does not prove the command can actually open a graphical browser in the current environment.

This matters in headless Linux, containers, SSH sessions, and WSL. For example:

- `gio` can be installed but unable to open URLs.
- `sensible-browser` can be installed but have no configured browser.
- `xdg-open` can start successfully but fail after startup depending on desktop/session configuration.

The initial `Open` implementation also used `cmd.Start()` only. `cmd.Start()` reports whether the process was launched, not whether the opener actually succeeded. If the opener immediately exits with an error, `cmd.Start()` alone does not catch that. Therefore, a command could be treated as successful even if it printed an error and failed to open anything.

## Commands tried during diagnosis

Several opener commands were tested manually from the WSL environment.

### `gio open`

Command:

```sh
gio open 'http://127.0.0.1:1'
```

Result:

```text
rc=2
gio: http://127.0.0.1:1: Operation not supported
```

Conclusion: `gio` existed, but it was not a working browser opener in this environment.

### `sensible-browser`

Command:

```sh
sensible-browser 'http://127.0.0.1:1'
```

Result:

```text
rc=1
Couldn't find a suitable web browser! Set the BROWSER environment variable to your desired browser.
```

Conclusion: `sensible-browser` existed, but no Linux-side browser was configured.

### `explorer.exe`

Command:

```sh
explorer.exe http://localhost:1
```

Result:

```text
rc=1
```

Conclusion: In this environment, `explorer.exe` was not reliable as the URL opener from WSL.

### `powershell.exe` direct invocation

An initial direct attempt was wrong:

```sh
powershell.exe 'http://127.0.0.1:1'
```

Result:

```text
The term 'http://127.0.0.1:1' is not recognized as the name of a cmdlet...
```

Conclusion: PowerShell treats the URL as a command unless wrapped in `Start-Process`.

### `powershell.exe Start-Process`

Command:

```sh
powershell.exe -NoProfile -Command Start-Process 'http://127.0.0.1:1'
```

Result:

```text
rc=0
```

This worked for a simple URL. However, it became fragile when the URL contained shell-significant characters such as `&`:

```sh
powershell.exe -NoProfile -Command Start-Process 'http://localhost:1/?a=1&b=2'
```

Result:

```text
The ampersand (&) character is not allowed...
```

Conclusion: PowerShell can work, but building a robust `-Command` string is easy to get wrong. It was not selected for the final fallback chain.

### `cmd.exe /C start`

Command:

```sh
cmd.exe /C start "" http://127.0.0.1:1
```

Result:

```text
rc=0
```

It also emitted a WSL UNC working-directory warning:

```text
CMD.EXE was started with the above path as the current directory.
UNC paths are not supported. Defaulting to Windows directory.
```

Conclusion: This is a viable fallback, but it is noisier than `rundll32.exe`.

### `rundll32.exe url.dll,FileProtocolHandler`

Command:

```sh
rundll32.exe url.dll,FileProtocolHandler 'http://localhost:1'
```

Result:

```text
rc=0
```

It also handled a URL containing `&`:

```sh
rundll32.exe url.dll,FileProtocolHandler 'http://localhost:1/?a=1&b=2'
```

Result:

```text
rc=0
```

Conclusion: This was the best available Windows-side opener in the observed WSL environment.

## Final implementation

The final implementation changed `internal/browser/browser.go` so that WSL is detected separately from ordinary Linux.

WSL detection checks Linux kernel marker files:

- `/proc/sys/kernel/osrelease`
- `/proc/version`

If either contains `microsoft` or `wsl`, the process is treated as running under WSL.

On ordinary Linux, the opener order remains:

1. `xdg-open`
2. `gio open`
3. `sensible-browser`

On WSL, the opener order is now:

1. `wslview`
2. `rundll32.exe url.dll,FileProtocolHandler`
3. `cmd.exe /C start ""`
4. `xdg-open`
5. `gio open`
6. `sensible-browser`

The first three are Windows-side openers. The Linux openers remain as last-resort fallbacks.

The final implementation also changed browser opening from "start one command and assume success" to "try available commands in order." It now runs an opener and waits briefly:

- If the opener exits quickly with success, launch is treated as successful.
- If it exits quickly with failure, the next opener is tried.
- If it stays running beyond a short grace period, launch is treated as successful so `yamdview` startup is not blocked.

This matters because `exec.LookPath` only answers "is this executable present?" The final logic also handles "the executable exists but failed immediately."

## Tests added

Tests were added in `internal/browser/browser_test.go` for:

- macOS command selection.
- Windows command selection.
- Linux fallback order.
- WSL opener priority.
- Falling back from WSL openers to Linux openers if Windows-side openers are unavailable.
- Runtime fallback when one available opener fails.
- Error reporting when all available openers fail.
- Unsupported platform error.
- WSL marker detection.

## Verification

The test suite was run through the project Makefile so Go caches stayed inside `.cache/`:

```sh
make test
```

Result:

```text
ok github.com/mengkeat/yamdview/internal/browser
ok github.com/mengkeat/yamdview/internal/app
ok github.com/mengkeat/yamdview/internal/cli
ok github.com/mengkeat/yamdview/internal/markdown
ok github.com/mengkeat/yamdview/internal/server
ok github.com/mengkeat/yamdview/internal/watcher
```

The binary was rebuilt:

```sh
make build
```

The exact user command path was then tested:

```sh
timeout 5s bin/yamdview PLAN.md
```

Observed output:

```text
2026/05/31 15:01:27 serving PLAN.md at http://127.0.0.1:46467/
2026/05/31 15:01:32 shutting down
```

There was no `xdg-open`/Linux-opener warning during this run.

## Root causes

1. **OS detection was too coarse.**
   `runtime.GOOS == "linux"` is not enough to decide how to open a browser. WSL needs special handling because it is Linux from Go's perspective but usually uses the Windows desktop browser.

2. **Presence was confused with correctness.**
   `exec.LookPath` can find an opener that is installed but unusable in the current session.

3. **The first source fix was not built into `bin/yamdview`.**
   Running `make test` validates source but does not update the ignored `bin/yamdview` binary. The user ran the binary directly, so `make build` was required.

4. **`cmd.Start()` alone was not enough.**
   It only verifies process startup. It does not catch immediate opener failure.

## What made it work eventually

The working solution combined four changes:

1. Detect WSL using `/proc` kernel markers.
2. Prefer Windows-side URL openers under WSL.
3. Try the next opener if an available opener exits with an immediate failure.
4. Rebuild `bin/yamdview` after changing the source.

The key practical change was preferring this under WSL:

```sh
rundll32.exe url.dll,FileProtocolHandler <url>
```

That delegates URL handling to Windows and opens the user's configured Windows default browser.

## Follow-up notes

- If `wslview` is installed, it is preferred because it is purpose-built for WSL URL/file opening.
- `rundll32.exe url.dll,FileProtocolHandler` is the main fallback for WSL because it worked cleanly in this environment.
- `cmd.exe /C start "" <url>` remains as a backup.
- Generic Linux openers still make sense for normal desktop Linux.
- `PLAN.md` remains a local planning artifact and must not be staged or committed.
