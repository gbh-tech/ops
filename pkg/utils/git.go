package utils

import (
	"os/exec"
	"regexp"
	"strings"

	"charm.land/log/v2"
)

func CurrentBranch() string {
	ref := exec.Command("git", "branch", "--show-current")

	out, err := ref.Output()
	if err != nil {
		log.Fatal("Failed to determine current branch", "err", err)
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
		log.Fatal("Ref doesn't match the expected name convention", "ref", strings.TrimSpace(ref))
	}

	ticket := re.FindString(ref)

	return strings.ToUpper(ticket)
}
