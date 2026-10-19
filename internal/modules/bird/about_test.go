package bird

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/sanction"
)

var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'j', 'f', 'i', 'f'}

const blackbird = `The common blackbird is a species of true thrush. It is also known as the Eurasian blackbird, or simply the blackbird. It breeds in Europe, western Asia, and North Africa, and has been introduced to Australia and New Zealand.`

// lookups serves iNaturalist, Wikipedia and the photo bucket, each answer
// editable by the test.
type lookups struct {
	taxa, summary string
	page          string // the taxon's own page, with its curated photos
	commons       string // Commons' answer for the article's lead image
	photo         []byte
	kind          string
}

func (l *lookups) serve(t *testing.T) (*Module, *httptest.Server) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/taxa":
			_, _ = fmt.Fprint(w, l.taxa)
		case r.URL.Path == "/taxa/12716" && l.page != "":
			_, _ = fmt.Fprint(w, l.page)
		case r.URL.Path == "/wiki/Turdus_merula":
			_, _ = fmt.Fprint(w, l.summary)
		case strings.HasPrefix(r.URL.Path, "/photos/"), strings.HasPrefix(r.URL.Path, "/thumbs/"):
			w.Header().Set("Content-Type", l.kind)
			_, _ = w.Write(l.photo)
		case r.URL.Path == "/commons" && l.commons != "":
			if r.URL.Query().Get("titles") != "File:Pyriglena_maura_male.jpg" {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprint(w, l.commons)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	m := New(guard.New(), &fakePoster{}, filter.Default(), "k")
	m.inat, m.wiki, m.photos = srv.URL+"/taxa", srv.URL+"/wiki/", srv.URL+"/photos/"
	m.commons, m.wikimedia = srv.URL+"/commons", []string{srv.URL + "/files/", srv.URL + "/thumbs/"}
	return m, srv
}

func taxa(photoURL, name, licence, flags, wiki string) string {
	return fmt.Sprintf(`{"results":[
		{"id":1,"name":"Turdus merula merula","rank":"subspecies","is_active":true,"iconic_taxon_name":"Aves"},
		{"id":12716,"name":%q,"rank":"species","is_active":true,"iconic_taxon_name":"Aves","wikipedia_url":%q,
		 "default_photo":{"id":356885346,"license_code":%q,"attribution":"(c) Luiz Lapa, some rights reserved (CC BY)","medium_url":%q,"flags":%s}}]}`,
		name, wiki, licence, photoURL, flags)
}

func summary(title string) string {
	return fmt.Sprintf(`{"type":"standard","titles":{"canonical":%q},"description":"Thrush native to Europe","extract":%q}`, title, blackbird)
}

var merula = recording{ID: "42", En: "Common Blackbird", Gen: "Turdus", Sp: "merula"}

func TestLookUpMatchesThePhotoAndTheLine(t *testing.T) {
	l := &lookups{photo: jpeg, kind: "image/jpeg", summary: summary("Common_blackbird")}
	m, srv := l.serve(t)
	l.taxa = taxa(srv.URL+"/photos/1/medium.jpg", "Turdus merula", "cc-by", "[]", "http://en.wikipedia.org/wiki/Common_blackbird")
	a := m.lookUp(context.Background(), merula)
	if string(a.photo) != string(jpeg) || a.photoName != "bird.jpg" || a.source != "https://www.inaturalist.org/photos/356885346" {
		t.Fatalf("photo %q %s %s", a.photo, a.photoName, a.source)
	}
	if a.credit != "luiz lapa, cc by" {
		t.Errorf("credit %q", a.credit)
	}
	if !strings.HasPrefix(a.line, "it ") || !strings.HasSuffix(a.line, ".") || strings.ContainsAny(a.line, "EB") {
		t.Errorf("line %q", a.line)
	}
	lines := a.lines()
	if !strings.Contains(lines, "\n-# it ") || !strings.Contains(lines, "[wikipedia](<https://en.wikipedia.org/wiki/Common_blackbird>)") ||
		!strings.Contains(lines, "\n-# photo [luiz lapa, cc by](<https://www.inaturalist.org/photos/356885346>)") {
		t.Errorf("lines %q", lines)
	}
	// One prank, one line: every post of it says the same thing.
	if again := m.lookUp(context.Background(), merula); again.line != a.line {
		t.Errorf("the line moved: %q then %q", a.line, again.line)
	}
}

