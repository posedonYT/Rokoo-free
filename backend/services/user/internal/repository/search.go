package repository

import "strings"

func parseTag(q string) (username, discriminator string, ok bool) {
	i := strings.LastIndex(q, "#")
	if i < 0 {
		return "", "", false
	}
	return q[:i], q[i+1:], true
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
