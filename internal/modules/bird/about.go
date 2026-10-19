package bird

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"
	"unicode"
)

// maxPhoto is far above an iNaturalist medium photo, about 50 KB.
const maxPhoto = 1 << 20

// openLicences are the photo licences that let skua repost with credit.
var openLicences = map[string]string{"cc0": "cc0", "cc-by": "cc by", "cc-by-sa": "cc by-sa"}

// about is a bird's photo and one line about it. Each part is kept only
// when it is matched to the recording's scientific name: the photo is the
// iNaturalist taxon of exactly that name, and the line is Wikipedia's
// article for that name, kept only when it is the article iNaturalist
// links for the same taxon. Two databases agreeing is the check.
type about struct {
	photo     []byte
	photoName string
	credit    string // the photo's attribution, screened and escaped
	source    string // the photo's iNaturalist page
	line      string // screened and escaped
	article   string
}

// taxon is the part of an iNaturalist taxa result bird reads.
type taxon struct {
	ID           int        `json:"id"`
	Name         string     `json:"name"`
	Rank         string     `json:"rank"`
	Active       bool       `json:"is_active"`
	Iconic       string     `json:"iconic_taxon_name"`
	WikipediaURL string     `json:"wikipedia_url"`
	Photo        *inatPhoto `json:"default_photo"`
	// Photos is the taxon's curated photos, which only the taxon's own
	// page carries.
	Photos []struct {
		Photo inatPhoto `json:"photo"`
	} `json:"taxon_photos"`
}

type inatPhoto struct {
	ID          int    `json:"id"`
	Licence     string `json:"license_code"`
	Attribution string `json:"attribution"`
	Medium      string `json:"medium_url"`
	Flags       []any  `json:"flags"`
}

// open is whether p may be posted: openly licensed, unflagged and on
// iNaturalist's open data bucket, the one host a photo is fetched from.
func (m *Module) open(p *inatPhoto) bool {
	_, ok := openLicences[p.Licence]
	return ok && len(p.Flags) == 0 && strings.HasPrefix(p.Medium, m.photos) && !strings.Contains(p.Medium[len(m.photos):], "..")
}

// lookUp finds rec's photo and line. Anything that doesn't match, or
// doesn't answer, leaves its part empty.
func (m *Module) lookUp(ctx context.Context, rec recording) about {
	name := rec.species()
	if rec.Gen == "" || rec.Sp == "" || m.inat == "" {
		return about{}
	}
	q := url.Values{"q": {name}, "rank": {"species"}, "is_active": {"true"}, "per_page": {"5"}}
	body, _, err := m.get(ctx, m.inat+"?"+q.Encode(), 1<<20)
	if err != nil {
		return about{}
	}
	var res struct {
		Results []taxon `json:"results"`
	}
	if json.Unmarshal(body, &res) != nil {
		return about{}
	}
	var t *taxon
	for i, r := range res.Results {
		if strings.EqualFold(r.Name, name) && r.Rank == "species" && r.Active && r.Iconic == "Aves" {
			t = &res.Results[i]
			break
		}
	}
	if t == nil {
		return about{}
	}
	var a about
	lined := make(chan struct{})
	go func() { defer close(lined); a.line, a.article = m.line(ctx, rec, t.WikipediaURL) }()
	a.photo, a.photoName, a.credit, a.source = m.photo(ctx, t)
	<-lined
	return a
}

// photo is t's default photo when it is open, or else the first open one
// of the taxon's curated photos: the default is often licensed for
// non-commercial use only, and one of the others rarely is not.
func (m *Module) photo(ctx context.Context, t *taxon) (img []byte, name, credit, source string) {
	p := t.Photo
	if p == nil || !m.open(p) {
		p = nil
		body, _, err := m.get(ctx, fmt.Sprintf("%s/%d", m.inat, t.ID), 1<<20)
		var res struct {
			Results []taxon `json:"results"`
		}
		if err != nil || json.Unmarshal(body, &res) != nil || len(res.Results) != 1 || res.Results[0].ID != t.ID {
			return nil, "", "", ""
		}
		for i := range res.Results[0].Photos {
			if c := &res.Results[0].Photos[i].Photo; m.open(c) {
				p = c
				break
			}
		}
		if p == nil {
			return nil, "", "", ""
		}
	}
	licence := openLicences[p.Licence]
	img, kind, err := m.get(ctx, p.Medium, maxPhoto)
	if err != nil {
		return nil, "", "", ""
	}
	switch {
	case strings.HasPrefix(kind, "image/jpeg") && len(img) > 2 && img[0] == 0xFF && img[1] == 0xD8:
		name = "bird.jpg"
	case strings.HasPrefix(kind, "image/png") && strings.HasPrefix(string(img), "\x89PNG"):
		name = "bird.png"
	default:
		return nil, "", "", ""
	}
	// "(c) Luiz Lapa, some rights reserved (CC BY)" is the photographer
	// and the licence; the licence is said once, in its own words.
	who, _, _ := strings.Cut(p.Attribution, ", ")
	who = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(who, "(c)"), "(C)"))
	if who == "" || len(who) > 80 {
		who = "inaturalist"
	}
	credit = m.screen.Check(strings.ToLower(who)).Text + ", " + licence
	return img, name, markdown.Replace(credit), fmt.Sprintf("https://www.inaturalist.org/photos/%d", p.ID)
}

