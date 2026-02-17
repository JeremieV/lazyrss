package rss

import (
	"encoding/xml"
	"io"
)

type OPML struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    Head     `xml:"head"`
	Body    Body     `xml:"body"`
}

type Head struct {
	Title string `xml:"title"`
}

type Body struct {
	Outlines []Outline `xml:"outline"`
}

type Outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr,omitempty"`
	Type     string    `xml:"type,attr,omitempty"`
	XMLURL   string    `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string    `xml:"htmlUrl,attr,omitempty"`
	Outlines []Outline `xml:"outline,omitempty"`
}

func ParseOPML(r io.Reader) (*OPML, error) {
	var opml OPML
	if err := xml.NewDecoder(r).Decode(&opml); err != nil {
		return nil, err
	}
	return &opml, nil
}

func (o *OPML) Flatten() []Outline {
	var flattened []Outline
	var traverse func([]Outline)
	traverse = func(outlines []Outline) {
		for _, out := range outlines {
			if out.Type == "rss" || out.XMLURL != "" {
				flattened = append(flattened, out)
			}
			if len(out.Outlines) > 0 {
				traverse(out.Outlines)
			}
		}
	}
	traverse(o.Body.Outlines)
	return flattened
}

func GenerateOPML(outlines []Outline) ([]byte, error) {
	opml := OPML{
		Version: "2.0",
		Head: Head{
			Title: "CLI RSS Feeds",
		},
		Body: Body{
			Outlines: outlines,
		},
	}
	return xml.MarshalIndent(opml, "", "  ")
}

