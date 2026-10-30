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

// TagKey is the tag every review app resource carries, with value pr-<n>.
const TagKey = "reviewapp"

// Namespace returns the Service Connect namespace of a review app.
func Namespace(pr int) string {
	return fmt.Sprintf("reviewapp-pr-%d", pr)
}

// Database returns the database of a review app.
func Database(pr int) string {
	return fmt.Sprintf("reviewapp_pr_%d", pr)
}

// TopicPrefix returns the Kafka topic prefix of a review app.
func TopicPrefix(pr int) string {
	return fmt.Sprintf("pr-%d.", pr)
}

// DataPrefix returns the key prefix of a review app in the data bucket.
func DataPrefix(pr int) string {
	return fmt.Sprintf("pr-%d/", pr)
}

// Family returns the task definition family of a unit.
func Family(pr int, unit string) string {
	return fmt.Sprintf("reviewapp-pr-%d-%s", pr, unit)
}

// ToolsFamily returns the task definition family of the helper tasks.
func ToolsFamily(pr int) string {
	return Family(pr, "tools")
}

// TargetGroup returns the target group name of a unit: pr-<n>-<unit>, cut
// to the 32-character limit without a trailing hyphen. The controller's IAM
// policy only allows target groups named pr-*.
func TargetGroup(pr int, unit string) string {
	name := fmt.Sprintf("pr-%d-%s", pr, unit)
	if len(name) > 32 {
		name = name[:32]
	}
	for len(name) > 0 && name[len(name)-1] == '-' {
		name = name[:len(name)-1]
	}
	return name
}
