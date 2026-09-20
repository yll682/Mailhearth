// Package sieve compiles the structured rule model into a Sieve script and
// talks ManageSieve (RFC 5804) to install it on the mail server.
package sieve

import (
	"fmt"
	"regexp"
	"strings"

	"mailhearth/internal/model"
)

// ScriptName is the ManageSieve script Mailhearth owns on every mailbox.
const ScriptName = "mailhearth"

// ValidationError points at the rule/condition/action that is invalid.
type ValidationError struct {
	Rule  string
	Field string
	Msg   string
}

func (e *ValidationError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("rule %q: %s: %s", e.Rule, e.Field, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Msg)
}

var (
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	sizeRe       = regexp.MustCompile(`^[0-9]{1,9}[KMkm]?$`)
	flagRe       = regexp.MustCompile(`^(\\[A-Za-z]+|\$?[A-Za-z0-9_-]{1,32})$`)
	addressRe    = regexp.MustCompile(`^[^@\s"<>]+@[^@\s"<>]+\.[^@\s"<>]+$`)
)

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return `"` + s + `"`
}

// multiline renders a Sieve multi-line string (text: ... .) with dot-stuffing.
func multiline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.WriteString("text:\r\n")
	for _, l := range lines {
		if strings.HasPrefix(l, ".") {
			b.WriteString(".")
		}
		b.WriteString(l)
		b.WriteString("\r\n")
	}
	b.WriteString(".\r\n")
	return b.String()
}

type compiler struct {
	requires map[string]bool
}

func (c *compiler) require(ext string) { c.requires[ext] = true }

func (c *compiler) condition(rule string, i int, cond model.SieveCondition) (string, error) {
	field := fmt.Sprintf("conditions[%d]", i)
	op := cond.Op
	if op == "" {
		op = "contains"
	}
	value := strings.TrimSpace(cond.Value)
	if value == "" {
		return "", &ValidationError{Rule: rule, Field: field, Msg: "value is required"}
	}
	negate := false
	if op == "not_contains" {
		op, negate = "contains", true
	}
	var test string
	switch cond.Field {
	case "from", "to", "subject", "header", "body":
		var match string
		switch op {
		case "contains", "is", "matches":
			match = ":" + op
		default:
			return "", &ValidationError{Rule: rule, Field: field, Msg: "unknown operator " + op}
		}
		switch cond.Field {
		case "from":
			test = fmt.Sprintf(`address %s "from" %s`, match, quote(value))
		case "to":
			test = fmt.Sprintf(`address %s ["to", "cc"] %s`, match, quote(value))
		case "subject":
			test = fmt.Sprintf(`header %s "subject" %s`, match, quote(value))
		case "header":
			if !headerNameRe.MatchString(cond.Header) {
				return "", &ValidationError{Rule: rule, Field: field, Msg: "invalid header name"}
			}
			test = fmt.Sprintf(`header %s %s %s`, match, quote(cond.Header), quote(value))
		case "body":
			c.require("body")
			test = fmt.Sprintf(`body :text %s %s`, match, quote(value))
		}
	case "size":
		if !sizeRe.MatchString(value) {
			return "", &ValidationError{Rule: rule, Field: field, Msg: "size must look like 500K or 2M"}
		}
		switch op {
		case "over", "under":
			test = fmt.Sprintf(`size :%s %s`, op, strings.ToUpper(value))
		default:
			return "", &ValidationError{Rule: rule, Field: field, Msg: "size supports over/under only"}
		}
	default:
		return "", &ValidationError{Rule: rule, Field: field, Msg: "unknown field " + cond.Field}
	}
	if negate {
		test = "not " + test
	}
	return test, nil
}

