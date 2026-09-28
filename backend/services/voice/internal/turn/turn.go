package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"time"
)

type Server struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

func Credential(secret string, ttl time.Duration, now time.Time) (username, password string) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	username = fmt.Sprintf("%d:rokko", now.Add(ttl).Unix())
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(username))
	password = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return username, password
}

func ICEServers(stunURL, turnURL, secret string, ttl time.Duration, now time.Time) []Server {
	var out []Server
	if stunURL != "" {
		out = append(out, Server{URLs: []string{stunURL}})
	}
	if turnURL != "" && secret != "" {
		user, pass := Credential(secret, ttl, now)
		out = append(out, Server{URLs: []string{turnURL}, Username: user, Credential: pass})
	}
	return out
}
