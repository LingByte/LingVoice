# Package encoder

Audio codec registry for LingVoice (`CreateEncode` / `CreateDecode`).

## Backends

| Build | Implementation |
|-------|----------------|
| default | Pure-Go G.711 / G.722 + `hraban/opus` (cgo libopus) |
| `-tags opus` | Enables opus codec tests (requires libopus installed) |

```bash
# Default build (G.711 + G.722 + PCM)
go build ./pkg/media/encoder/

# Run opus tests (requires libopus: brew install opus / apt install libopus-dev)
CGO_ENABLED=1 go test -tags opus ./pkg/media/encoder/ -v
```

## Layout

```
encoder/
  registry.go              # name → factory map
  pcm.go                   # PCM passthrough + resample
  pool.go                  # int16/byte scratch pool
  g711.go                  # pure-Go G.711 (A-law + μ-law) shared helpers
  pcma.go                  # G.711 A-law encode/decode + factory
  pcmu.go                  # G.711 μ-law encode/decode + factory
  g722.go                  # pure-Go G.722 encode/decode + factory
  opus.go                  # hraban/opus (cgo libopus) encode/decode + factory
  *_test.go                # unit + benchmark tests
```

## Supported codecs

| Codec | Encode | Decode | Notes |
|-------|--------|--------|-------|
| `pcm`  | ✅ | ✅ | passthrough + resample |
| `pcmu` | ✅ | ✅ | G.711 μ-law, 8 kHz |
| `pcma` | ✅ | ✅ | G.711 A-law, 8 kHz |
| `g722` | ✅ | ✅ | 16 kHz |
| `opus` | ✅ | ✅ | cgo libopus, 48 kHz, requires `-tags opus` for tests |
