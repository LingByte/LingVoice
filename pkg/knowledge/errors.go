package knowledge

import "errors"

var (
	ErrHandlerNotFound        = errors.New("handler not be null")
	ErrBaseURL                = errors.New("BaseURL is required")
	ErrCollectionNotFound     = errors.New("Collection is required")
	ErrEmbedderNotFound       = errors.New("Embedder is required")
	ErrRecordNotFound         = errors.New("record not found")
	ErrNamespaceNotFound      = errors.New("namespace not found")
	ErrInvalidVectorDimension = errors.New("invalid vector dimension")
	ErrEmptyQuery             = errors.New("empty query text")
)
