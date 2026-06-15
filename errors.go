package errorgap

import "errors"

var (
	// ErrMissingProjectSlug is returned from validation when ProjectSlug
	// is empty.
	ErrMissingProjectSlug = errors.New("errorgap: ProjectSlug is required")
	// ErrMissingEndpoint is returned from validation when Endpoint is
	// empty.
	ErrMissingEndpoint = errors.New("errorgap: Endpoint is required")
)
