package web

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var errPassiveStyle = errors.New("invalid passive stylesheet")
var passiveStyleNumber = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:px)?$`)
var passiveStyleCombinator = regexp.MustCompile(`\s*([>+~])\s*`)

// One bounded parser, with feature-owned selector/property/value capabilities.
// Apps, Memory and Trace are its actual consumers; no public CSS API is exposed.
func validatePassiveStylesheet(source string, slots map[string]string, conditionPattern *regexp.Regexp, valueAllowed func(string, string, string) bool) error {
	return validatePassiveStylesheetRules(source, slots, conditionPattern, valueAllowed, 256)
}

// Workbench has 270 original rules; other consumers retain their 256-rule cap.
func validatePassiveStylesheetRules(source string, slots map[string]string, conditionPattern *regexp.Regexp, valueAllowed func(string, string, string) bool, maxRules int) error {
	if len(source) > 32<<10 {
		return errPassiveStyle
	}
	for {
		at := strings.Index(source, "/*")
		if at < 0 {
			break
		}
		end := strings.Index(source[at+2:], "*/")
		if end < 0 {
			return errPassiveStyle
		}
		source = source[:at] + source[at+2+end+2:]
	}
	if strings.ContainsAny(source, "\\\"'<!") {
		return errPassiveStyle
	}
	rules := 0
	var parse func(string, int) error
	parse = func(text string, depth int) error {
		if depth > 2 {
			return errPassiveStyle
		}
		for text = strings.TrimSpace(text); text != ""; text = strings.TrimSpace(text) {
			opening := strings.IndexByte(text, '{')
			if opening < 1 {
				return errPassiveStyle
			}
			header := strings.TrimSpace(text[:opening])
			level, end := 1, opening+1
			for end < len(text) && level > 0 {
				if text[end] == '{' {
					level++
				}
				if text[end] == '}' {
					level--
				}
				end++
			}
			if level != 0 {
				return errPassiveStyle
			}
			body := text[opening+1 : end-1]
			text = text[end:]
			rules++
			if rules > maxRules {
				return errPassiveStyle
			}
			if strings.HasPrefix(header, "@") {
				condition := conditionPattern.FindStringSubmatch(header)
				if condition == nil {
					return errPassiveStyle
				}
				width, _ := strconv.Atoi(condition[len(condition)-1])
				if width < 1 || width > 4096 {
					return errPassiveStyle
				}
				if err := parse(body, depth+1); err != nil {
					return err
				}
				continue
			}
			selector := passiveStyleCombinator.ReplaceAllString(strings.Join(strings.Fields(header), " "), "$1")
			allowed, ok := slots[selector]
			if !ok {
				return errPassiveStyle
			}
			for _, declaration := range strings.Split(body, ";") {
				if strings.TrimSpace(declaration) == "" {
					continue
				}
				pair := strings.SplitN(declaration, ":", 2)
				if len(pair) != 2 {
					return errPassiveStyle
				}
				property, value := strings.TrimSpace(pair[0]), strings.TrimSpace(pair[1])
				if !strings.Contains(allowed, " "+property+" ") {
					return errPassiveStyle
				}
				if !valueAllowed(selector, property, value) {
					return fmt.Errorf("%w: %s / %s", errPassiveStyle, selector, property)
				}
			}
		}
		return nil
	}
	if err := parse(source, 0); err != nil {
		return err
	}
	if rules == 0 {
		return errPassiveStyle
	}
	return nil
}
func passiveStyleOne(value, choices string) bool {
	return value != "" && !strings.Contains(value, "|") && strings.Contains("|"+choices+"|", "|"+value+"|")
}
func passiveStyleDimension(value string, min, max float64) bool {
	if !passiveStyleNumber.MatchString(value) {
		return false
	}
	if value != "0" && !strings.HasSuffix(value, "px") {
		return false
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(value, "px"), 64)
	return err == nil && n >= min && n <= max
}
