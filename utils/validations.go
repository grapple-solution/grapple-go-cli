package utils

import (
	"fmt"
	"regexp"
)

func ValidateGrasTemplates(template string) error {
	if !Contains(GrasTemplates, template) {
		return fmt.Errorf("invalid template: %s", template)
	}
	return nil
}

func ValidateResourceName(name string) error {
	matched, err := regexp.MatchString(AlphaNumericWithHyphenUnderscoreRegex, name)
	if err != nil {
		return fmt.Errorf("invalid regex pattern: %v", err)
	}
	if !matched {
		return fmt.Errorf("gras name must be alphanumeric with hyphens and underscores")
	}

	return nil
}
