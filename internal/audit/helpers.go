package audit

import (
	"net/mail"
	"regexp"
	"strings"
)

var angleBracketRe = regexp.MustCompile(`^[<\s]*(.*?)[>\s]*$`)

const listIDMatchGroups = 2

func domainOf(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}
	addrs, err := mail.ParseAddressList(from)
	if err != nil {
		return extractDomain(from)
	}
	for _, addr := range addrs {
		if dom := extractDomain(addr.Address); dom != "" {
			return dom
		}
	}
	return ""
}

func extractDomain(address string) string {
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return ""
	}
	at := strings.LastIndex(address, "@")
	if at == -1 {
		return ""
	}
	domain := address[at+1:]
	return strings.Trim(domain, ". ")
}

func normalizeListID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if matches := angleBracketRe.FindStringSubmatch(raw); len(matches) == listIDMatchGroups {
		raw = matches[1]
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ">")
	raw = strings.TrimPrefix(raw, "<")
	raw = strings.Trim(raw, "\" ")
	return strings.ToLower(raw)
}
