package domain

type Object struct {
	ID          int64
	OwnerID     int64
	Bucket      string
	ObjectKey   string
	Filename    string
	ContentType string
	Size        int64
	Status      string
}

const (
	StatusPending = "pending"
	StatusReady   = "ready"
	MaxSize       = 25 * 1024 * 1024
)

func AllowedType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/webp", "image/gif",
		"application/pdf", "audio/mpeg", "audio/ogg", "video/mp4", "video/webm",
		"text/plain":
		return true
	default:
		return false
	}
}
