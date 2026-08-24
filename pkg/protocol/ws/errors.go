package ws

import "errors"

var (
	ErrFrameTooShort    = errors.New("ws: binary frame too short, need at least 8 bytes header")
	ErrNotNegotiated    = errors.New("ws: session not negotiated, send offer first")
	ErrAlreadyNegotiated = errors.New("ws: session already negotiated")
	ErrSessionNotFound  = errors.New("ws: session not found")
	ErrInvalidMessage   = errors.New("ws: invalid message format")
	ErrCodecNotSupported = errors.New("ws: no common codec found")
)
