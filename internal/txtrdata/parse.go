package txtrdata

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse converts TXT presentation-format RDATA into a single plain-text value.
// If the input does not contain quoted TXT chunks, it is returned as-is.
func Parse(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	if !strings.Contains(input, "\"") {
		return input, nil
	}

	var out strings.Builder
	for len(input) > 0 {
		input = strings.TrimSpace(input)
		if input == "" {
			break
		}
		if input[0] != '"' {
			return "", fmt.Errorf("invalid TXT RDATA %q", input)
		}

		end := findQuotedChunkEnd(input)
		if end <= 0 {
			return "", fmt.Errorf("unterminated TXT chunk in %q", input)
		}

		part, err := strconv.Unquote(input[:end])
		if err != nil {
			return "", fmt.Errorf("unquoting TXT chunk %q: %w", input[:end], err)
		}
		out.WriteString(part)
		input = input[end:]
	}

	return out.String(), nil
}

func findQuotedChunkEnd(input string) int {
	escaped := false
	for i := 1; i < len(input); i++ {
		switch {
		case escaped:
			escaped = false
		case input[i] == '\\':
			escaped = true
		case input[i] == '"':
			return i + 1
		}
	}
	return -1
}
