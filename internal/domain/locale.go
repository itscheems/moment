package domain

// ValidateUILocale accepts the languages shipped by the application. Nil means
// the account has not selected a language and follows its current browser.
func ValidateUILocale(locale *string) error {
	if locale == nil || *locale == "zh-CN" || *locale == "en" {
		return nil
	}
	return NewError(CodeInvalidArgument, "ui_locale must be zh-CN, en, or null", map[string]any{"field": "ui_locale"})
}
