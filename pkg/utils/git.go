package utils

import (
	"os/exec"
	"regexp"
	"strings"

	"github.com/charmbracelet/log"
)

func CurrentBranch() string {
	ref := exec.Command("git", "branch", "--show-current")

	out, err := ref.Output()
	if err != nil {
		log.Fatalf("Failed to determine current branch: %v", err)
	}

	return string(out)
}

func GetTicketId(ref string) string {
	if strings.TrimSpace(ref) == "main" {
		return ref
	}

	re := regexp.MustCompile(`[A-Za-z]{2,}-[0-9]+`)
	match := re.FindString(ref)

	if match == "" {
		log.Error("Failed to extract Ticket ID.")
		log.Fatalf("Ref '%s' %s", strings.TrimSpace(ref), "doesn't match the expected name convention.")
	}

	ticket := re.FindString(ref)

	return strings.ToUpper(ticket)
}
