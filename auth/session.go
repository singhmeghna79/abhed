package auth

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// browserSession is what a cookie resolves to, for any provider that holds
// sessions in memory. Shared here rather than defined beside one provider so
// the providers can live in different packages without either owning it.
type browserSession struct {
	Identity *Identity
	Expires  time.Time
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// A failing CSPRNG is not something to paper over with a weaker source.
		panic("auth: system random source unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
