package source

import (
	"errors"
	"time"
)

// sourceTime scans timezone-free MySQL values. SourceLocation is authoritative
// even if a driver's parseTime setting attached a different location to the
// same wall-clock fields. SQL comparisons likewise bind Now in SourceLocation.
type sourceTime struct {
	location *time.Location
	time     time.Time
	valid    bool
}

func (s *sourceTime) Scan(value any) error {
	s.time, s.valid = time.Time{}, false
	if value == nil {
		return nil
	}
	var parsed time.Time
	var err error
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return errors.New("source: invalid zero timestamp")
		}
		parsed = time.Date(v.Year(), v.Month(), v.Day(), v.Hour(), v.Minute(), v.Second(), v.Nanosecond(), s.location)
	case string:
		parsed, err = time.ParseInLocation("2006-01-02 15:04:05", v, s.location)
	case []byte:
		parsed, err = time.ParseInLocation("2006-01-02 15:04:05", string(v), s.location)
	default:
		return errors.New("source: unsupported timestamp representation")
	}
	if err != nil {
		return errors.New("source: invalid timestamp")
	}
	s.time, s.valid = parsed.UTC(), true
	return nil
}
