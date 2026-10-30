// Package reviewapp holds the naming conventions shared by the client, the
// controller and the read-only commands of ops reviewapp (RFC 0004). Every
// name derives from the PR number, so no command needs a lookup table.
package reviewapp

import "fmt"

// Host returns the review app host, e.g. pr-123.reviewapps.example.com.
func Host(pr int, domain string) string {
	return fmt.Sprintf("pr-%d.%s", pr, domain)
}

// URL returns the review app URL.
func URL(pr int, domain string) string {
	return "https://" + Host(pr, domain)
}

// Service returns the ECS service name of a unit, e.g. pr-123-shell.
func Service(pr int, unit string) string {
	return fmt.Sprintf("pr-%d-%s", pr, unit)
}

// LogStreamPrefix returns the awslogs stream prefix of a review app's tasks.
// Streams are <prefix>/<container>/<task id>, so one prefix finds every unit
// of a review app inside the unit's log group.
func LogStreamPrefix(pr int) string {
	return fmt.Sprintf("pr-%d", pr)
}