func TestLookUpDropsWhatDoesntMatch(t *testing.T) {
	cases := []struct {
		name                                   string
		photoPath, taxon, licence, flags, wiki string
		kind                                   string
		photo                                  []byte
		title                                  string
		wantPhoto, wantLine                    bool
	}{
		{name: "another species", taxon: "Turdus philomelos", wiki: "/Common_blackbird"},
		{name: "a closed licence", licence: "cc-by-nc", wantLine: true},
		{name: "no licence", licence: "", wantLine: true},
		{name: "a flagged photo", flags: `[{"flag":"x"}]`, wantLine: true},
		{name: "another host", photoPath: "http://example.com/photos/1.jpg", wantLine: true},
		{name: "climbing out of the bucket", photoPath: "/photos/../x.jpg", wantLine: true},
		{name: "not an image", kind: "text/html", photo: []byte("<html>"), wantLine: true},
		{name: "a jpeg that isn't", kind: "image/jpeg", photo: []byte("nope"), wantLine: true},
		{name: "too big", photo: append(append([]byte{}, jpeg...), make([]byte, maxPhoto)...), wantLine: true},
		{name: "articles disagree", title: "Song_thrush", wantPhoto: true},
		{name: "no article linked", wiki: "none", wantPhoto: true},
	}
	for _, c := range cases {
		l := &lookups{photo: c.photo, kind: c.kind}
		if l.photo == nil {
			l.photo = jpeg
		}
		if l.kind == "" {
			l.kind = "image/jpeg"
		}
		m, srv := l.serve(t)
		photo := srv.URL + "/photos/1/medium.jpg"
		switch {
		case strings.HasPrefix(c.photoPath, "http"):
			photo = c.photoPath
		case c.photoPath != "":
			photo = srv.URL + c.photoPath
		}
		taxon, licence, flags, wiki, title := "Turdus merula", "cc-by", "[]", "https://en.wikipedia.org/wiki/Common_blackbird", "Common_blackbird"
		if c.taxon != "" {
			taxon = c.taxon
		}
		if c.name != "a closed licence" && c.name != "no licence" {
			c.licence = licence
		}
		if c.flags != "" {
			flags = c.flags
		}
		if c.wiki == "none" {
			wiki = ""
		}
		if c.title != "" {
			title = c.title
		}
		l.taxa, l.summary = taxa(photo, taxon, c.licence, flags, wiki), summary(title)
		a := m.lookUp(context.Background(), merula)
		if (a.photo != nil) != c.wantPhoto || (a.line != "") != c.wantLine {
			t.Errorf("%s: photo %v line %q", c.name, a.photo != nil, a.line)
		}
		if !c.wantPhoto && strings.Contains(a.lines(), "photo") {
			t.Errorf("%s: credited a photo it didn't post", c.name)
		}
	}
}

// A default photo licensed for non-commercial use only gives way to the
// first open one the taxon's curators chose.
func TestLookUpFallsBackToACuratedPhoto(t *testing.T) {
	l := &lookups{photo: jpeg, kind: "image/jpeg", summary: summary("Common_blackbird")}
	m, srv := l.serve(t)
	l.taxa = taxa(srv.URL+"/photos/1/medium.jpg", "Turdus merula", "cc-by-nc", "[]", "http://en.wikipedia.org/wiki/Common_blackbird")
	l.page = fmt.Sprintf(`{"results":[{"id":12716,"taxon_photos":[
		{"photo":{"license_code":"cc-by-nc","attribution":"(c) A","medium_url":"%[1]s/photos/2/medium.jpg","flags":[]}},
		{"photo":{"license_code":"cc-by-sa","attribution":"(c) B","medium_url":"http://elsewhere/3.jpg","flags":[]}},
		{"photo":{"license_code":"cc-by-sa","attribution":"(c) Caroline Legg, some rights reserved (CC BY-SA)","medium_url":"%[1]s/photos/4/medium.jpg","flags":[]}}]}]}`, srv.URL)
	if a := m.lookUp(context.Background(), merula); a.photo == nil || a.credit != "caroline legg, cc by-sa" {
		t.Fatalf("photo %v credit %q", a.photo != nil, a.credit)
	}
	l.page = `{"results":[{"id":12716,"taxon_photos":[{"photo":{"license_code":null,"medium_url":"x"}}]}]}`
	if a := m.lookUp(context.Background(), merula); a.photo != nil || a.line == "" {
		t.Fatal("no open photo, yet one was posted, or the line went with it")
	}
}

