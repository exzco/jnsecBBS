// Package content provides forum text encoding, safe HTML rendering and URL slugs.
package content

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
)

// DecodeContent reverses s9e TextFormatter's XML representation. Formatting
// markers (s/e tags) are part of the original source and must be retained.
// Like s9e's Unparser, concatenate text without adding newlines for XML tags:
// the original newlines already exist next to <br/> and paragraph boundaries.
// Never interpret arbitrary Markdown as XML or silently drop malformed input.
func DecodeContent(content string) string {
	if strings.HasPrefix(content, "<r>") || strings.HasPrefix(content, "<t>") {
		decoder := xml.NewDecoder(strings.NewReader(content))
		var source strings.Builder
		pendingBR := false
		pendingQuoteBreak := false
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				return source.String()
			}
			if err != nil {
				return content
			}
			switch tok := token.(type) {
			case xml.StartElement:
				if tok.Name.Local == "br" {
					pendingBR = true
				}
			case xml.EndElement:
				if tok.Name.Local == "QUOTE" {
					pendingQuoteBreak = true
				}
			case xml.CharData:
				if pendingBR {
					if !strings.HasPrefix(string(tok), "\n") && !strings.HasSuffix(source.String(), "\n") {
						source.WriteByte('\n')
					}
					pendingBR = false
				}
				if pendingQuoteBreak {
					if !strings.HasSuffix(source.String(), "\n\n") {
						if strings.HasSuffix(source.String(), "\n") {
							source.WriteByte('\n')
						} else {
							source.WriteString("\n\n")
						}
					}
					pendingQuoteBreak = false
				}
				source.Write(tok)
			}
		}
	}
	// Earlier Go versions stored JSON. Only recognize an actual text field;
	// valid JSON examples such as {"answer":42} are ordinary post content.
	var old struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal([]byte(content), &old) == nil && old.Text != nil {
		return *old.Text
	}
	return content
}

// EncodeContent uses s9e's plain-text XML envelope. This preserves the complete
// Markdown source for either editor and is readable by the PHP renderer. PHP
// displays it as text until its formatter reparses it; Go renders Markdown.
func EncodeContent(source string) string {
	var out strings.Builder
	out.WriteString("<t>")
	_ = xml.EscapeText(&out, []byte(source))
	out.WriteString("</t>")
	return out.String()
}
