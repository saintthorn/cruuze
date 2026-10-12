package importer

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Jira Cloud stores text as a tree it calls the Atlassian Document Format.
// adfToMarkdown writes such a tree out as Markdown. What Markdown has no
// form for (text colour, panels' colours, layout columns) keeps its words
// and loses its dressing.

type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []adfMark      `json:"marks"`
	Content []adfNode      `json:"content"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// adfToMarkdown converts a field that holds an ADF document. Old data and
// some fields hold a plain string instead; that is returned as it is.
func adfToMarkdown(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var doc adfNode
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return strings.TrimSpace(adfBlocks(doc.Content, ""))
}

func (n adfNode) attr(name string) string {
	switch v := n.Attrs[name].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// adfBlocks writes block nodes one after another, a blank line between
// them, each line prefixed with indent (for lists and quotes).
func adfBlocks(nodes []adfNode, indent string) string {
	var parts []string
	for _, node := range nodes {
		if block := adfBlock(node); strings.TrimSpace(block) != "" {
			parts = append(parts, block)
		}
	}
	out := strings.Join(parts, "\n\n")
	if indent == "" {
		return out
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

func adfBlock(n adfNode) string {
	switch n.Type {
	case "paragraph":
		return adfInline(n.Content)
	case "heading":
		level := 1
		if v, ok := n.Attrs["level"].(float64); ok && v >= 1 && v <= 6 {
			level = int(v)
		}
		return strings.Repeat("#", level) + " " + adfInline(n.Content)
	case "bulletList":
		return adfList(n.Content, func(int) string { return "- " })
	case "orderedList":
		start := 1
		if v, ok := n.Attrs["order"].(float64); ok && v >= 1 {
			start = int(v)
		}
		return adfList(n.Content, func(i int) string { return fmt.Sprintf("%d. ", start+i) })
	case "taskList":
		return adfList(n.Content, func(int) string { return "- " })
	case "decisionList":
		return adfList(n.Content, func(int) string { return "- " })
	case "codeBlock":
		var code strings.Builder
		for _, c := range n.Content {
			code.WriteString(c.Text)
		}
		fence := "```"
		for strings.Contains(code.String(), fence) {
			fence += "`"
		}
		return fence + n.attr("language") + "\n" + code.String() + "\n" + fence
	case "blockquote", "panel":
		return adfBlocks(n.Content, "> ")
	case "rule":
		return "---"
	case "expand", "nestedExpand":
		body := adfBlocks(n.Content, "")
		if title := n.attr("title"); title != "" {
			return "**" + title + "**\n\n" + body
		}
		return body
	case "mediaSingle", "mediaGroup":
		var media []string
		for _, c := range n.Content {
			if m := adfMedia(c); m != "" {
				media = append(media, m)
			}
		}
		return strings.Join(media, "\n")
	case "media":
		return adfMedia(n)
	case "table":
		return adfTable(n)
	case "blockCard", "embedCard":
		if address := n.attr("url"); address != "" {
			return "<" + address + ">"
		}
		return ""
	case "layoutSection", "layoutColumn", "bodiedExtension", "doc":
		return adfBlocks(n.Content, "")
	}
	// An inline node standing where a block should, or something newer than
	// this code: its text, at least.
	if text := adfInline([]adfNode{n}); text != "" {
		return text
	}
	return adfBlocks(n.Content, "")
}

