# Running the tests

*2026-10-15*

The test suite drives a real Chromium. Run it in the Linux environment where
Rodney is used, not on macOS.

## macOS: the frame tests hang

On macOS, `TestRenderFrame` and `TestE2E_ScreenshotFrame` never return: each
blocks inside the frame rendering until `go test` reaches its timeout. In the
Linux sandbox both tests pass in under a second. To run the rest of the suite
on macOS, skip them:

```bash
go test -skip '^(TestRenderFrame|TestE2E_ScreenshotFrame)$' ./...
```

## Linux sandbox: keep temporary files off `/tmp`

The sandbox mounts `/tmp` without execute permission. Go compiles the test
binary into its temporary directory, and `TestMain` builds a Rodney binary into
the system temporary directory, so both must point to a directory that allows
execution. In the waysact sandbox, the agent home allows it:

```bash
export GOTMPDIR=/var/opt/agent-home/.cache/rodney-test/tmp
export TMPDIR=$GOTMPDIR
mkdir -p "$TMPDIR"
go test -timeout 30m ./...
```

A copy of the source without `.git`, such as one made with `git archive`, also
needs `GOFLAGS=-buildvcs=false`, because `TestMain` builds Rodney with version
control stamping.

## Platform differences the tests must allow for

The same key chord can type text on one platform and not on another: Meta+K
types "k" on Linux but not on macOS. Tests that send chords compare the keydown
and keyup events, not the input events that follow them.
