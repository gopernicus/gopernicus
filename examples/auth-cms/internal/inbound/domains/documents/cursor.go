package documents

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/sdk"
)

// CursorCodec keeps denied candidates' sort values private and binds continuation
// to the host query. Hosts supply a stable shared key across application instances.
type CursorCodec struct{ aead cipher.AEAD }

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("documents: cursor key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CursorCodec{aead: aead}, nil
}

func cursorBinding(principal sdk.Principal, query Query) []byte {
	// Limit is deliberately absent: a client can resize a page while preserving
	// its principal, tenant, search, ordering and permission.
	data, _ := json.Marshal(struct {
		Principal  sdk.Principal
		Tenant     string
		Search     string
		Desc       bool
		Permission string
	}{principal, query.TenantID, query.Search, query.Desc, "document:view:v1"})
	return data
}

func (c *CursorCodec) encode(p domain.Position, binding []byte) (string, error) {
	plain, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plain, binding)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *CursorCodec) decode(token string, binding []byte) (domain.Position, error) {
	if token == "" {
		return domain.Position{}, nil
	}
	bad := fmt.Errorf("documents: invalid continuation; restart the query: %w", sdk.ErrInvalidInput)
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(sealed) < c.aead.NonceSize()+c.aead.Overhead() {
		return domain.Position{}, bad
	}
	nonce := sealed[:c.aead.NonceSize()]
	plain, err := c.aead.Open(nil, nonce, sealed[c.aead.NonceSize():], binding)
	if err != nil {
		return domain.Position{}, bad
	}
	var p domain.Position
	if err := json.Unmarshal(plain, &p); err != nil || p.ID == "" {
		return domain.Position{}, bad
	}
	return p, nil
}
