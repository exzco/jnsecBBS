package content_test

import (
	"testing"

	"github.com/exzco/bbs/internal/models"
)

func TestDecodeContent(t *testing.T) {
	for _, tc := range []struct{ name, stored, source string }{
		{"formatted", `<r><p><STRONG><s>**</s>加粗<e>**</e></STRONG> &amp; <URL url="https://example.org"><s>[</s>链接<e>](https://example.org)</e></URL></p></r>`, "**加粗** & [链接](https://example.org)"},
		{"newlines", "<t><p>第一行<br/>\n第二行</p>\n\n<p>第三行</p></t>", "第一行\n第二行\n\n第三行"},
		{"code", `<r><CODE><s>` + "```go\n" + `</s>&lt;script&gt; &amp;lt;<e>` + "\n```" + `</e></CODE></r>`, "```go\n<script> &lt;\n```"},
		{"old Go", `{"text":"# Title\n\n**bold**"}`, "# Title\n\n**bold**"},
		{"JSON example", `{"answer":42}`, `{"answer":42}`},
		{"raw", "# Title\n\n**bold**", "# Title\n\n**bold**"},
		{"invalid", "<r><p>broken", "<r><p>broken"},
		{"entity", `<t>&lt;img src=x onerror=alert(1)&gt;</t>`, `<img src=x onerror=alert(1)>`},
		{"quote reply", `<r><QUOTE><i>> </i><p>quote text</p></QUOTE><p>reply text</p></r>`, "> quote text\n\nreply text"},
		{"standalone br", `<r><p>第一行<br/>第二行</p></r>`, "第一行\n第二行"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := models.Post{Type: "comment", Content: tc.stored}
			if got := p.Markdown(); got != tc.source {
				t.Fatalf("got %q, want %q", got, tc.source)
			}
		})
	}
}

func TestContentRoundTrip(t *testing.T) {
	for _, source := range []string{"", "# 中文\n\n**bold** & <tag> \"quotes\"", "```go\nfmt.Println(1)\n```", "a\r\nb\t🙂"} {
		p := models.Post{Type: "comment"}
		p.SetMarkdown(source)
		if got := p.Markdown(); got != source {
			t.Fatalf("got %q, want %q", got, source)
		}
	}
}
