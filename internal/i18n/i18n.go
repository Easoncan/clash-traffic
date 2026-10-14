// Package i18n has the texts the app shows from Go, such as errors, the
// tray menu and dialogs, in the languages of the app. A text is looked up
// by its English text, which is also what shows for a missing translation.
package i18n

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
)

// Languages are the language tags of the app, English first.
var Languages = []string{"en", "zh-Hans", "zh-Hant", "ja", "ko", "ru"}

// catalogs are the translations of each language but English, by English
// text. Their verbs follow those of the English text, in the same order or
// indexed (%[2]s).
var catalogs = map[string]map[string]string{
	"zh-Hans": zhHans,
	"zh-Hant": zhHant,
	"ja":      ja,
	"ko":      ko,
	"ru":      ru,
}

var current atomic.Value // string

func init() { current.Store("en") }

// Match returns the language of the app for a locale such as zh-Hans-CN,
// zh_TW or en-US: English when the app does not speak it.
func Match(locale string) string {
	tag := strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	base, rest, _ := strings.Cut(tag, "-")
	switch base {
	case "zh":
		for _, sub := range strings.Split(rest, "-") {
			if sub == "hant" || sub == "tw" || sub == "hk" || sub == "mo" {
				return "zh-Hant"
			}
		}
		return "zh-Hans"
	case "ja", "ko", "ru":
		return base
	}
	return "en"
}

// Supported reports a language tag of the app.
func Supported(lang string) bool { return slices.Contains(Languages, lang) }

// Set makes lang the language of the texts; English for one the app does
// not speak.
func Set(lang string) {
	if !Supported(lang) {
		lang = "en"
	}
	current.Store(lang)
}

// Current returns the language of the texts.
func Current() string { return current.Load().(string) }

func lookup(key string) string {
	if s, ok := catalogs[Current()][key]; ok {
		return s
	}
	return key
}

// T returns the text of key, its English text, in the current language,
// formatted with args as by fmt.Sprintf when there are any.
func T(key string, args ...any) string {
	s := lookup(key)
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// Errorf is fmt.Errorf with key in the current language: %w wraps.
func Errorf(key string, args ...any) error {
	return fmt.Errorf(lookup(key), args...)
}