func (c *compiler) action(rule string, i int, a model.SieveAction) (string, error) {
	field := fmt.Sprintf("actions[%d]", i)
	switch a.Type {
	case "move", "copy":
		folder := strings.TrimSpace(a.Folder)
		if folder == "" {
			return "", &ValidationError{Rule: rule, Field: field, Msg: "folder is required"}
		}
		c.require("fileinto")
		if a.Type == "copy" {
			c.require("copy")
			return fmt.Sprintf("fileinto :copy %s;", quote(folder)), nil
		}
		return fmt.Sprintf("fileinto %s;", quote(folder)), nil
	case "flag":
		flag := strings.TrimSpace(a.Flag)
		if flag == "" {
			flag = `\Flagged`
		}
		if !flagRe.MatchString(flag) {
			return "", &ValidationError{Rule: rule, Field: field, Msg: "invalid flag"}
		}
		c.require("imap4flags")
		return fmt.Sprintf("addflag %s;", quote(flag)), nil
	case "markread":
		c.require("imap4flags")
		return `addflag "\\Seen";`, nil
	case "forward", "redirect":
		addr := strings.TrimSpace(a.Address)
		if !addressRe.MatchString(addr) {
			return "", &ValidationError{Rule: rule, Field: field, Msg: "invalid forwarding address"}
		}
		if a.Type == "forward" {
			c.require("copy")
			return fmt.Sprintf("redirect :copy %s;", quote(addr)), nil
		}
		return fmt.Sprintf("redirect %s;", quote(addr)), nil
	case "discard":
		return "discard;", nil
	case "stop":
		return "stop;", nil
	}
	return "", &ValidationError{Rule: rule, Field: field, Msg: "unknown action " + a.Type}
}

// Compile turns rules and vacation settings into a complete Sieve script.
// Disabled rules are emitted as comments so nothing is silently lost.
func Compile(rules []model.SieveRule, vacation *model.Vacation) (string, error) {
	c := &compiler{requires: map[string]bool{}}
	var body strings.Builder

	if vacation != nil && vacation.Enabled {
		days := vacation.Days
		if days <= 0 {
			days = 7
		}
		if days > 30 {
			days = 30
		}
		subject := strings.TrimSpace(vacation.Subject)
		text := strings.TrimSpace(vacation.Body)
		if text == "" {
			return "", &ValidationError{Field: "vacation.body", Msg: "auto-reply text is required"}
		}
		c.require("vacation")
		body.WriteString("# Auto-reply\r\n")
		body.WriteString(fmt.Sprintf("vacation :days %d", days))
		if subject != "" {
			body.WriteString(" :subject " + quote(subject))
		}
		body.WriteString(" " + multiline(text))
		body.WriteString("\r\n")
	}

	for _, r := range rules {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = "Untitled rule"
		}
		if !r.Enabled {
			body.WriteString("# (disabled) " + strings.ReplaceAll(name, "\n", " ") + "\r\n\r\n")
			continue
		}
		if len(r.Conditions) == 0 {
			return "", &ValidationError{Rule: name, Field: "conditions", Msg: "at least one condition is required"}
		}
		if len(r.Actions) == 0 {
			return "", &ValidationError{Rule: name, Field: "actions", Msg: "at least one action is required"}
		}
		tests := make([]string, 0, len(r.Conditions))
		for i, cond := range r.Conditions {
			t, err := c.condition(name, i, cond)
			if err != nil {
				return "", err
			}
			tests = append(tests, t)
		}
		combinator := "allof"
		if r.Match == "any" {
			combinator = "anyof"
		}
		body.WriteString("# Rule: " + strings.ReplaceAll(name, "\n", " ") + "\r\n")
		if len(tests) == 1 {
			body.WriteString("if " + tests[0] + " {\r\n")
		} else {
			body.WriteString("if " + combinator + " (" + strings.Join(tests, ",\r\n          ") + ") {\r\n")
		}
		for i, a := range r.Actions {
			s, err := c.action(name, i, a)
			if err != nil {
				return "", err
			}
			body.WriteString("    " + s + "\r\n")
		}
		body.WriteString("}\r\n\r\n")
	}

	var out strings.Builder
	out.WriteString("# Generated by Mailhearth. Edit your rules in Mailhearth; manual changes are overwritten.\r\n")
	if len(c.requires) > 0 {
		order := []string{"fileinto", "imap4flags", "copy", "body", "vacation"}
		var reqs []string
		for _, ext := range order {
			if c.requires[ext] {
				reqs = append(reqs, `"`+ext+`"`)
			}
		}
		out.WriteString("require [" + strings.Join(reqs, ", ") + "];\r\n\r\n")
	} else {
		out.WriteString("\r\n")
	}
	out.WriteString(body.String())
	return out.String(), nil
}
