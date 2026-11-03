package taskdeliveries

import "time"

// Match the native ODBC parameter contract exactly; never round cursor identity.
func nativeTime(value time.Time) bool {
 return !value.IsZero() && value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}
