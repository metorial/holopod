package errors

import "fmt"

type ErrorCode int

const (
	ExitSuccess         ErrorCode = 0
	ExitConfigError     ErrorCode = 1
	ExitSetupError      ErrorCode = 2
	ExitRuntimeError    ErrorCode = 3
	ExitTimeout         ErrorCode = 124
	ExitDockerError     ErrorCode = 125
	ExitContainerFailed ErrorCode = 126
)

type IsolationError struct {
	Code    ErrorCode
	Message string
	Err     error
}

func (e *IsolationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *IsolationError) Unwrap() error {
	return e.Err
}

func (e *IsolationError) ExitCode() int {
	return int(e.Code)
}

func NewConfigError(message string, err error) *IsolationError {
	return &IsolationError{
		Code:    ExitConfigError,
		Message: message,
		Err:     err,
	}
}

func NewSetupError(message string, err error) *IsolationError {
	return &IsolationError{
		Code:    ExitSetupError,
		Message: message,
		Err:     err,
	}
}

func NewRuntimeError(message string, err error) *IsolationError {
	return &IsolationError{
		Code:    ExitRuntimeError,
		Message: message,
		Err:     err,
	}
}

func NewTimeoutError(message string, err error) *IsolationError {
	return &IsolationError{
		Code:    ExitTimeout,
		Message: message,
		Err:     err,
	}
}

func NewDockerError(message string, err error) *IsolationError {
	return &IsolationError{
		Code:    ExitDockerError,
		Message: message,
		Err:     err,
	}
}

func NewContainerFailedError(exitCode int, message string) *IsolationError {
	return &IsolationError{
		Code:    ErrorCode(exitCode),
		Message: message,
		Err:     nil,
	}
}

func GetExitCode(err error) int {
	if err == nil {
		return int(ExitSuccess)
	}

	if ie, ok := err.(*IsolationError); ok {
		return ie.ExitCode()
	}

	return int(ExitConfigError)
}
