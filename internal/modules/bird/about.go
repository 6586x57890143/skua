package bird

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"net/url"
	"path"
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
// links for the same taxon. Two databases agreeing is the check. When
// iNaturalist has no open photo, the photo is that same article's lead
// image, taken only from Wikimedia Commons, which holds nothing unfree.
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
	var lead string
	lined := make(chan struct{})
	go func() { defer close(lined); a.line, a.article, lead = m.line(ctx, rec, t.WikipediaURL) }()
	a.photo, a.photoName, a.credit, a.source = m.photo(ctx, t)
	<-lined
	if a.photo == nil && lead != "" {
		a.photo, a.photoName, a.credit, a.source = m.commonsPhoto(ctx, lead)
	}
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
	if img, name = m.image(ctx, p.Medium); img == nil {
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

// image is the JPEG or PNG at u, checked by its type and its first bytes,
// and the name it is attached under.
func (m *Module) image(ctx context.Context, u string) ([]byte, string) {
	img, kind, err := m.get(ctx, u, maxPhoto)
	switch {
	case err != nil:
		return nil, ""
	case strings.HasPrefix(kind, "image/jpeg") && len(img) > 2 && img[0] == 0xFF && img[1] == 0xD8:
		return img, "bird.jpg"
	case strings.HasPrefix(kind, "image/png") && strings.HasPrefix(string(img), "\x89PNG"):
		return img, "bird.png"
	}
	return nil, ""
}

// commonsPhoto is the lead image of the bird's article, when it is a file
// on Wikimedia Commons under a free licence with no restriction marked on
// it, as a 500px thumbnail, credited to its author.
func (m *Module) commonsPhoto(ctx context.Context, lead string) (img []byte, name, credit, source string) {
	file, ok := m.onCommons(lead)
	if !ok || m.commons == "" {
		return nil, "", "", ""
	}
	q := url.Values{
		"action": {"query"}, "format": {"json"}, "formatversion": {"2"},
		"titles": {"File:" + file}, "prop": {"imageinfo"}, "iiprop": {"url|extmetadata|mime"}, "iiurlwidth": {"500"},
		"iiextmetadatafilter": {"Artist|LicenseShortName|License|NonFree|Restrictions"},
	}
	body, _, err := m.get(ctx, m.commons+"?"+q.Encode(), 1<<20)
	if err != nil {
		return nil, "", "", ""
	}
	var res struct {
		Query struct {
			Pages []struct {
				Repository string `json:"imagerepository"`
				Info       []struct {
					Thumb string `json:"thumburl"`
					Page  string `json:"descriptionurl"`
					Mime  string `json:"mime"`
					Meta  map[string]struct {
						Value string `json:"value"`
					} `json:"extmetadata"`
				} `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
	}
	if json.Unmarshal(body, &res) != nil || len(res.Query.Pages) != 1 || res.Query.Pages[0].Repository != "local" || len(res.Query.Pages[0].Info) != 1 {
		return nil, "", "", ""
	}
	info := res.Query.Pages[0].Info[0]
	meta := func(k string) string { return strings.TrimSpace(info.Meta[k].Value) }
	if !freeLicence(meta("License")) || (meta("NonFree") != "" && meta("NonFree") != "false") || meta("Restrictions") != "" ||
		!m.onWikimedia(info.Thumb) || !strings.HasPrefix(info.Page, "https://commons.wikimedia.org/wiki/File:") {
		return nil, "", "", ""
	}
	if img, name = m.image(ctx, info.Thumb); img == nil {
		return nil, "", "", ""
	}
	who := strings.Join(strings.Fields(html.UnescapeString(stripTags(meta("Artist")))), " ")
	if who == "" || len(who) > 80 {
		who = "wikimedia commons"
	}
	licence := strings.ToLower(meta("LicenseShortName"))
	if licence == "" || len(licence) > 40 {
		licence = strings.ReplaceAll(meta("License"), "-", " ")
	}
	credit = m.screen.Check(strings.ToLower(who)).Text + ", " + licence
	return img, name, markdown.Replace(credit), info.Page
}

// onCommons is the file name of lead, when it is an original on Commons
// rather than a file held by one Wikipedia, which is where unfree ones live.
func (m *Module) onCommons(lead string) (string, bool) {
	u, err := url.Parse(lead)
	if err != nil || len(m.wikimedia) == 0 {
		return "", false
	}
	u.RawQuery = ""
	if !strings.HasPrefix(u.String(), m.wikimedia[0]) {
		return "", false
	}
	file := path.Base(u.Path)
	return file, file != "" && file != "." && file != "/"
}

func (m *Module) onWikimedia(u string) bool {
	for _, p := range m.wikimedia {
		if strings.HasPrefix(u, p) && !strings.Contains(u[len(p):], "..") {
			return true
		}
	}
	return false
}

// freeLicence is a Commons licence template skua may repost under with
// credit: CC0, public domain, or CC BY and BY-SA. Commons takes nothing
// else, and this holds it to that.
func freeLicence(l string) bool {
	l = strings.ToLower(l)
	switch {
	case l == "cc0", l == "pd", strings.HasPrefix(l, "pd-"), l == "public domain":
		return true
	case strings.HasPrefix(l, "cc-by"):
		return !strings.Contains(l, "-nc") && !strings.Contains(l, "-nd")
	}
	return false
}

// stripTags drops the markup Commons wraps an author's name in.
func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// line is one sentence of Wikipedia's article for rec's scientific name,
// when that article is the one iNaturalist links for the same taxon, with
// the article's lead image.
func (m *Module) line(ctx context.Context, rec recording, linked string) (text, article, lead string) {
	want := articleOf(linked)
	if want == "" || m.wiki == "" {
		return "", "", ""
	}
	body, _, err := m.get(ctx, m.wiki+url.PathEscape(rec.Gen+"_"+rec.Sp), 1<<20)
	if err != nil {
		return "", "", ""
	}
	var s struct {
		Type   string `json:"type"`
		Titles struct {
			Canonical string `json:"canonical"`
		} `json:"titles"`
		Description string `json:"description"`
		Extract     string `json:"extract"`
		Image       struct {
			Source string `json:"source"`
		} `json:"originalimage"`
	}
	if json.Unmarshal(body, &s) != nil || s.Type != "standard" || !strings.EqualFold(s.Titles.Canonical, want) {
		return "", "", ""
	}
	lead = s.Image.Source
	h := fnv.New32a()
	_, _ = h.Write([]byte(rec.ID))
	text = pick(sentences(s.Extract), h.Sum32())
	if text == "" {
		text = s.Description
	}
	text = strings.Join(strings.Fields(strings.ToLower(text)), " ")
	if text == "" || len(text) > 220 {
		return "", "", lead
	}
	return markdown.Replace(m.screen.Check(text).Text), "https://en.wikipedia.org/wiki/" + url.PathEscape(s.Titles.Canonical), lead
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
