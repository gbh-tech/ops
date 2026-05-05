package utils

import "strings"

func OrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func JoinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}
