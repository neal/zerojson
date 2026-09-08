package zerojson

import "time"

var nsScale = [10]int{1e9, 1e8, 1e7, 1e6, 1e5, 1e4, 1e3, 1e2, 10, 1}

var monthDays = [13]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// parseRFC3339Z parses "YYYY-MM-DDTHH:MM:SS(.f{1,9})?Z" at fixed digit
// positions. Returns ok=false for offsets, out-of-range components, or
// anything unusual — callers fall back to time.Parse for identical error
// and offset semantics.
func parseRFC3339Z(b []byte) (time.Time, bool) {
	if len(b) < 20 || b[4] != '-' || b[7] != '-' || b[10] != 'T' || b[13] != ':' || b[16] != ':' {
		return time.Time{}, false
	}
	bad := false
	d2 := func(i int) int {
		hi, lo := b[i]-'0', b[i+1]-'0'
		// hi/lo are byte subtractions: a non-digit char is either > 9 (for
		// c > '9') or wraps to a large byte (for c < '0'), so a per-digit
		// range check catches both. Range-check each digit separately rather
		// than OR-ing their values, since two valid digits can OR above 9.
		if hi > 9 || lo > 9 {
			bad = true
		}
		return int(hi)*10 + int(lo)
	}
	year := d2(0)*100 + d2(2)
	month := d2(5)
	day := d2(8)
	hour := d2(11)
	min := d2(14)
	sec := d2(17)
	if bad {
		return time.Time{}, false
	}

	ns := 0
	i := 19
	if b[i] == '.' {
		i++
		start := i
		v := 0
		for i < len(b) && b[i]-'0' <= 9 {
			v = v*10 + int(b[i]-'0')
			i++
		}
		n := i - start
		if n == 0 || n > 9 {
			return time.Time{}, false
		}
		ns = v * nsScale[n]
	}
	if i != len(b)-1 || b[i] != 'Z' {
		return time.Time{}, false
	}

	if month < 1 || month > 12 || hour > 23 || min > 59 || sec > 59 {
		return time.Time{}, false
	}
	maxDay := monthDays[month]
	if month == 2 && year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		maxDay = 29
	}
	if day < 1 || day > maxDay {
		return time.Time{}, false
	}
	// time.Date's generic calendar math (month/day normalization, offset
	// lookup, monotonic-reading strip) is overkill for a value we've
	// already validated: compute the day count since the Unix epoch
	// arithmetically and hand it to time.Unix, which — like time.Date —
	// is safe, public API (no unsafe time.Time layout mirroring). This
	// produces a time.Time identical (reflect.DeepEqual) to what
	// time.Date(..., time.UTC) would for the same instant.
	days := daysSinceUnixEpoch(year, month, day)
	secs := days*86400 + int64(hour)*3600 + int64(min)*60 + int64(sec)
	return time.Unix(secs, int64(ns)).UTC(), true
}

// daysBeforeMonth holds the cumulative day count at the start of each
// month (0-indexed) in a non-leap year; daysBeforeMonthLeap the same for
// a leap year.
var daysBeforeMonth = [12]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
var daysBeforeMonthLeap = [12]int{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335}

func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// leapsBeforeYear counts leap years strictly before year (i.e. in
// [1, year)), matching the convention time.Date's internal arithmetic
// relies on.
func leapsBeforeYear(year int) int {
	y := year - 1
	return y/4 - y/100 + y/400
}

func floorDiv(n, d int) int {
	q := n / d
	r := n % d
	if r != 0 && (r < 0) != (d < 0) {
		q--
	}
	return q
}

// daysSinceUnixEpoch returns the number of days between the Unix epoch
// (1970-01-01) and the given date (which parseRFC3339Z has already
// validated: 1 <= month <= 12, 1 <= day <= daysInMonth). Negative for
// dates before the epoch. year >= 1970 uses simple leap-year counting;
// earlier years use Howard Hinnant's days-from-civil algorithm (the
// era/yoe/doy/doe construction), which is valid over the proleptic
// Gregorian calendar for any year time.Time can represent.
func daysSinceUnixEpoch(year, month, day int) int64 {
	if year >= 1970 {
		days := 365*(year-1970) + leapsBeforeYear(year) - leapsBeforeYear(1970)
		if isLeapYear(year) {
			days += daysBeforeMonthLeap[month-1]
		} else {
			days += daysBeforeMonth[month-1]
		}
		days += day - 1
		return int64(days)
	}
	y := year
	if month <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	m := month
	if m <= 2 {
		m += 12
	}
	doy := (153*(m-3)+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era*146097 + doe - 719468)
}

// ReadTime parses a quoted RFC3339 timestamp. UTC ("Z") timestamps — the
// common fast path — parse at fixed digit positions; offsets and
// edge cases fall back to time.Parse.
func (l *Lexer) ReadTime() time.Time {
	b, plain, _ := l.readStringBytes()
	if !plain {
		// An escaped spelling (`\u0032024-...`) is exotic but valid JSON
		// for the same string; unescape and parse it like any other.
		b = unescapeStringBytes(b)
	}
	if t, ok := parseRFC3339Z(b); ok {
		return t
	}
	t, err := time.Parse(time.RFC3339, bytesToString(b))
	if err != nil {
		// ParseError embeds the entire token (sometimes twice). Keep only a
		// bounded excerpt in the diagnostic, as the numeric readers do.
		l.setErr("time: invalid RFC3339 timestamp %s", errToken(b))
	}
	return t
}
