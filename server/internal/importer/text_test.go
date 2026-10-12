package importer

import (
	"encoding/json"
	"testing"
)

func TestADFToMarkdown(t *testing.T) {
	doc := `{"type":"doc","version":1,"content":[
		{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Steps"}]},
		{"type":"paragraph","content":[
			{"type":"text","text":"Open "},
			{"type":"text","text":"the app","marks":[{"type":"strong"}]},
			{"type":"text","text":" and "},
			{"type":"text","text":"read this","marks":[{"type":"link","attrs":{"href":"https://example.com/a"}},{"type":"em"}]},
			{"type":"text","text":", then run "},
			{"type":"text","text":"make *all*","marks":[{"type":"code"}]},
			{"type":"hardBreak"},
			{"type":"text","text":"Ask "},
			{"type":"mention","attrs":{"id":"abc123","text":"@Ada Lovelace"}},
			{"type":"text","text":" about snake_case "},
			{"type":"emoji","attrs":{"shortName":":tada:","text":"🎉"}}]},
		{"type":"bulletList","content":[
			{"type":"listItem","content":[
				{"type":"paragraph","content":[{"type":"text","text":"First"}]},
				{"type":"orderedList","content":[
					{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Nested one"}]}]},
					{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Nested two"}]}]}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Second","marks":[{"type":"strike"}]}]}]}]},
		{"type":"taskList","content":[
			{"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"Shipped"}]},
			{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"Announce"}]}]},
		{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"fmt.Println(\"hi\")\n// done"}]},
		{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Careful."}]}]},
		{"type":"rule"},
		{"type":"table","content":[
			{"type":"tableRow","content":[
				{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Name"}]}]},
				{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Value"}]}]}]},
			{"type":"tableRow","content":[
				{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"a|b"}]}]},
				{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"1"}]}]}]}]},
		{"type":"mediaSingle","content":[{"type":"media","attrs":{"type":"file","id":"m-1","alt":"screenshot.png"}}]},
		{"type":"paragraph","content":[{"type":"inlineCard","attrs":{"url":"https://example.com/card"}},{"type":"text","text":" "},{"type":"status","attrs":{"text":"BLOCKED"}}]},
		{"type":"somethingNew","content":[{"type":"paragraph","content":[{"type":"text","text":"Still readable."}]}]}
	]}`
	want := "## Steps\n\n" +
		"Open **the app** and [*read this*](https://example.com/a), then run `make *all*`\n" +
		"Ask @Ada Lovelace about snake\\_case 🎉\n\n" +
		"- First\n  1. Nested one\n  2. Nested two\n- ~~Second~~\n\n" +
		"- [x] Shipped\n- [ ] Announce\n\n" +
		"```go\nfmt.Println(\"hi\")\n// done\n```\n\n" +
		"> Careful.\n\n" +
		"---\n\n" +
		"| Name | Value |\n| --- | --- |\n| a\\|b | 1 |\n\n" +
		"_(attached file: screenshot.png)_\n\n" +
		"<https://example.com/card> `BLOCKED`\n\n" +
		"Still readable."
	if got := adfToMarkdown(json.RawMessage(doc)); got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
	// Not a document: a plain string, null, nothing.
	if got := adfToMarkdown(json.RawMessage(`"plain text"`)); got != "plain text" {
		t.Errorf("a string field = %q", got)
	}
	if got := adfToMarkdown(json.RawMessage(`null`)); got != "" {
		t.Errorf("null = %q", got)
	}
	if got := adfToMarkdown(nil); got != "" {
		t.Errorf("nothing = %q", got)
	}
}

func TestWikiToMarkdown(t *testing.T) {
	wiki := "h2. Steps\n\n" +
		"Open *the app* and _read_ [the docs|https://example.com/a] or [https://example.com/b].\n" +
		"Run {{make *all*}} on 2026-10-01 - not earlier - with snake_case and -old- flags, +underlined+.\n" +
		"* First\n** Nested\n# One\n## Two\n" +
		"{code:java}\nint x = *p;\n{code}\n" +
		"{noformat}plain _text_{noformat}\n" +
		"{quote}\nSaid before.\n{quote}\n" +
		"bq. Also quoted\n" +
		"||Name||Value||\n|a|*1*|\n" +
		"----\n" +
		"!screenshot.png|thumbnail! by [~ada] and [~accountid:5b10a2844c20165700ede21g]\n" +
		"{color:red}Urgent{color} {panel:title=Note}In a panel{panel}"
	want := "## Steps\n\n" +
		"Open **the app** and *read* [the docs](https://example.com/a) or <https://example.com/b>.\n" +
		"Run `make *all*` on 2026-10-01 - not earlier - with snake_case and ~~old~~ flags, underlined.\n" +
		"- First\n  - Nested\n1. One\n  1. Two\n" +
		"```java\nint x = *p;\n```\n" +
		"```\nplain _text_\n```\n" +
		"\n> Said before.\n\n" +
		"> Also quoted\n" +
		"| Name | Value |\n| --- | --- |\n| a | **1** |\n" +
		"---\n" +
		"![](screenshot.png) by @ada and @5b10a2844c20165700ede21g\n" +
		"Urgent In a panel"
	if got := wikiToMarkdown(wiki); got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
	if got := wikiToMarkdown("  \n"); got != "" {
		t.Errorf("blank = %q", got)
	}
	// A table without a header row still becomes a table.
	if got := wikiToMarkdown("|a|b|\n|c|d|"); got != "|  |  |\n| --- | --- |\n| a | b |\n| c | d |" {
		t.Errorf("headerless table:\n%s", got)
	}
	// Code left open is closed.
	if got := wikiToMarkdown("{code}\nx"); got != "```\nx\n```" {
		t.Errorf("unclosed code:\n%s", got)
	}
}
