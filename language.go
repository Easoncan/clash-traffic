package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"

	"clashtraffic/internal/i18n"
)

// LanguageEvent tells the pages the language of the app once it changed.
var LanguageEvent = mygo.NewEvent[string]("language")

// languageOf returns the language that settings s choose, else the
// system's.
func languageOf(s Settings) string {
	if i18n.Supported(s.Language) {
		return s.Language
	}
	return i18n.Match(mygo.App.Locale())
}

// savedLanguage reads the language chosen in the settings in dir before
// the app runs, for the update window, which takes it once: "" follows the
// system.
func savedLanguage(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		Language string `json:"language"`
	}
	if json.Unmarshal(b, &s) != nil || !i18n.Supported(s.Language) {
		return ""
	}
	return s.Language
}

var (
	languageMu    sync.Mutex
	languageHooks []func()
)

// onLanguage calls fn after the language changed, to show menus in it.
func onLanguage(fn func()) {
	languageMu.Lock()
	languageHooks = append(languageHooks, fn)
	languageMu.Unlock()
}

// setLanguage shows the app in lang, a tag of i18n.Languages: the texts
// from Go, the menus and the pages.
func setLanguage(lang string) {
	i18n.Set(lang)
	languageMu.Lock()
	hooks := append([]func(){}, languageHooks...)
	languageMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
	_ = LanguageEvent.Broadcast(i18n.Current())
}
