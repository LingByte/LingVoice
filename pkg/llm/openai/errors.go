package openai

import (
	"errors"
)

var errMissingAPIKey = errors.New("openai: api key is required")
