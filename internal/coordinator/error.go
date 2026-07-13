package coordinator

import "fmt"

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }
func Usage(format string, args ...any) error {
	return &ExitError{Code: 2, Err: fmt.Errorf(format, args...)}
}
func Operational(format string, args ...any) error {
	return &ExitError{Code: 1, Err: fmt.Errorf(format, args...)}
}
func Timeout(format string, args ...any) error {
	return &ExitError{Code: 124, Err: fmt.Errorf(format, args...)}
}
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	for err != nil {
		if e, ok := err.(*ExitError); ok {
			return e.Code
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return 1
}
func Interrupt(format string, args ...any) error {
	return &ExitError{Code: 130, Err: fmt.Errorf(format, args...)}
}