// commonsAnswer is Commons' imageinfo for one file, as the API sends it.
func commonsAnswer(base, repo, licence, nonFree, restrictions, thumb string) string {
	return fmt.Sprintf(`{"query":{"pages":[{"imagerepository":%q,"imageinfo":[{"thumburl":%q,
		"descriptionurl":"https://commons.wikimedia.org/wiki/File:Pyriglena_maura_male.jpg","mime":"image/jpeg",
		"extmetadata":{"Artist":{"value":"<a href=\"//commons.wikimedia.org/wiki/User:Hector_Bottai\">Hector Bottai</a>"},
		"LicenseShortName":{"value":"CC BY-SA 4.0"},"License":{"value":%q},"NonFree":{"value":%q},"Restrictions":{"value":%q}}}]}]}}`,
		repo, base+thumb, licence, nonFree, restrictions)
}

// With no open photo on iNaturalist, the article's lead image comes from
// Commons, and only from Commons, only when it is free and unrestricted.
func TestLookUpFallsBackToCommons(t *testing.T) {
	l := &lookups{photo: jpeg, kind: "image/jpeg"}
	m, srv := l.serve(t)
	l.taxa = taxa(srv.URL+"/photos/1/medium.jpg", "Turdus merula", "cc-by-nc", "[]", "http://en.wikipedia.org/wiki/Common_blackbird")
	withLead := func(lead string) string {
		return fmt.Sprintf(`{"type":"standard","titles":{"canonical":"Common_blackbird"},"extract":%q,"originalimage":{"source":%q}}`, blackbird, lead)
	}
	l.summary = withLead(srv.URL + "/files/a/a6/Pyriglena_maura_male.jpg?utm_source=x")
	l.commons = commonsAnswer(srv.URL, "local", "cc-by-sa-4.0", "", "", "/thumbs/a/a6/500px-x.jpg")
	a := m.lookUp(context.Background(), merula)
	if a.photo == nil || a.credit != "hector bottai, cc by-sa 4.0" || a.source != "https://commons.wikimedia.org/wiki/File:Pyriglena_maura_male.jpg" || a.line == "" {
		t.Fatalf("photo %v credit %q source %q line %q", a.photo != nil, a.credit, a.source, a.line)
	}
	cases := map[string]func(){
		"held by one wikipedia": func() { l.summary = withLead(srv.URL + "/wikipedia/en/a/a6/Pyriglena_maura_male.jpg") },
		"a non-commercial licence": func() {
			l.commons = commonsAnswer(srv.URL, "local", "cc-by-nc-sa-4.0", "", "", "/thumbs/x.jpg")
		},
		"marked unfree": func() { l.commons = commonsAnswer(srv.URL, "local", "cc-by-sa-4.0", "true", "", "/thumbs/x.jpg") },
		"restricted": func() {
			l.commons = commonsAnswer(srv.URL, "local", "cc-by-sa-4.0", "", "personality", "/thumbs/x.jpg")
		},
		"not on commons": func() { l.commons = commonsAnswer(srv.URL, "shared", "cc-by-sa-4.0", "", "", "/thumbs/x.jpg") },
		"a foreign thumb": func() {
			l.commons = commonsAnswer("http://elsewhere", "local", "cc-by-sa-4.0", "", "", "/thumbs/x.jpg")
		},
		"no lead image": func() { l.summary = withLead("") },
	}
	for name, set := range cases {
		l.summary = withLead(srv.URL + "/files/a/a6/Pyriglena_maura_male.jpg")
		l.commons = commonsAnswer(srv.URL, "local", "cc-by-sa-4.0", "", "", "/thumbs/a/a6/500px-x.jpg")
		set()
		if a := m.lookUp(context.Background(), merula); a.photo != nil {
			t.Errorf("%s: posted a photo", name)
		}
	}
	for l, want := range map[string]bool{"cc0": true, "pd": true, "pd-old-70": true, "cc-by-4.0": true, "cc-by-sa-3.0": true, "cc-by-nc-4.0": false, "cc-by-nd-2.0": false, "gfdl": false, "": false} {
		if freeLicence(l) != want {
			t.Errorf("freeLicence(%q) = %v", l, !want)
		}
	}
	if stripTags(`<a href="x">Hector <b>Bottai</b></a>`) != "Hector Bottai" {
		t.Fatal("stripTags")
	}
}

