package locale

import (
	"time"
)

// The Spanish clock says every minute, the hour first - "las dos y media" - and keeps "menos" for
// the round minutes near the next hour only: menos veinte, menos cuarto, menos diez, menos cinco.
// Nobody says "las ocho menos veintitrés": twenty-three minutes past is "las siete y treinta y
// siete". The parts of the day are counted by the sun where it is known, but the sun only moves
// them so far: the morning starts when it rises, never later than seven, and the afternoon ends
// when it sets, never later than nine - in Madrid in December eight o'clock is "de la mañana" with
// the sun still down, and half past nine is "de la noche" on the longest summer evening. Mediodía
// and medianoche are the exact hours only - ten past twelve is "las doce y diez" - and they take
// the verb, "es mediodía", while the twelve hours go without a part of the day.

// esMinutes are the minutes Spanish counts with y, to fifty-nine. The round ones near the next hour
// are said with menos instead, so their entries here go unused.
var esMinutes = []string{"", "uno", "dos", "tres", "cuatro", "cinco", "seis", "siete", "ocho", "nueve", "diez",
	"once", "doce", "trece", "catorce", "quince", "dieciséis", "diecisiete", "dieciocho", "diecinueve", "veinte",
	"veintiuno", "veintidós", "veintitrés", "veinticuatro", "veinticinco", "veintiséis", "veintisiete",
	"veintiocho", "veintinueve", "treinta", "treinta y uno", "treinta y dos", "treinta y tres", "treinta y cuatro",
	"treinta y cinco", "treinta y seis", "treinta y siete", "treinta y ocho", "treinta y nueve", "cuarenta",
	"cuarenta y uno", "cuarenta y dos", "cuarenta y tres", "cuarenta y cuatro", "cuarenta y cinco",
	"cuarenta y seis", "cuarenta y siete", "cuarenta y ocho", "cuarenta y nueve", "cincuenta", "cincuenta y uno",
	"cincuenta y dos", "cincuenta y tres", "cincuenta y cuatro", "cincuenta y cinco", "cincuenta y seis",
	"cincuenta y siete", "cincuenta y ocho", "cincuenta y nueve"}

// esHours are the hours as Spanish says them, bare: the article rides on the intro - "son las /
// dos" - and the clock is feminine, "es la / una".
var esHours = []string{"", "una", "dos", "tres", "cuatro", "cinco", "seis", "siete", "ocho", "nueve", "diez",
	"once", "doce"}

// saySpanish says the time to the minute: "las dos y media de la tarde", "las tres menos cuarto",
// "una en punto", "es mediodía".
func saySpanish(t time.Time, sun Sun) SaidTime {
	s := SaidTime{HourFirst: true}
	h, m := t.Hour(), t.Minute()
	switch {
	case h == 0 && m == 0:
		s.Intro, s.Hour = "es", "medianoche"
		return s
	case h == 12 && m == 0:
		s.Intro, s.Hour = "es", "mediodía"
		return s
	}
	// The minutes near the next hour are said with menos only when they are round; the rest keep
	// the hour they are in and say y.
	menos := m == 40 || m == 45 || m == 50 || m == 55
	said := h % 12
	if said == 0 {
		said = 12
	}
	if menos {
		said = said%12 + 1 // "las ocho menos cuarto" at a quarter to eight
	}
	if said == 1 {
		s.Intro = "es la"
	} else {
		s.Intro = "son las"
	}
	s.Hour = esHours[said]
	switch {
	case m == 0:
		s.Hour += " en punto"
	case m == 15:
		s.Count = "y cuarto"
	case m == 30:
		s.Count = "y media"
	case m == 40:
		s.Count = "menos veinte"
	case m == 45:
		s.Count = "menos cuarto"
	case m == 50:
		s.Count = "menos diez"
	case m == 55:
		s.Count = "menos cinco"
	default:
		s.Count = "y " + esMinutes[m]
	}
	switch {
	case h == 12:
		// Between twelve and one the hour stands on its own - "las doce y cuarto" - except when it
		// is said as one ("la una menos veinte"), which is the afternoon's already.
		if menos {
			s.Period = "de la tarde"
		}
	case h < 12:
		// The morning starts when the sun rises, but never later than seven: a December sun that
		// comes up at half past eight still leaves eight o'clock "de la mañana". With no sun to go
		// by, at six. Before it, madrugada.
		start := time.Date(t.Year(), t.Month(), t.Day(), 6, 0, 0, 0, t.Location())
		if sun.OK {
			start = time.Date(t.Year(), t.Month(), t.Day(), 7, 0, 0, 0, t.Location())
			if sun.Rise.Before(start) {
				start = sun.Rise
			}
		}
		if t.Before(start) {
			s.Period = "de la madrugada"
		} else {
			s.Period = "de la mañana"
		}
	default:
		// The afternoon ends when the sun sets, but never before eight and never later than nine:
		// eight o'clock is "de la noche" in winter, and even the latest sunset does not keep
		// "de la tarde" going past nine.
		night := time.Date(t.Year(), t.Month(), t.Day(), 20, 0, 0, 0, t.Location())
		if sun.OK && sun.Set.After(night) {
			night = sun.Set
		}
		if late := time.Date(t.Year(), t.Month(), t.Day(), 21, 0, 0, 0, t.Location()); night.After(late) {
			night = late
		}
		if t.Before(night) {
			s.Period = "de la tarde"
		} else {
			s.Period = "de la noche"
		}
	}
	return s
}
