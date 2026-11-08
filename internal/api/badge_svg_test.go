package api

import (
	"encoding/xml"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBadgeSVGAccessibleTextAndEscaping(t *testing.T) {
	for _, tc := range []struct {
		label, width string
	}{
		{"A&B", "59"},
		{"É", "45"},
		{"e\u0301", "45"},
		{"東京", "66"},
		{"Ａpi", "66"},
		{"♥\ufe0f", "52"},
		{`<script>alert("x")</script>`, ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeBadgeSVG(w, tc.label, "up", colorOK)
			decoder := xml.NewDecoder(strings.NewReader(w.Body.String()))
			texts, titles := 0, 0
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("invalid badge SVG: %v", err)
				}
				if el, ok := token.(xml.StartElement); ok {
					switch el.Name.Local {
					case "script", "linearGradient":
						t.Errorf("unexpected %s element", el.Name.Local)
					case "text":
						texts++
					case "title":
						titles++
					case "svg":
						attrs := map[string]string{}
						for _, attr := range el.Attr {
							attrs[attr.Name.Local] = attr.Value
						}
						if attrs["role"] != "img" || attrs["aria-label"] != tc.label+": up" {
							t.Errorf("badge has no complete accessible name: %v", attrs)
						}
						if tc.width != "" && (attrs["width"] != tc.width || attrs["viewBox"] != "0 0 "+tc.width+" 20") {
							t.Errorf("badge width counts escaped bytes instead of characters: %v", attrs)
						}
					}
				}
			}
			if texts != 2 || titles != 1 {
				t.Errorf("badge has %d text layers and %d titles", texts, titles)
			}
		})
	}
}

func TestBadgeSVGReplacesCharactersForbiddenInXML(t *testing.T) {
	w := httptest.NewRecorder()
	writeBadgeSVG(w, "A\x00B\x01", "up", colorOK)
	decoder := xml.NewDecoder(strings.NewReader(w.Body.String()))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("control character broke SVG: %v", err)
		}
	}
	if !strings.Contains(w.Body.String(), "A�B�") {
		t.Fatal("invalid XML text was not replaced consistently")
	}
}