// adfList writes list items, each with its marker; what follows an item's
// first line is indented under it.
func adfList(items []adfNode, marker func(i int) string) string {
	var out []string
	for i, item := range items {
		prefix := marker(i)
		switch item.Type {
		case "taskItem":
			box := "[ ] "
			if item.attr("state") == "DONE" {
				box = "[x] "
			}
			out = append(out, prefix+box+adfInline(item.Content))
			continue
		case "decisionItem":
			out = append(out, prefix+adfInline(item.Content))
			continue
		}
		body := adfBlocks(item.Content, "")
		// Nested blocks sit tight under their item rather than a blank line away.
		body = strings.ReplaceAll(body, "\n\n", "\n")
		lines := strings.Split(body, "\n")
		pad := strings.Repeat(" ", len(prefix))
		for j, line := range lines {
			if j == 0 {
				lines[j] = prefix + line
			} else if line != "" {
				lines[j] = pad + line
			}
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return strings.Join(out, "\n")
}

func adfMedia(n adfNode) string {
	if n.Type != "media" {
		return ""
	}
	alt := n.attr("alt")
	if address := n.attr("url"); address != "" {
		return "![" + alt + "](" + address + ")"
	}
	// A file kept by Jira: the import brings attachments in separately, and
	// a media ID says nothing about which one this is except by its name.
	if alt != "" {
		return "_(attached file: " + alt + ")_"
	}
	return "_(attached file)_"
}

func adfTable(n adfNode) string {
	var rows []string
	for i, row := range n.Content {
		cells := make([]string, 0, len(row.Content))
		for _, cell := range row.Content {
			text := adfBlocks(cell.Content, "")
			// A table cell is one line.
			text = strings.ReplaceAll(strings.ReplaceAll(text, "\n\n", "<br>"), "\n", "<br>")
			cells = append(cells, strings.ReplaceAll(text, "|", `\|`))
		}
		rows = append(rows, "| "+strings.Join(cells, " | ")+" |")
		if i == 0 {
			rows = append(rows, "|"+strings.Repeat(" --- |", len(cells)))
		}
	}
	return strings.Join(rows, "\n")
}

func adfInline(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			b.WriteString(adfText(n))
		case "hardBreak":
			b.WriteString("\n")
		case "mention":
			name := strings.TrimPrefix(n.attr("text"), "@")
			if name == "" {
				name = n.attr("id")
			}
			b.WriteString("@" + name)
		case "emoji":
			if text := n.attr("text"); text != "" {
				b.WriteString(text)
			} else {
				b.WriteString(n.attr("shortName"))
			}
		case "inlineCard":
			if address := n.attr("url"); address != "" {
				b.WriteString("<" + address + ">")
			}
		case "status":
			b.WriteString("`" + n.attr("text") + "`")
		case "date":
			if ms, ok := n.Attrs["timestamp"].(string); ok {
				var millis int64
				if _, err := fmt.Sscan(ms, &millis); err == nil {
					b.WriteString(time.UnixMilli(millis).UTC().Format(time.DateOnly))
				}
			}
		case "placeholder":
		case "media", "mediaInline":
			b.WriteString(adfMedia(adfNode{Type: "media", Attrs: n.Attrs}))
		default:
			// Blocks nested where text is expected, or a node not known here.
			b.WriteString(adfInline(n.Content))
		}
	}
	return b.String()
}

// adfText is a run of text with its marks: bold, links and so on.
func adfText(n adfNode) string {
	text := n.Text
	var link string
	code := false
	for _, mark := range n.Marks {
		if mark.Type == "code" {
			code = true
		}
	}
	if code {
		text = "`" + text + "`"
	} else {
		text = escapeMarkdown(text)
	}
	for _, mark := range n.Marks {
		switch mark.Type {
		case "strong":
			text = wrapMark(text, "**")
		case "em":
			text = wrapMark(text, "*")
		case "strike":
			text = wrapMark(text, "~~")
		case "link":
			if href, ok := mark.Attrs["href"].(string); ok {
				link = href
			}
		}
	}
	if link != "" {
		return "[" + text + "](" + link + ")"
	}
	return text
}

// wrapMark puts emphasis around text, keeping surrounding spaces outside
// it: "** bold**" is not bold in Markdown.
func wrapMark(text, mark string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}
	lead := text[:strings.Index(text, trimmed)]
	trail := text[len(lead)+len(trimmed):]
	return lead + mark + trimmed + mark + trail
}

// escapeMarkdown keeps characters that Markdown would read as formatting
// from being read that way, where they could be: at the start of a line or
// around words.
var markdownEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`)

func escapeMarkdown(text string) string {
	if !strings.ContainsAny(text, "\\*_`[]<") {
		return text
	}
	return markdownEscaper.Replace(text)
}
