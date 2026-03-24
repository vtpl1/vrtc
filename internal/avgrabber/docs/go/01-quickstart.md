# Quickstart

## Prerequisites

- Go 1.21+
- gcc or clang (cgo requires a C compiler)
- AVGrabber shared library built from this repository (see root `CLAUDE.md`)

## Build

Set cgo flags to point at the public header and the built library:

**Linux / macOS:**
```bash
export CGO_CFLAGS="-I/path/to/repo/AudioVideoGrabber3/include"
export CGO_LDFLAGS="-L/path/to/repo/build/Release -lAudioVideoGrabber2"
go build ./...
```

**Windows (PowerShell):**
```powershell
$env:CGO_CFLAGS  = "-I..\..\AudioVideoGrabber3\include"
$env:CGO_LDFLAGS = "-L..\..\build\Release -lAudioVideoGrabber2"
go build ./...
```

You may also embed these in each Go file's cgo comment block:

```go
/*
#cgo CFLAGS: -I/path/to/repo/AudioVideoGrabber3/include
#cgo LDFLAGS: -L/path/to/repo/build/Release -lAudioVideoGrabber2
#include "avgrabber_api.h"
#include <stdlib.h>
*/
import "C"
```

## Runtime

The shared library must be on the dynamic linker path at runtime:

| Platform | Variable | Library file |
|----------|----------|-------------|
| Linux | `LD_LIBRARY_PATH` | `libAudioVideoGrabber2.so` |
| macOS | `DYLD_LIBRARY_PATH` | `libAudioVideoGrabber2.dylib` |
| Windows | `PATH` | `AudioVideoGrabber2.dll` |

```bash
export LD_LIBRARY_PATH=/path/to/repo/build/Release:$LD_LIBRARY_PATH
./myprogram
```

## Minimal pull-model example

This is the simplest complete program. It opens one RTSP session and prints
a one-line summary for every frame it receives.

```go
package main

/*
#cgo CFLAGS: -I../../AudioVideoGrabber3/include
#cgo LDFLAGS: -L../../build/Release -lAudioVideoGrabber2
#include "avgrabber_api.h"
#include <stdlib.h>
*/
import "C"

import (
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "unsafe"
)

func main() {
    if len(os.Args) < 2 {
        fmt.Fprintln(os.Stderr, "usage: quickstart <rtsp_url> [user] [pass]")
        os.Exit(1)
    }

    C.avgrabber_init()
    defer C.avgrabber_deinit()

    // Config must be zero-initialised — future fields default to zero.
    var cfg C.AVGrabberConfig
    cfg.url = C.CString(os.Args[1])
    defer C.free(unsafe.Pointer(cfg.url))
    if len(os.Args) >= 3 {
        cfg.username = C.CString(os.Args[2])
        defer C.free(unsafe.Pointer(cfg.username))
    }
    if len(os.Args) >= 4 {
        cfg.password = C.CString(os.Args[3])
        defer C.free(unsafe.Pointer(cfg.password))
    }
    cfg.protocol = C.AVGRABBER_PROTO_TCP
    cfg.audio = 1

    var session *C.AVGrabberSession
    if rc := C.avgrabber_open(&session, &cfg); rc != C.AVGRABBER_OK {
        fmt.Fprintf(os.Stderr, "open failed: %s\n", C.GoString(C.avgrabber_strerror(rc)))
        os.Exit(1)
    }
    defer C.avgrabber_close(session)

    stop := make(chan os.Signal, 1)
    signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

    for {
        select {
        case <-stop:
            return
        default:
        }

        var frame *C.AVGrabberFrame
        rc := C.avgrabber_next_frame(session, 200, &frame)
        switch int(rc) {
        case C.AVGRABBER_OK:
            h := C.avgrabber_frame_header(frame)
            fmt.Printf("type=%d media=%d codec=%d size=%d pts=%d\n",
                h.frame_type, h.media_type, h.codec_type,
                h.frame_size, h.pts_ticks)
            C.avgrabber_frame_release(frame)

        case C.AVGRABBER_ERR_NOT_READY:
            // Normal — timeout elapsed, no frame yet.

        case C.AVGRABBER_ERR_STOPPED, C.AVGRABBER_ERR_AUTH_FAILED:
            fmt.Fprintf(os.Stderr, "session ended: %s\n", C.GoString(C.avgrabber_strerror(rc)))
            return

        default:
            fmt.Fprintf(os.Stderr, "error: %s\n", C.GoString(C.avgrabber_strerror(rc)))
            return
        }
    }
}
```

## What to read next

- [02-cgo-bindings.md](02-cgo-bindings.md) — production-quality Go wrapper
- [03-frame-guide.md](03-frame-guide.md) — what each frame type contains
- [04-timestamps.md](04-timestamps.md) — how to use pts_ticks for muxing
