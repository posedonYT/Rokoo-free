package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCredentialMatchesCoturn(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	user, pass := Credential("secret", time.Hour, now)
	require.Equal(t, "1700003600:rokko", user)
	mac := hmac.New(sha1.New, []byte("secret"))
	_, _ = mac.Write([]byte(user))
	require.Equal(t, base64.StdEncoding.EncodeToString(mac.Sum(nil)), pass)
	servers := ICEServers("stun:stun.l.google.com:19302", "turn:localhost:3478", "secret", time.Hour, now)
	require.Len(t, servers, 2)
	require.Equal(t, user, servers[1].Username)
}