func TestLookUpNeedsAScientificName(t *testing.T) {
	m := New(guard.New(), &fakePoster{}, filter.Default(), "k")
	m.inat = "http://127.0.0.1:1/never"
	if a := m.lookUp(context.Background(), recording{ID: "1", En: "Identity unknown", Gen: "Mystery"}); a.photo != nil || a.line != "" {
		t.Fatal("looked up half a name")
	}
	l := &lookups{taxa: "not json"}
	m, _ = l.serve(t)
	if a := m.lookUp(context.Background(), merula); a.photo != nil {
		t.Fatal("a broken answer found a photo")
	}
}

func TestSentences(t *testing.T) {
	got := sentences("The bird lives on St. Helena and nearby islands all year. It was described by J. Smith in 1830. Its call is a loud harsh rattle given in flight. Short one here. It nests (rarely) in cliffs and on old walls too.")
	// "St. Helena" and "J. Smith" stay whole; the short one and the one
	// with a parenthesis go.
	if len(got) != 3 || got[0] != "The bird lives on St. Helena and nearby islands all year." || got[2] != "Its call is a loud harsh rattle given in flight." {
		t.Fatalf("sentences %q", got)
	}
	if pick(nil, 3) != "" {
		t.Fatal("picked from nothing")
	}
}

func TestTurnExtendsABirdWithoutFetching(t *testing.T) {
	srv, asked := xenoCanto(t, http.StatusOK)
	m := module(srv.URL)
	now := time.Now()
	m.now = func() time.Time { return now }
	if sp, err := m.Turn(context.Background(), 3, 7, "2", 10*time.Minute); err != nil || sp != "eurasian wren" {
		t.Fatalf("turn %q %v", sp, err)
	}
	n := len(*asked)
	if sp, err := m.Turn(context.Background(), 3, 7, "2", 40*time.Minute); err != nil || sp != "eurasian wren" || len(*asked) != n {
		t.Fatalf("second turn %q %v, asked %d more", sp, err, len(*asked)-n)
	}
	if !m.Bird(3, 7) || m.Bird(3, 8) || !mustPrank(t, m).end().Equal(now.Add(40*time.Minute)) {
		t.Fatal("the bird didn't last the longer turn")
	}
	// A shorter one never cuts a bird short.
	_, _ = m.Turn(context.Background(), 3, 7, "2", time.Minute)
	if !mustPrank(t, m).end().Equal(now.Add(40 * time.Minute)) {
		t.Fatal("a shorter turn cut the bird short")
	}
	m.turning.Store(target{3, 9}, struct{}{})
	if _, err := m.Turn(context.Background(), 3, 9, "2", time.Minute); err != errBusy {
		t.Fatalf("a second fetch at once: %v", err)
	}
	m.key = ""
	if _, err := m.Turn(context.Background(), 3, 10, "2", time.Minute); err != errNoKey {
		t.Fatalf("no key: %v", err)
	}
}

func TestSanction(t *testing.T) {
	srv, _ := xenoCanto(t, http.StatusOK)
	m := module(srv.URL)
	s := m.Sanction()
	sn := sanction.Sentence{Offence: sanction.Offence{Guild: 3, User: 7, Rule: "spam"}, For: 20 * time.Minute}
	if said, err := s.Apply(context.Background(), sn); s.Name() != "bird" || err != nil || said != "a eurasian wren for 20m" {
		t.Fatalf("said %q %v", said, err)
	}
	if mustPrank(t, m).by != "spam" {
		t.Fatal("the audit log won't say spam started it")
	}
	m.key = ""
	if _, err := s.Apply(context.Background(), sanction.Sentence{Offence: sanction.Offence{Guild: 3, User: 8}}); err != sanction.ErrUnavailable {
		t.Fatalf("no key: %v", err)
	}
	if article("upland sandpiper") != "an" || article("ural owl") != "a" {
		t.Fatal("articles")
	}
}
