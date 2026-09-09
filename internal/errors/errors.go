package errors

type ErrorKind string

const (
	Transient          ErrorKind = "TRANSIENT_ERROR"
	Authentication     ErrorKind = "AUTHENTICATION_ERROR"
	Permission         ErrorKind = "PERMISSION_ERROR"
	Build              ErrorKind = "BUILD_ERROR"
	Policy             ErrorKind = "POLICY_ERROR"
	Graph              ErrorKind = "GRAPH_ERROR"
	TagConflict        ErrorKind = "TAG_CONFLICT_ERROR"
	Asset              ErrorKind = "ASSET_ERROR"
	RegistryConflict   ErrorKind = "REGISTRY_CONFLICT_ERROR"
	VersionConflict    ErrorKind = "VERSION_CONFLICT_ERROR"
	InvariantViolation ErrorKind = "INVARIANT_VIOLATION"
)

type Error struct {
	Kind    ErrorKind
	Message string
	Cause   error
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Message }
func (e *Error) Unwrap() error { return e.Cause }

func New(kind ErrorKind, message string) error { return &Error{Kind: kind, Message: message} }
func Wrap(kind ErrorKind, message string, cause error) error {
	return &Error{Kind: kind, Message: message, Cause: cause}
}