// line is one sentence of Wikipedia's article for rec's scientific name,
// when that article is the one iNaturalist links for the same taxon.
func (m *Module) line(ctx context.Context, rec recording, linked string) (string, string) {
	want := articleOf(linked)
	if want == "" || m.wiki == "" {
		return "", ""
	}
	body, _, err := m.get(ctx, m.wiki+url.PathEscape(rec.Gen+"_"+rec.Sp), 1<<20)
	if err != nil {
		return "", ""
	}
	var s struct {
		Type   string `json:"type"`
		Titles struct {
			Canonical string `json:"canonical"`
		} `json:"titles"`
		Description string `json:"description"`
		Extract     string `json:"extract"`
	}
	if json.Unmarshal(body, &s) != nil || s.Type != "standard" || !strings.EqualFold(s.Titles.Canonical, want) {
		return "", ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(rec.ID))
	text := pick(sentences(s.Extract), h.Sum32())
	if text == "" {
		text = s.Description
	}
	text = strings.Join(strings.Fields(strings.ToLower(text)), " ")
	if text == "" || len(text) > 220 {
		return "", ""
	}
	return markdown.Replace(m.screen.Check(text).Text), "https://en.wikipedia.org/wiki/" + url.PathEscape(s.Titles.Canonical)
}

// articleOf is the article title a Wikipedia link names, or "".
func articleOf(link string) string {
	u, err := url.Parse(link)
	if err != nil || !strings.HasSuffix(u.Host, "wikipedia.org") || !strings.HasPrefix(u.Path, "/wiki/") {
		return ""
	}
	return strings.ReplaceAll(strings.TrimPrefix(u.Path, "/wiki/"), " ", "_")
}

// sentences splits a summary at a full stop followed by a space and a
// capital, but never after what looks like an abbreviation ("St.", "c.",
// "Mt."), and keeps only whole plain sentences long enough to say
// something.
func sentences(text string) []string {
	var out []string
	rs := []rune(text)
	start := 0
	for i := range rs {
		end := i == len(rs)-1
		if !end && (rs[i] != '.' || i+2 >= len(rs) || rs[i+1] != ' ' || !unicode.IsUpper(rs[i+2]) || abbreviated(rs[start:i])) {
			continue
		}
		s := strings.TrimSpace(string(rs[start : i+1]))
		start = i + 2
		if strings.HasSuffix(s, ".") && !abbreviated([]rune(s[:len(s)-1])) && len(strings.Fields(s)) >= 6 && len(s) <= 200 && !strings.ContainsAny(s, "()[]") {
			out = append(out, s)
		}
	}
	return out
}

// abbreviated is whether text ends in a word that reads as an abbreviation
// before a full stop: one letter, or up to three starting with a capital.
func abbreviated(text []rune) bool {
	i := len(text)
	for i > 0 && text[i-1] != ' ' {
		i--
	}
	w := text[i:]
	return len(w) == 1 || (len(w) > 0 && len(w) <= 3 && unicode.IsUpper(w[0]))
}

func pick(s []string, seed uint32) string {
	if len(s) == 0 {
		return ""
	}
	return s[int(seed%uint32(len(s)))]
}

// lines is what about adds under the marker: the line, then the photo's
// credit, each subtext and each linked to where it came from.
func (a about) lines() string {
	var b strings.Builder
	if a.line != "" {
		fmt.Fprintf(&b, "\n-# %s [wikipedia](<%s>)", a.line, a.article)
	}
	if a.photo != nil {
		fmt.Fprintf(&b, "\n-# photo [%s](<%s>)", a.credit, a.source)
	}
	return b.String()
}
