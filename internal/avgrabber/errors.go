package avgrabber

import (
	"errors"
	"fmt"
)

var (
	errNotReady    = errors.New("not ready")
	errStopped     = errors.New("stopped")
	errAuthFailed  = errors.New("authentication failed")
	errNullPointer = errors.New("null pointer")
	errInvalidArg  = errors.New("invalid argument")
	errUnknown     = errors.New("avgrabber error")
)

func statusError(rc int) error {
	switch rc {
	case StatusOK:
		return nil
	case ErrNullPointer:
		return errNullPointer
	case ErrNotReady:
		return errNotReady
	case ErrStopped:
		return errStopped
	case ErrInvalidArg:
		return errInvalidArg
	case ErrAuthFailed:
		return errAuthFailed
	default:
		return fmt.Errorf("%w %d", errUnknown, rc)
	}
}

// IsNotReady returns true for the normal per-frame timeout condition.
// Callers should loop rather than treating this as a fatal error.
func IsNotReady(err error) bool { return errors.Is(err, errNotReady) }

// IsFatal returns true when the session will not recover automatically.
func IsFatal(err error) bool {
	return errors.Is(err, errStopped) || errors.Is(err, errAuthFailed)
}
