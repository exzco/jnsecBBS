package markdown

import (
	"github.com/exzco/bbs/internal/flarum"
	"strings"
	"testing"
)

func TestLegacyMarkdownRendering(t *testing.T) {
	source := flarum.DecodeContent(`<r><p><STRONG><s>**</s>加粗<e>**</e></STRONG></p></r>`)
	rendered, _ := Render(source, nil)
	if !strings.Contains(rendered, "<strong>加粗</strong>") {
		t.Fatalf("lost formatting: %s", rendered)
	}
	for _, source := range []string{`<script>alert(1)</script>`, `[x](javascript:alert%281%29)`, flarum.DecodeContent(`<t>&lt;img src=x onerror=alert(1)&gt;</t>`)} {
		rendered, _ := Render(source, nil)
		if strings.Contains(rendered, "<script") || strings.Contains(rendered, "javascript:") || strings.Contains(rendered, "<img src=x") {
			t.Fatalf("unsafe output: %s", rendered)
		}
	}
}

func TestUplTagsAndMentions(t *testing.T) {
	imgSource := `[upl-image-preview url=https://bbs.jnsec.org/assets/files/2022-04-08/1649409498-739055-image.png]`
	html, _ := Render(imgSource, nil)
	if !strings.Contains(html, `<img src="https://bbs.jnsec.org/assets/files/2022-04-08/1649409498-739055-image.png"`) {
		t.Fatalf("failed to render upl-image-preview as img: %s", html)
	}

	fileSource := `[upl-file uuid=9393e74e-f7a4-46fc-93b0-1721c6ac6cfd size=712kB]malloc.pdf[/upl-file]`
	html, _ = Render(fileSource, nil)
	if !strings.Contains(html, `/api/fof/download/9393e74e-f7a4-46fc-93b0-1721c6ac6cfd`) || !strings.Contains(html, `malloc.pdf`) {
		t.Fatalf("failed to render upl-file download link: %s", html)
	}

	mentionSource := `@"moyue"#p14553 关注一下`
	html, _ = Render(mentionSource, nil)
	if !strings.Contains(html, `<a href="#post-14553"`) || !strings.Contains(html, `@moyue #14553`) {
		t.Fatalf("failed to render post mention link: %s", html)
	}
}
