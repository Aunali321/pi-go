package session

import "time"

const isoLayout = "2006-01-02T15:04:05.000Z07:00"

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func ParseISO(s string) time.Time {
	for _, layout := range []string{isoLayout, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
