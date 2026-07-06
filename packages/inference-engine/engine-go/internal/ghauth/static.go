package ghauth

// StaticToken wraps a constant string to satisfy TokenProvider.
type StaticToken string

// GetToken returns the static token.
func (s StaticToken) GetToken() (string, error) {
	return string(s), nil
}
