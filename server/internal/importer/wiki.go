package importer

import (
	"regexp"
	"strings"
)

// Jira Server and Data Center store text in Jira's own wiki markup.
// wikiToMarkdown rewrites the common forms as Markdown; markup it doesn't
// know is left as it was written, which is still readable.

var (
	wikiHeading   = regexp.MustCompile(`^h([1-6])\.\s*(.*)$`)
	wikiListItem  = regexp.MustCompile(`^([*#-]+)\s+(.*)$`)
	wikiCodeOpen  = regexp.MustCompile(`^\{(code|noformat)(?::([^}]*))?\}`)
	wikiBlockTag  = regexp.MustCompile(`\{(quote|panel|color|info|note|warning|tip)(?::[^}]*)?\}`)
	wikiLink      = regexp.MustCompile(`\[([^\]|\n]+)\|([^\]|\n]+)(?:\|[^\]\n]*)?\]`)
	wikiBareLink  = regexp.MustCompile(`\[((?:https?|ftp)://[^\]\s|]+)\]`)
	wikiMention   = regexp.MustCompile(`\[~(?:accountid:)?([^\]]+)\]`)
	wikiImage     = regexp.MustCompile(`!([^!|\s]+)(?:\|[^!\n]*)?!`)
	wikiMono      = regexp.MustCompile(`\{\{(.+?)\}\}`)
	wikiBold      = regexp.MustCompile(`(^|[\s(\[>|])\*(\S(?:[^*\n]*?\S)?)\*($|[\s.,;:!?)\]<|])`)
	wikiItalic    = regexp.MustCompile(`(^|[\s(\[>|])_(\S(?:[^_\n]*?\S)?)_($|[\s.,;:!?)\]<|])`)
	wikiStrike    = regexp.MustCompile(`(^|[\s(\[>|])-(\S(?:[^-\n]*?\S)?)-($|[\s.,;:!?)\]<|])`)
	wikiUnderline = regexp.MustCompile(`(^|[\s(\[>|])\+(\S(?:[^+\n]*?\S)?)\+($|[\s.,;:!?)\]<|])`)
	wikiCite      = regexp.MustCompile(`\?\?(.+?)\?\?`)
)

func wikiToMarkdown(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	inCode, inQuote, inTable := false, false, false
	for _, line := range lines {
		// Code is copied through untouched until its closing tag.
		if inCode {
			if i := strings.Index(line, "{code}"); i >= 0 {
				inCode = false
				if rest := line[:i]; rest != "" {
					out = append(out, rest)
				}
				out = append(out, "```")
				continue
			}
			if i := strings.Index(line, "{noformat}"); i >= 0 {
				inCode = false
				if rest := line[:i]; rest != "" {
					out = append(out, rest)
				}
				out = append(out, "```")
				continue
			}
			out = append(out, line)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if m := wikiCodeOpen.FindStringSubmatch(trimmed); m != nil {
			language := ""
			if m[1] == "code" {
				// {code:java} or {code:title=X|language=java}
				for _, part := range strings.Split(m[2], "|") {
					if key, value, found := strings.Cut(part, "="); !found && part != "" {
						language = part
					} else if key == "language" {
						language = value
					}
				}
			}
			out = append(out, "```"+language)
			rest := trimmed[len(m[0]):]
			// Opened and closed on one line.
			if i := strings.Index(rest, "{"+m[1]+"}"); i >= 0 {
				out = append(out, rest[:i], "```")
			} else {
				inCode = true
				if rest != "" {
					out = append(out, rest)
				}
			}
			continue
		}

		// {quote} opens and closes a quotation; other block tags are dropped.
		for _, tag := range wikiBlockTag.FindAllStringSubmatch(line, -1) {
			if tag[1] == "quote" {
				inQuote = !inQuote
			}
		}
		quoted := inQuote || strings.Contains(line, "{quote}")
		line = strings.TrimSpace(wikiBlockTag.ReplaceAllString(line, ""))
		if strings.HasPrefix(line, "bq. ") {
			line, quoted = line[4:], true
		}

		isRow := strings.HasPrefix(line, "|")
		switch {
		case line == "----":
			line = "---"
		case isRow:
			header := strings.HasPrefix(line, "||")
			cells := splitWikiRow(line)
			for i, cell := range cells {
				cells[i] = wikiInline(strings.TrimSpace(cell))
			}
			row := "| " + strings.Join(cells, " | ") + " |"
			if !inTable {
				// Markdown tables need a header row; a table that begins
				// with a plain row gets an empty one.
				if !header {
					out = append(out, "|"+strings.Repeat("  |", len(cells)))
					out = append(out, "|"+strings.Repeat(" --- |", len(cells)))
					out = append(out, row)
				} else {
					out = append(out, row, "|"+strings.Repeat(" --- |", len(cells)))
				}
				inTable = true
				continue
			}
			line = row
		default:
			if m := wikiHeading.FindStringSubmatch(line); m != nil {
				line = strings.Repeat("#", int(m[1][0]-'0')) + " " + wikiInline(m[2])
			} else if m := wikiListItem.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, "---") {
				depth := len(m[1])
				marker := "- "
				if m[1][depth-1] == '#' {
					marker = "1. "
				}
				line = strings.Repeat("  ", depth-1) + marker + wikiInline(m[2])
			} else {
				line = wikiInline(line)
			}
		}
		if !isRow {
			inTable = false
		}
		if quoted && line != "" {
			line = "> " + line
		}
		out = append(out, line)
	}
	if inCode {
		out = append(out, "```")
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// splitWikiRow splits "||a||b||" or "|a|b|" into its cells.
func splitWikiRow(line string) []string {
	line = strings.ReplaceAll(line, "||", "|")
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	return strings.Split(line, "|")
}

// wikiInline rewrites the markup within a line: emphasis, links, images.
func wikiInline(line string) string {
	// Monospace first, and set aside, so nothing inside it is rewritten.
	var held []string
	line = wikiMono.ReplaceAllStringFunc(line, func(match string) string {
		held = append(held, "`"+wikiMono.FindStringSubmatch(match)[1]+"`")
		return "\x00" + string(rune('0'+len(held)-1)) + "\x00"
	})
	line = wikiImage.ReplaceAllString(line, "![]($1)")
	line = wikiLink.ReplaceAllString(line, "[$1]($2)")
	line = wikiBareLink.ReplaceAllString(line, "<$1>")
	line = wikiMention.ReplaceAllString(line, "@$1")
	// Italic before bold, so that bold's asterisks aren't taken for more markup.
	line = wikiItalic.ReplaceAllString(line, "$1\x01$2\x01$3")
	line = wikiBold.ReplaceAllString(line, "$1**$2**$3")
	line = strings.ReplaceAll(line, "\x01", "*")
	line = wikiStrike.ReplaceAllString(line, "$1~~$2~~$3")
	line = wikiUnderline.ReplaceAllString(line, "$1$2$3")
	line = wikiCite.ReplaceAllString(line, "*$1*")
	for i, code := range held {
		line = strings.ReplaceAll(line, "\x00"+string(rune('0'+i))+"\x00", code)
	}
	return line
}
