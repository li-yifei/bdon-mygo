package i18n

import (
	"errors"
	"fmt"
)

// Error is an error whose text is a catalog message. The services return it
// instead of wrapping causes with a translated prefix, so that the user
// interface can show the message in the current language and the raw cause
// (adb output, OS errors) on its own line.
type Error struct {
	Key   string
	Args  []any // alternating placeholder names and values
	Cause error
}

// New returns an *Error for key with placeholder name/value pairs.
func New(key string, kv ...any) error { return &Error{Key: key, Args: kv} }

// Wrap returns an *Error for key that wraps cause.
func Wrap(cause error, key string, kv ...any) error {
	return &Error{Key: key, Args: kv, Cause: cause}
}

// Message returns the translated message, without the cause.
func (e *Error) Message() string { return T(e.Key, e.Args...) }

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Message()
	}
	return e.Message() + ": " + e.Cause.Error()
}

func (e *Error) Unwrap() error { return e.Cause }

// Is matches another *Error with the same key, so that sentinel values such
// as ErrX = i18n.New("key") work with errors.Is.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Key == e.Key
}

// Describe splits err for display: reason is the translated message of the
// outermost *Error in its chain ("" when there is none) and raw the
// untranslated rest — the cause of that *Error, or err.Error() for a plain
// error.
func Describe(err error) (reason, raw string) {
	if err == nil {
		return "", ""
	}
	var e *Error
	if !errors.As(err, &e) {
		return "", err.Error()
	}
	reason = e.Message()
	if e.Cause != nil {
		_, inner := Describe(e.Cause)
		if r, _ := Describe(e.Cause); r != "" {
			reason += " · " + r
		}
		raw = inner
	}
	return reason, raw
}

// Raw returns the untranslated part of err (see Describe), falling back to
// err.Error() when everything is translated.
func Raw(err error) string {
	_, raw := Describe(err)
	if raw == "" && err != nil {
		return fmt.Sprint(err)
	}
	return raw
}
