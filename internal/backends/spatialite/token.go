//go:build cgo && spatialite

package spatialite

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
)

// encodeListToken / decodeListToken: simple offset cursor for
// ListCollections (collections are bounded — keyset is overkill).
func encodeListToken(n int) string {
	if n <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(n)))
}

func decodeListToken(tok string) (int, error) {
	if tok == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return 0, errors.New("invalid token encoding")
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, errors.New("invalid token")
	}
	return n, nil
}

// searchCursor is the keyset cursor used by Search. Datetime is the
// leading sort value (RFC3339, lex-ordered) and ID is the tiebreak.
type searchCursor struct {
	V        int    `json:"v"`
	Datetime string `json:"d,omitempty"`
	ID       string `json:"i"`
}

func encodeSearchToken(c searchCursor) string {
	c.V = 1
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeSearchToken(tok string) (*searchCursor, error) {
	if tok == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil, errors.New("invalid token encoding")
	}
	var c searchCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, errors.New("invalid token")
	}
	if c.V != 1 || c.ID == "" {
		return nil, errors.New("invalid token")
	}
	return &c, nil
}
